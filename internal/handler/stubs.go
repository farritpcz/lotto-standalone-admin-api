// Package handler — stubs.go → IMPLEMENTED handlers
// ทุก handler ตอนนี้ใช้ GORM query DB จริง (ไม่ใช่ stub แล้ว)
//
// ⭐ admin-api ส่วนใหญ่เป็น CRUD → ใช้ GORM query ตรงไม่ต้อง service layer
// ยกเว้น SubmitResult ที่ต้องใช้ lotto-core payout
//
// ความสัมพันธ์:
// - share DB "lotto_standalone" กับ member-api (#3)
// - provider-backoffice-api (#9) มี handlers คล้ายกัน (เพิ่ม operator scope)
package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"github.com/farritpcz/lotto-standalone-admin-api/internal/job"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

// =============================================================================
// Helper — JSON response
// =============================================================================

func ok(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}
func fail(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{"success": false, "error": msg})
}
func paginated(c *gin.Context, items interface{}, total int64, page, perPage int) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"items": items, "total": total, "page": page, "per_page": perPage},
	})
}
func pageParams(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "20"))
	if page < 1 { page = 1 }
	if perPage < 1 || perPage > 100 { perPage = 20 }
	return page, perPage
}

// =============================================================================
// Auth — Admin Login
// =============================================================================

func (h *Handler) AdminLogin(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error()); return
	}

	var admin model.Admin
	if err := h.DB.Where("username = ?", req.Username).First(&admin).Error; err != nil {
		fail(c, 401, "invalid credentials"); return
	}
	if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.Password)) != nil {
		fail(c, 401, "invalid credentials"); return
	}
	if admin.Status != "active" {
		fail(c, 403, "account suspended"); return
	}

	// อัพเดท last login
	now := time.Now()
	h.DB.Model(&admin).Update("last_login_at", &now)

	// สร้าง JWT token จริง
	token, err := middleware.GenerateAdminToken(admin.ID, admin.Username, admin.Role, h.AdminJWTSecret, h.AdminJWTExpiryHours)
	if err != nil {
		fail(c, 500, "failed to generate token")
		return
	}
	ok(c, gin.H{"admin": admin, "token": token})
}

// =============================================================================
// Dashboard
// =============================================================================

func (h *Handler) GetDashboard(c *gin.Context) {
	var stats struct {
		TotalMembers  int64   `json:"total_members"`
		ActiveMembers int64   `json:"active_members"`
		TotalBets     int64   `json:"total_bets_today"`
		TotalAmount   float64 `json:"total_amount_today"`
		TotalWin      float64 `json:"total_win_today"`
		OpenRounds    int64   `json:"open_rounds"`
	}

	today := time.Now().Format("2006-01-02")

	h.DB.Model(&model.Member{}).Count(&stats.TotalMembers)
	h.DB.Model(&model.Member{}).Where("status = ?", "active").Count(&stats.ActiveMembers)
	h.DB.Model(&model.Bet{}).Where("DATE(created_at) = ?", today).Count(&stats.TotalBets)
	h.DB.Model(&model.Bet{}).Where("DATE(created_at) = ?", today).Select("COALESCE(SUM(amount), 0)").Scan(&stats.TotalAmount)
	h.DB.Model(&model.Bet{}).Where("DATE(created_at) = ? AND status = ?", today, "won").Select("COALESCE(SUM(win_amount), 0)").Scan(&stats.TotalWin)
	h.DB.Model(&model.LotteryRound{}).Where("status = ?", "open").Count(&stats.OpenRounds)

	ok(c, stats)
}

// =============================================================================
// Members CRUD
// =============================================================================

func (h *Handler) ListMembers(c *gin.Context) {
	page, perPage := pageParams(c)
	var members []model.Member
	var total int64
	query := h.DB.Model(&model.Member{})
	if s := c.Query("status"); s != "" { query = query.Where("status = ?", s) }
	if q := c.Query("q"); q != "" { query = query.Where("username LIKE ? OR phone LIKE ?", "%"+q+"%", "%"+q+"%") }
	query.Count(&total)
	query.Order("created_at DESC").Offset((page - 1) * perPage).Limit(perPage).Find(&members)
	paginated(c, members, total, page, perPage)
}

func (h *Handler) GetMember(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var member model.Member
	if err := h.DB.First(&member, id).Error; err != nil { fail(c, 404, "member not found"); return }

	// ── Aggregated data: ยอดแทง/ชนะ/ฝาก/ถอน + referrer username ──
	type AggBet struct {
		TotalBets      int64   `json:"total_bets"`
		TotalBetAmount float64 `json:"total_bet_amount"`
		TotalWinAmount float64 `json:"total_win_amount"`
	}
	var agg AggBet
	h.DB.Raw(`
		SELECT COUNT(*) as total_bets,
		       COALESCE(SUM(amount), 0) as total_bet_amount,
		       COALESCE(SUM(CASE WHEN status = 'won' THEN win_amount ELSE 0 END), 0) as total_win_amount
		FROM bets WHERE member_id = ?
	`, id).Scan(&agg)

	type AggTx struct {
		TotalDeposit  float64 `json:"total_deposit"`
		TotalWithdraw float64 `json:"total_withdraw"`
	}
	var aggTx AggTx
	h.DB.Raw(`
		SELECT COALESCE(SUM(CASE WHEN type IN ('deposit','admin_credit') THEN amount ELSE 0 END), 0) as total_deposit,
		       COALESCE(SUM(CASE WHEN type IN ('withdraw','admin_debit') THEN ABS(amount) ELSE 0 END), 0) as total_withdraw
		FROM transactions WHERE member_id = ?
	`, id).Scan(&aggTx)

	// referrer username (ถ้ามี referred_by)
	var referrerUsername string
	if member.ReferredBy != nil {
		var referrer model.Member
		if err := h.DB.Select("username").First(&referrer, *member.ReferredBy).Error; err == nil {
			referrerUsername = referrer.Username
		}
	}

	// สร้าง response รวม member + aggregated data
	resp := gin.H{
		"id":                  member.ID,
		"username":            member.Username,
		"phone":               member.Phone,
		"email":               member.Email,
		"balance":             member.Balance,
		"status":              member.Status,
		"referred_by":         member.ReferredBy,
		"bank_code":           member.BankCode,
		"bank_account_number": member.BankAccountNumber,
		"bank_account_name":   member.BankAccountName,
		"created_at":          member.CreatedAt,
		"updated_at":          member.UpdatedAt,
		// aggregated
		"referrer_username": referrerUsername,
		"total_bets":        agg.TotalBets,
		"total_bet_amount":  agg.TotalBetAmount,
		"total_win_amount":  agg.TotalWinAmount,
		"total_deposit":     aggTx.TotalDeposit,
		"total_withdraw":    aggTx.TotalWithdraw,
	}
	ok(c, resp)
}

func (h *Handler) UpdateMember(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var member model.Member
	if err := h.DB.First(&member, id).Error; err != nil { fail(c, 404, "member not found"); return }
	var req struct {
		Phone string `json:"phone"`
		Email string `json:"email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil { fail(c, 400, err.Error()); return }
	if req.Phone != "" { member.Phone = req.Phone }
	if req.Email != "" { member.Email = req.Email }
	h.DB.Save(&member)
	ok(c, member)
}

// AdjustMemberBalance แอดมินเติม/หักเครดิตสมาชิกตรง
// PUT /api/v1/members/:id/balance
// Body: { "amount": 500, "note": "เติมเครดิตโดยแอดมิน" }
// amount เป็นบวก = เติม, ลบ = หัก
func (h *Handler) AdjustMemberBalance(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		Amount float64 `json:"amount" binding:"required"`
		Note   string  `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil { fail(c, 400, err.Error()); return }

	var member model.Member
	if err := h.DB.First(&member, id).Error; err != nil { fail(c, 404, "member not found"); return }

	// เช็คว่าหักแล้วไม่ติดลบ
	if req.Amount < 0 && member.Balance+req.Amount < 0 {
		fail(c, 400, "ยอดเงินไม่เพียงพอ"); return
	}

	tx := h.DB.Begin()
	now := time.Now()

	// อัพเดทยอดเงิน
	tx.Model(&model.Member{}).Where("id = ?", id).Update("balance", h.DB.Raw("balance + ?", req.Amount))

	// สร้าง transaction record
	txType := "admin_credit"
	if req.Amount < 0 { txType = "admin_debit" }
	tx.Exec(`INSERT INTO transactions (member_id, type, amount, balance_before, balance_after, reference_type, note, created_at)
		VALUES (?, ?, ?, ?, ?, 'admin_adjust', ?, ?)`,
		id, txType, req.Amount, member.Balance, member.Balance+req.Amount, req.Note, now)

	tx.Commit()
	ok(c, gin.H{"member_id": id, "amount": req.Amount, "balance_before": member.Balance, "balance_after": member.Balance + req.Amount})
}

func (h *Handler) UpdateMemberStatus(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct { Status string `json:"status" binding:"required"` }
	if err := c.ShouldBindJSON(&req); err != nil { fail(c, 400, err.Error()); return }
	h.DB.Model(&model.Member{}).Where("id = ?", id).Update("status", req.Status)
	ok(c, gin.H{"id": id, "status": req.Status})
}

// =============================================================================
// Lotteries CRUD
// =============================================================================

func (h *Handler) ListLotteries(c *gin.Context) {
	var types []model.LotteryType
	h.DB.Order("id ASC").Find(&types)
	ok(c, types)
}

func (h *Handler) CreateLottery(c *gin.Context) {
	var lt model.LotteryType
	if err := c.ShouldBindJSON(&lt); err != nil { fail(c, 400, err.Error()); return }
	if err := h.DB.Create(&lt).Error; err != nil { fail(c, 500, "failed to create"); return }
	ok(c, lt)
}

func (h *Handler) UpdateLottery(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var lt model.LotteryType
	if err := h.DB.First(&lt, id).Error; err != nil { fail(c, 404, "not found"); return }
	if err := c.ShouldBindJSON(&lt); err != nil { fail(c, 400, err.Error()); return }
	h.DB.Save(&lt)
	ok(c, lt)
}

// =============================================================================
// Rounds
// =============================================================================

func (h *Handler) ListRounds(c *gin.Context) {
	page, perPage := pageParams(c)
	var rounds []model.LotteryRound
	var total int64
	query := h.DB.Model(&model.LotteryRound{}).Preload("LotteryType")
	if s := c.Query("status"); s != "" { query = query.Where("status = ?", s) }
	if lt := c.Query("lottery_type_id"); lt != "" { query = query.Where("lottery_type_id = ?", lt) }
	query.Count(&total)
	query.Order("round_date DESC, close_time DESC").Offset((page - 1) * perPage).Limit(perPage).Find(&rounds)
	paginated(c, rounds, total, page, perPage)
}

func (h *Handler) CreateRound(c *gin.Context) {
	var round model.LotteryRound
	if err := c.ShouldBindJSON(&round); err != nil { fail(c, 400, err.Error()); return }
	round.Status = "upcoming"
	if err := h.DB.Create(&round).Error; err != nil { fail(c, 500, "failed to create round"); return }
	ok(c, round)
}

func (h *Handler) UpdateRoundStatus(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct { Status string `json:"status" binding:"required"` }
	if err := c.ShouldBindJSON(&req); err != nil { fail(c, 400, err.Error()); return }
	h.DB.Model(&model.LotteryRound{}).Where("id = ?", id).Update("status", req.Status)
	ok(c, gin.H{"id": id, "status": req.Status})
}

// =============================================================================
// Results — ⭐ กรอกผลรางวัล (trigger payout ผ่าน lotto-core)
// =============================================================================

// PreviewResult ดูตัวอย่างก่อนกรอกผล — ใครจะถูก จ่ายเท่าไร
// POST /api/v1/results/:roundId/preview
// Body: { "top3": "999", "top2": "99", "bottom2": "56" }
// ⭐ ไม่บันทึกอะไร แค่คำนวณแล้วส่งกลับ
func (h *Handler) PreviewResult(c *gin.Context) {
	roundID, _ := strconv.ParseInt(c.Param("roundId"), 10, 64)
	var req struct {
		Top3    string `json:"top3" binding:"required"`
		Top2    string `json:"top2" binding:"required"`
		Bottom2 string `json:"bottom2" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil { fail(c, 400, err.Error()); return }

	// ดึง round
	var round model.LotteryRound
	if err := h.DB.First(&round, roundID).Error; err != nil { fail(c, 404, "round not found"); return }

	// ดึง bets ทั้งหมดของรอบ (pending หรือ won/lost ถ้าออกผลไปแล้ว)
	var bets []model.Bet
	if round.Status == "resulted" {
		// รอบ resulted แล้ว → ดึงทุก status
		h.DB.Where("lottery_round_id = ?", roundID).
			Preload("BetType").Preload("Member").Find(&bets)
	} else {
		// รอบยังไม่ resulted → ดึงเฉพาะ pending
		h.DB.Where("lottery_round_id = ? AND status = ?", roundID, "pending").
			Preload("BetType").Preload("Member").Find(&bets)
	}

	// คำนวณว่าใครถูก
	type WinnerInfo struct {
		BetID    int64   `json:"bet_id"`
		MemberID int64   `json:"member_id"`
		Username string  `json:"username"`
		Number   string  `json:"number"`
		BetType  string  `json:"bet_type"`
		Amount   float64 `json:"amount"`
		Rate     float64 `json:"rate"`
		Payout   float64 `json:"payout"`
	}

	var winners []WinnerInfo
	totalPayout := 0.0
	totalBets := len(bets)
	totalAmount := 0.0

	for _, bet := range bets {
		totalAmount += bet.Amount
		betTypeCode := ""
		betTypeName := ""
		if bet.BetType != nil { betTypeCode = bet.BetType.Code; betTypeName = bet.BetType.Name }

		isWin := false
		switch betTypeCode {
		case "3TOP":
			isWin = bet.Number == req.Top3
		case "3TOD":
			// โต๊ด: เลขตรงกันทุกตัว (ไม่สนลำดับ)
			isWin = sortString(bet.Number) == sortString(req.Top3)
		case "2TOP":
			isWin = bet.Number == req.Top2
		case "2BOTTOM":
			isWin = bet.Number == req.Bottom2
		case "RUN_TOP":
			isWin = containsDigit(req.Top3, bet.Number) || containsDigit(req.Top2, bet.Number)
		case "RUN_BOT":
			isWin = containsDigit(req.Bottom2, bet.Number)
		}

		if isWin {
			payout := bet.Amount * bet.Rate
			username := ""
			if bet.Member != nil { username = bet.Member.Username }
			winners = append(winners, WinnerInfo{
				BetID: bet.ID, MemberID: bet.MemberID, Username: username,
				Number: bet.Number, BetType: betTypeName,
				Amount: bet.Amount, Rate: bet.Rate, Payout: payout,
			})
			totalPayout += payout
		}
	}

	ok(c, gin.H{
		"round_id":     roundID,
		"round_number": round.RoundNumber,
		"result":       gin.H{"top3": req.Top3, "top2": req.Top2, "bottom2": req.Bottom2},
		"total_bets":   totalBets,
		"total_amount":  totalAmount,
		"winners":      winners,
		"winner_count": len(winners),
		"total_payout": totalPayout,
		"profit":       totalAmount - totalPayout,
	})
}

// helper: sort string chars
func sortString(s string) string {
	r := []byte(s)
	for i := 0; i < len(r); i++ {
		for j := i + 1; j < len(r); j++ {
			if r[i] > r[j] { r[i], r[j] = r[j], r[i] }
		}
	}
	return string(r)
}

// helper: check if number contains digit
func containsDigit(result string, digit string) bool {
	for _, c := range result {
		if string(c) == digit { return true }
	}
	return false
}

// SubmitResult กรอกผลรางวัล
// POST /api/v1/results/:roundId
// Body: { "top3": "847", "top2": "47", "bottom2": "56" }
//
// ⭐ Flow หลังกรอกผล:
//  1. บันทึกผลลง lottery_rounds
//  2. ดึง bets ทั้งหมดของรอบ (status = pending)
//  3. lotto-core payout.SettleRound() → เทียบผลทุก bet
//  4. อัพเดท bets: won/lost + win_amount
//  5. จ่ายเงินคนชนะ: members.balance += win_amount
//  6. สร้าง transactions สำหรับคนชนะ
func (h *Handler) SubmitResult(c *gin.Context) {
	roundID, _ := strconv.ParseInt(c.Param("roundId"), 10, 64)

	var req struct {
		Top3    string `json:"top3" binding:"required"`
		Top2    string `json:"top2" binding:"required"`
		Bottom2 string `json:"bottom2" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil { fail(c, 400, err.Error()); return }

	// 1. ดึง round → เช็คว่ายังไม่มีผล
	var round model.LotteryRound
	if err := h.DB.First(&round, roundID).Error; err != nil { fail(c, 404, "round not found"); return }
	if round.Status == "resulted" { fail(c, 400, "round already has result"); return }

	// 2. บันทึกผล
	now := time.Now()
	h.DB.Model(&round).Updates(map[string]interface{}{
		"result_top3":    req.Top3,
		"result_top2":    req.Top2,
		"result_bottom2": req.Bottom2,
		"status":         "resulted",
		"resulted_at":    &now,
	})

	// 3. ดึง bets ทั้งหมดของรอบ (pending)
	var bets []model.Bet
	h.DB.Where("lottery_round_id = ? AND status = ?", roundID, "pending").
		Preload("BetType").Find(&bets)

	// 4-6. ⭐ TODO: เรียก lotto-core payout.SettleRound() + จ่ายเงิน
	// (ตอนนี้ mark as done เพื่อแสดง flow — implement จริงเมื่อ integrate lotto-core)
	// ดู service/result_service.go สำหรับ pseudo code
	settledCount := 0
	totalWin := 0.0

	for _, bet := range bets {
		// Simple matching (TODO: ใช้ lotto-core payout.Match() แทน)
		isWin := false
		winAmount := 0.0
		betTypeCode := ""
		if bet.BetType != nil { betTypeCode = bet.BetType.Code }

		switch betTypeCode {
		case "3TOP":
			isWin = bet.Number == req.Top3
		case "2TOP":
			isWin = bet.Number == req.Top2
		case "2BOTTOM":
			isWin = bet.Number == req.Bottom2
		}

		if isWin {
			winAmount = bet.Amount * bet.Rate
		}

		status := "lost"
		if isWin { status = "won" }

		h.DB.Model(&bet).Updates(map[string]interface{}{
			"status":     status,
			"win_amount": winAmount,
			"settled_at": &now,
		})

		if isWin {
			// จ่ายเงินคนชนะ
			h.DB.Model(&model.Member{}).Where("id = ?", bet.MemberID).
				Update("balance", h.DB.Raw("balance + ?", winAmount))
			totalWin += winAmount
		}
		settledCount++
	}

	// ⭐ Step 7: คำนวณ commission ให้ referrers ของ bettors
	// Run ใน goroutine แยก → ไม่ block response
	// ดู internal/job/commission_job.go สำหรับ logic ทั้งหมด
	go job.CalculateCommissions(h.DB, roundID, 1 /* agentID = 1 สำหรับ standalone */)

	ok(c, gin.H{
		"round_id":      roundID,
		"result":        gin.H{"top3": req.Top3, "top2": req.Top2, "bottom2": req.Bottom2},
		"total_bets":    len(bets),
		"settled":       settledCount,
		"total_win":     totalWin,
	})
}

func (h *Handler) ListResults(c *gin.Context) {
	page, perPage := pageParams(c)
	var rounds []model.LotteryRound
	var total int64
	query := h.DB.Model(&model.LotteryRound{}).Where("status = ?", "resulted").Preload("LotteryType")
	if lt := c.Query("lottery_type_id"); lt != "" { query = query.Where("lottery_type_id = ?", lt) }
	query.Count(&total)
	query.Order("resulted_at DESC").Offset((page - 1) * perPage).Limit(perPage).Find(&rounds)
	paginated(c, rounds, total, page, perPage)
}

// =============================================================================
// Number Bans
// =============================================================================

func (h *Handler) ListBans(c *gin.Context) {
	page, perPage := pageParams(c)
	var bans []model.NumberBan
	var total int64
	query := h.DB.Model(&model.NumberBan{}).Where("status = ?", "active")
	if lt := c.Query("lottery_type_id"); lt != "" { query = query.Where("lottery_type_id = ?", lt) }
	query.Count(&total)
	query.Order("created_at DESC").Offset((page - 1) * perPage).Limit(perPage).Find(&bans)
	paginated(c, bans, total, page, perPage)
}

func (h *Handler) CreateBan(c *gin.Context) {
	var ban model.NumberBan
	if err := c.ShouldBindJSON(&ban); err != nil { fail(c, 400, err.Error()); return }
	ban.Status = "active"
	ban.CreatedAt = time.Now()
	if err := h.DB.Create(&ban).Error; err != nil { fail(c, 500, "failed to create ban"); return }
	ok(c, ban)
}

func (h *Handler) DeleteBan(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	h.DB.Model(&model.NumberBan{}).Where("id = ?", id).Update("status", "inactive")
	ok(c, gin.H{"id": id, "status": "inactive"})
}

// =============================================================================
// Pay Rates
// =============================================================================

func (h *Handler) ListRates(c *gin.Context) {
	var rates []model.PayRate
	query := h.DB.Preload("BetType").Preload("LotteryType").Where("status = ?", "active")
	if lt := c.Query("lottery_type_id"); lt != "" { query = query.Where("lottery_type_id = ?", lt) }
	query.Find(&rates)
	ok(c, rates)
}

func (h *Handler) UpdateRate(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		Rate            *float64 `json:"rate"`
		MaxBetPerNumber *float64 `json:"max_bet_per_number"`
	}
	if err := c.ShouldBindJSON(&req); err != nil { fail(c, 400, err.Error()); return }
	updates := map[string]interface{}{}
	if req.Rate != nil { updates["rate"] = *req.Rate }
	if req.MaxBetPerNumber != nil { updates["max_bet_per_number"] = *req.MaxBetPerNumber }
	h.DB.Model(&model.PayRate{}).Where("id = ?", id).Updates(updates)
	ok(c, gin.H{"id": id, "updated": updates})
}

// =============================================================================
// Bets + Transactions (read-only)
// =============================================================================

func (h *Handler) ListAllBets(c *gin.Context) {
	page, perPage := pageParams(c)
	var bets []model.Bet
	var total int64
	query := h.DB.Model(&model.Bet{}).Preload("Member").Preload("BetType").Preload("LotteryRound")
	if s := c.Query("status"); s != "" { query = query.Where("status = ?", s) }
	if r := c.Query("round_id"); r != "" { query = query.Where("lottery_round_id = ?", r) }
	query.Count(&total)
	query.Order("created_at DESC").Offset((page - 1) * perPage).Limit(perPage).Find(&bets)
	paginated(c, bets, total, page, perPage)
}

func (h *Handler) ListAllTransactions(c *gin.Context) {
	page, perPage := pageParams(c)
	var txns []model.Transaction
	var total int64
	query := h.DB.Model(&model.Transaction{})
	if t := c.Query("type"); t != "" { query = query.Where("type = ?", t) }
	if m := c.Query("member_id"); m != "" { query = query.Where("member_id = ?", m) }
	query.Count(&total)
	query.Order("created_at DESC").Offset((page - 1) * perPage).Limit(perPage).Find(&txns)
	paginated(c, txns, total, page, perPage)
}

// =============================================================================
// Reports
// =============================================================================

func (h *Handler) GetSummaryReport(c *gin.Context) {
	var result struct {
		TotalBets    int64   `json:"total_bets"`
		TotalAmount  float64 `json:"total_amount"`
		TotalWin     float64 `json:"total_win"`
		Profit       float64 `json:"profit"`
	}
	dateFrom := c.DefaultQuery("from", time.Now().AddDate(0, 0, -7).Format("2006-01-02"))
	dateTo := c.DefaultQuery("to", time.Now().Format("2006-01-02"))

	h.DB.Model(&model.Bet{}).Where("DATE(created_at) BETWEEN ? AND ?", dateFrom, dateTo).Count(&result.TotalBets)
	h.DB.Model(&model.Bet{}).Where("DATE(created_at) BETWEEN ? AND ?", dateFrom, dateTo).
		Select("COALESCE(SUM(amount), 0)").Scan(&result.TotalAmount)
	h.DB.Model(&model.Bet{}).Where("DATE(created_at) BETWEEN ? AND ? AND status = ?", dateFrom, dateTo, "won").
		Select("COALESCE(SUM(win_amount), 0)").Scan(&result.TotalWin)
	result.Profit = result.TotalAmount - result.TotalWin

	ok(c, result)
}

func (h *Handler) GetProfitReport(c *gin.Context) {
	// รายงานกำไร/ขาดทุนรายวัน
	dateFrom := c.DefaultQuery("from", time.Now().AddDate(0, 0, -30).Format("2006-01-02"))
	dateTo := c.DefaultQuery("to", time.Now().Format("2006-01-02"))

	type DailyProfit struct {
		Date        string  `json:"date"`
		TotalBets   int64   `json:"total_bets"`
		TotalAmount float64 `json:"total_amount"`
		TotalWin    float64 `json:"total_win"`
		Profit      float64 `json:"profit"`
	}
	var daily []DailyProfit
	h.DB.Model(&model.Bet{}).
		Select("DATE(created_at) as date, COUNT(*) as total_bets, COALESCE(SUM(amount), 0) as total_amount, COALESCE(SUM(CASE WHEN status='won' THEN win_amount ELSE 0 END), 0) as total_win, COALESCE(SUM(amount), 0) - COALESCE(SUM(CASE WHEN status='won' THEN win_amount ELSE 0 END), 0) as profit").
		Where("DATE(created_at) BETWEEN ? AND ?", dateFrom, dateTo).
		Group("DATE(created_at)").
		Order("date ASC").
		Scan(&daily)

	ok(c, daily)
}

// =============================================================================
// Settings
// =============================================================================

func (h *Handler) GetSettings(c *gin.Context) {
	var settings []model.Setting
	h.DB.Find(&settings)
	ok(c, settings)
}

func (h *Handler) UpdateSettings(c *gin.Context) {
	var req map[string]string
	if err := c.ShouldBindJSON(&req); err != nil { fail(c, 400, err.Error()); return }
	for key, value := range req {
		h.DB.Model(&model.Setting{}).Where("`key` = ?", key).Update("value", value)
	}
	ok(c, gin.H{"updated": req})
}

// =============================================================================
// Affiliate Settings — agent ตั้งค่า commission rate ต่อประเภทหวย + withdrawal conditions
//
// GET  /api/v1/admin/affiliate/settings → ดูค่าทั้งหมด (รวม default + per-lottery)
// POST /api/v1/admin/affiliate/settings → upsert: สร้างหรืออัพเดท
// GET  /api/v1/admin/affiliate/report   → รายงาน commission ทั้งหมด
// =============================================================================

func (h *Handler) GetAffiliateSettings(c *gin.Context) {
	var settings []model.AffiliateSettings
	h.DB.Preload("LotteryType").Order("lottery_type_id ASC").Find(&settings)
	ok(c, settings)
}

// UpsertAffiliateSetting สร้างหรืออัพเดท setting
// Body: { "lottery_type_id": null|1, "commission_rate": 0.8, "withdrawal_min": 10, "withdrawal_note": "..." }
func (h *Handler) UpsertAffiliateSetting(c *gin.Context) {
	var req struct {
		LotteryTypeID  *int64  `json:"lottery_type_id"`  // nil = default
		CommissionRate float64 `json:"commission_rate" binding:"required,min=0,max=100"`
		WithdrawalMin  float64 `json:"withdrawal_min"`
		WithdrawalNote string  `json:"withdrawal_note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil { fail(c, 400, err.Error()); return }

	// หา agent_id จาก JWT (ใช้ default 1 สำหรับ standalone)
	agentID := int64(1)

	var existing model.AffiliateSettings
	query := h.DB.Where("agent_id = ?", agentID)
	if req.LotteryTypeID == nil {
		query = query.Where("lottery_type_id IS NULL")
	} else {
		query = query.Where("lottery_type_id = ?", *req.LotteryTypeID)
	}

	if err := query.First(&existing).Error; err != nil {
		// สร้างใหม่
		setting := model.AffiliateSettings{
			AgentID:        agentID,
			LotteryTypeID:  req.LotteryTypeID,
			CommissionRate: req.CommissionRate,
			WithdrawalMin:  req.WithdrawalMin,
			WithdrawalNote: req.WithdrawalNote,
			Status:         "active",
		}
		if err := h.DB.Create(&setting).Error; err != nil { fail(c, 500, "failed to create"); return }
		ok(c, setting)
		return
	}

	// อัพเดท
	updates := map[string]interface{}{
		"commission_rate": req.CommissionRate,
		"withdrawal_min":  req.WithdrawalMin,
		"withdrawal_note": req.WithdrawalNote,
	}
	h.DB.Model(&existing).Updates(updates)
	h.DB.Preload("LotteryType").First(&existing, existing.ID)
	ok(c, existing)
}

func (h *Handler) DeleteAffiliateSetting(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	h.DB.Model(&model.AffiliateSettings{}).Where("id = ?", id).Update("status", "inactive")
	ok(c, gin.H{"id": id, "status": "inactive"})
}

// GetAffiliateReport รายงาน commission ทั้งหมด (สำหรับ agent ดู)
func (h *Handler) GetAffiliateReport(c *gin.Context) {
	type CommSummary struct {
		MemberID         int64   `json:"member_id"`
		Username         string  `json:"username"`
		TotalReferred    int64   `json:"total_referred"`
		TotalCommission  float64 `json:"total_commission"`
		PendingComm      float64 `json:"pending_commission"`
	}
	var report []CommSummary
	h.DB.Table("referral_commissions rc").
		Select("rc.referrer_id as member_id, m.username, COUNT(DISTINCT rc.referred_id) as total_referred, COALESCE(SUM(rc.commission_amount), 0) as total_commission, COALESCE(SUM(CASE WHEN rc.status='pending' THEN rc.commission_amount ELSE 0 END), 0) as pending_commission").
		Joins("LEFT JOIN members m ON m.id = rc.referrer_id").
		Group("rc.referrer_id, m.username").
		Order("total_commission DESC").
		Scan(&report)

	ok(c, report)
}

// =============================================================================
// Deposit Requests — อนุมัติ/ปฏิเสธคำขอฝากเงิน
// =============================================================================

func (h *Handler) ListDepositRequests(c *gin.Context) {
	page, perPage := pageParams(c)
	status := c.DefaultQuery("status", "")

	type DepositRow struct {
		ID        int64   `json:"id"`
		MemberID  int64   `json:"member_id"`
		Username  string  `json:"username"`
		Amount    float64 `json:"amount"`
		Status    string  `json:"status"`
		CreatedAt string  `json:"created_at"`
	}

	var rows []DepositRow
	var total int64

	query := h.DB.Table("deposit_requests d").
		Select("d.id, d.member_id, m.username, d.amount, d.status, d.created_at").
		Joins("LEFT JOIN members m ON m.id = d.member_id")
	if status != "" {
		query = query.Where("d.status = ?", status)
	}
	query.Count(&total)
	query.Order("d.created_at DESC").Offset((page - 1) * perPage).Limit(perPage).Scan(&rows)

	paginated(c, rows, total, page, perPage)
}

// ApproveDeposit อนุมัติคำขอฝากเงิน — เพิ่มเงินให้สมาชิก
// PUT /api/v1/deposits/:id/approve
func (h *Handler) ApproveDeposit(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	adminID := middleware.GetAdminID(c)

	// ดึง request
	var amount float64
	var memberID int64
	var reqStatus string
	row := h.DB.Table("deposit_requests").Select("amount, member_id, status").Where("id = ?", id).Row()
	if err := row.Scan(&amount, &memberID, &reqStatus); err != nil {
		fail(c, 404, "ไม่พบคำขอ"); return
	}
	if reqStatus != "pending" {
		fail(c, 400, "คำขอนี้ไม่ใช่สถานะรอดำเนินการ (สถานะ: "+reqStatus+")"); return
	}

	tx := h.DB.Begin()

	now := time.Now()
	tx.Exec("UPDATE deposit_requests SET status = 'approved', approved_at = ?, approved_by = ? WHERE id = ?", now, adminID, id)

	// เพิ่มเงินให้สมาชิก
	var balanceBefore float64
	tx.Table("members").Select("balance").Where("id = ?", memberID).Row().Scan(&balanceBefore)
	tx.Exec("UPDATE members SET balance = balance + ? WHERE id = ?", amount, memberID)

	// สร้าง transaction record
	tx.Exec(`INSERT INTO transactions (member_id, type, amount, balance_before, balance_after, reference_type, note, created_at)
		VALUES (?, 'deposit', ?, ?, ?, 'deposit_request', ?, ?)`,
		memberID, amount, balanceBefore, balanceBefore+amount, "อนุมัติโดยแอดมิน #"+strconv.FormatInt(adminID, 10), now)

	tx.Commit()
	ok(c, gin.H{"id": id, "status": "approved", "amount": amount, "member_id": memberID})
}

// RejectDeposit ปฏิเสธคำขอฝากเงิน
// PUT /api/v1/deposits/:id/reject
// Body (optional): { "reason": "เหตุผล" }
func (h *Handler) RejectDeposit(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	adminID := middleware.GetAdminID(c)

	var req struct {
		Reason string `json:"reason"`
	}
	c.ShouldBindJSON(&req)
	if req.Reason == "" {
		req.Reason = "ปฏิเสธโดยแอดมิน"
	}

	now := time.Now()
	result := h.DB.Exec(
		"UPDATE deposit_requests SET status = 'rejected', approved_at = ?, reject_reason = ?, approved_by = ? WHERE id = ? AND status = 'pending'",
		now, req.Reason, adminID, id,
	)
	if result.RowsAffected == 0 {
		fail(c, 400, "คำขอนี้ไม่ใช่สถานะรอดำเนินการ"); return
	}

	ok(c, gin.H{"id": id, "status": "rejected", "reason": req.Reason})
}

// CancelDeposit ยกเลิกรายการฝากที่อนุมัติแล้ว (reverse — หักเงินคืน)
// PUT /api/v1/deposits/:id/cancel
// Body (optional): { "reason": "เหตุผล" }
func (h *Handler) CancelDeposit(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	adminID := middleware.GetAdminID(c)

	var req struct {
		Reason string `json:"reason"`
	}
	c.ShouldBindJSON(&req)
	if req.Reason == "" {
		req.Reason = "ยกเลิกโดยแอดมิน"
	}

	// ดึงข้อมูลคำขอ
	var amount float64
	var memberID int64
	var reqStatus string
	row := h.DB.Table("deposit_requests").Select("amount, member_id, status").Where("id = ?", id).Row()
	if err := row.Scan(&amount, &memberID, &reqStatus); err != nil {
		fail(c, 404, "ไม่พบคำขอ"); return
	}
	if reqStatus != "approved" {
		fail(c, 400, "ยกเลิกได้เฉพาะคำขอที่อนุมัติแล้ว (สถานะปัจจุบัน: "+reqStatus+")"); return
	}

	tx := h.DB.Begin()
	now := time.Now()

	// อัพเดท status → cancelled
	tx.Exec("UPDATE deposit_requests SET status = 'cancelled', reject_reason = ?, approved_by = ? WHERE id = ?",
		req.Reason, adminID, id)

	// หักเงินคืน (atomic)
	debitResult := tx.Exec("UPDATE members SET balance = balance - ? WHERE id = ? AND balance >= ?", amount, memberID, amount)
	if debitResult.RowsAffected == 0 {
		tx.Rollback()
		fail(c, 400, "สมาชิกมียอดเงินไม่เพียงพอสำหรับหักคืน"); return
	}

	// ดึง balance ล่าสุด
	var balanceAfter float64
	tx.Table("members").Select("balance").Where("id = ?", memberID).Row().Scan(&balanceAfter)

	// บันทึก transaction
	tx.Exec(`INSERT INTO transactions (member_id, type, amount, balance_before, balance_after, reference_type, note, created_at)
		VALUES (?, 'admin_debit', ?, ?, ?, 'deposit_cancel', ?, ?)`,
		memberID, -amount, balanceAfter+amount, balanceAfter,
		"ยกเลิกฝาก #"+strconv.FormatInt(id, 10)+": "+req.Reason, now)

	tx.Commit()
	ok(c, gin.H{"id": id, "status": "cancelled", "amount": amount, "member_id": memberID, "reason": req.Reason})
}

// =============================================================================
// Withdraw Requests — อนุมัติ/ปฏิเสธคำขอถอนเงิน
//
// ⚠️ IMPORTANT: member-api หักเงินตอนสร้างคำขอแล้ว (atomic debit)
// ดังนั้น:
// - ApproveWithdraw: ไม่ต้องหักเงินอีก (แค่เปลี่ยนสถานะ + บันทึก mode)
// - RejectWithdraw: ต้องคืนเงินให้สมาชิก (เพราะหักไปแล้วตอนสร้างคำขอ)
// =============================================================================

func (h *Handler) ListWithdrawRequests(c *gin.Context) {
	page, perPage := pageParams(c)
	status := c.DefaultQuery("status", "")

	type WithdrawRow struct {
		ID                int64   `json:"id"`
		MemberID          int64   `json:"member_id"`
		Username          string  `json:"username"`
		Amount            float64 `json:"amount"`
		BankCode          string  `json:"bank_code"`
		BankAccountNumber string  `json:"bank_account_number"`
		BankAccountName   string  `json:"bank_account_name"`
		Status            string  `json:"status"`
		CreatedAt         string  `json:"created_at"`
	}

	var rows []WithdrawRow
	var total int64

	query := h.DB.Table("withdraw_requests w").
		Select("w.id, w.member_id, m.username, w.amount, w.bank_code, w.bank_account_number, w.bank_account_name, w.status, w.created_at").
		Joins("LEFT JOIN members m ON m.id = w.member_id")
	if status != "" {
		query = query.Where("w.status = ?", status)
	}
	query.Count(&total)
	query.Order("w.created_at DESC").Offset((page - 1) * perPage).Limit(perPage).Scan(&rows)

	paginated(c, rows, total, page, perPage)
}

// ApproveWithdraw อนุมัติคำขอถอนเงิน
// PUT /api/v1/withdrawals/:id/approve
// Body: { "mode": "auto" | "manual" }
//
// ⚠️ ไม่หักเงินอีก — member-api หักไปแล้วตอนสร้างคำขอ
// แค่เปลี่ยนสถานะ → approved + บันทึก mode การโอน
func (h *Handler) ApproveWithdraw(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	adminID := middleware.GetAdminID(c)

	var req struct {
		Mode string `json:"mode"` // "auto" = โอนอัตโนมัติ, "manual" = โอนเอง
	}
	c.ShouldBindJSON(&req)
	if req.Mode == "" {
		req.Mode = "manual"
	}

	var amount float64
	var memberID int64
	var reqStatus string
	row := h.DB.Table("withdraw_requests").Select("amount, member_id, status").Where("id = ?", id).Row()
	if err := row.Scan(&amount, &memberID, &reqStatus); err != nil {
		fail(c, 404, "ไม่พบคำขอ"); return
	}
	if reqStatus != "pending" {
		fail(c, 400, "คำขอนี้ไม่ใช่สถานะรอดำเนินการ (สถานะ: "+reqStatus+")"); return
	}

	now := time.Now()
	// ⚠️ ไม่หักเงินซ้ำ — แค่เปลี่ยนสถานะ + บันทึก mode + admin_id
	h.DB.Exec(
		"UPDATE withdraw_requests SET status = 'approved', approved_at = ?, transfer_mode = ?, approved_by = ? WHERE id = ?",
		now, req.Mode, adminID, id,
	)

	ok(c, gin.H{"id": id, "status": "approved", "mode": req.Mode, "amount": amount, "member_id": memberID})
}

// RejectWithdraw ปฏิเสธคำขอถอนเงิน
// PUT /api/v1/withdrawals/:id/reject
// Body: { "refund": true/false, "reason": "เหตุผล" }
//
// ⚠️ refund=true (default) → คืนเงินให้สมาชิก (เพราะหักไปแล้วตอนสร้างคำขอ)
// ⚠️ refund=false → ไม่คืนเงิน (กรณีสมาชิกทุจริต)
func (h *Handler) RejectWithdraw(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	adminID := middleware.GetAdminID(c)

	var req struct {
		Refund *bool  `json:"refund"` // default true — คืนเงิน
		Reason string `json:"reason"`
	}
	c.ShouldBindJSON(&req)
	if req.Reason == "" {
		req.Reason = "ปฏิเสธโดยแอดมิน"
	}
	// default: คืนเงิน
	shouldRefund := true
	if req.Refund != nil {
		shouldRefund = *req.Refund
	}

	var amount float64
	var memberID int64
	var reqStatus string
	row := h.DB.Table("withdraw_requests").Select("amount, member_id, status").Where("id = ?", id).Row()
	if err := row.Scan(&amount, &memberID, &reqStatus); err != nil {
		fail(c, 404, "ไม่พบคำขอ"); return
	}
	if reqStatus != "pending" {
		fail(c, 400, "คำขอนี้ไม่ใช่สถานะรอดำเนินการ (สถานะ: "+reqStatus+")"); return
	}

	tx := h.DB.Begin()
	now := time.Now()

	// อัพเดท status → rejected
	tx.Exec(
		"UPDATE withdraw_requests SET status = 'rejected', approved_at = ?, reject_reason = ?, approved_by = ? WHERE id = ?",
		now, req.Reason, adminID, id,
	)

	// คืนเงินให้สมาชิก (ถ้า refund=true)
	if shouldRefund {
		tx.Exec("UPDATE members SET balance = balance + ? WHERE id = ?", amount, memberID)

		// ดึง balance ล่าสุด
		var balanceAfter float64
		tx.Table("members").Select("balance").Where("id = ?", memberID).Row().Scan(&balanceAfter)

		// บันทึก transaction คืนเงิน
		tx.Exec(`INSERT INTO transactions (member_id, type, amount, balance_before, balance_after, reference_type, note, created_at)
			VALUES (?, 'refund', ?, ?, ?, 'withdraw_reject', ?, ?)`,
			memberID, amount, balanceAfter-amount, balanceAfter,
			"คืนเงินถอน #"+strconv.FormatInt(id, 10)+": "+req.Reason, now)
	}

	tx.Commit()
	ok(c, gin.H{"id": id, "status": "rejected", "refund": shouldRefund, "reason": req.Reason, "amount": amount})
}

// =============================================================================
// ⭐ Auto-Ban Rules — กฎอั้นเลขอัตโนมัติ
// =============================================================================

// ListAutoBanRules ดูกฎอั้นทั้งหมด (filter by lottery_type_id)
// GET /api/v1/auto-ban-rules?lottery_type_id=1
func (h *Handler) ListAutoBanRules(c *gin.Context) {
	query := h.DB.Model(&model.AutoBanRule{}).Where("status = ?", "active")
	if lt := c.Query("lottery_type_id"); lt != "" {
		query = query.Where("lottery_type_id = ?", lt)
	}
	var rules []model.AutoBanRule
	query.Preload("LotteryType").Order("lottery_type_id, bet_type").Find(&rules)
	ok(c, rules)
}

// CreateAutoBanRule สร้างกฎอั้น 1 กฎ
// POST /api/v1/auto-ban-rules
func (h *Handler) CreateAutoBanRule(c *gin.Context) {
	var rule model.AutoBanRule
	if err := c.ShouldBindJSON(&rule); err != nil {
		fail(c, 400, err.Error())
		return
	}
	rule.Status = "active"
	rule.CreatedAt = time.Now()
	rule.UpdatedAt = time.Now()
	if rule.AgentID == 0 {
		rule.AgentID = 1
	}
	if err := h.DB.Create(&rule).Error; err != nil {
		fail(c, 500, "failed to create auto-ban rule")
		return
	}
	ok(c, rule)
}

// BulkCreateAutoBanRules สร้างกฎอั้นหลายกฎพร้อมกัน (จากคำนวณอัตโนมัติ)
// POST /api/v1/auto-ban-rules/bulk
// Body: { "rules": [...], "lottery_type_id": 1, "capital": 100000, "max_loss": 20000 }
func (h *Handler) BulkCreateAutoBanRules(c *gin.Context) {
	var req struct {
		LotteryTypeID int64 `json:"lottery_type_id" binding:"required"`
		Capital       float64 `json:"capital"`
		MaxLoss       float64 `json:"max_loss"`
		Rules         []struct {
			BetType         string  `json:"bet_type"`
			ThresholdAmount float64 `json:"threshold_amount"`
			Action          string  `json:"action"`
			Rate            float64 `json:"rate"`
			ReducedRate     float64 `json:"reduced_rate"`
		} `json:"rules" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// ลบกฎเก่าของ lottery type นี้ก่อน (soft delete)
	h.DB.Model(&model.AutoBanRule{}).
		Where("lottery_type_id = ? AND status = ?", req.LotteryTypeID, "active").
		Update("status", "inactive")

	// สร้างกฎใหม่ทั้งหมด
	now := time.Now()
	created := make([]model.AutoBanRule, 0, len(req.Rules))
	for _, r := range req.Rules {
		action := r.Action
		if action == "" {
			action = "full_ban"
		}
		rule := model.AutoBanRule{
			AgentID:         1,
			LotteryTypeID:   req.LotteryTypeID,
			BetType:         r.BetType,
			ThresholdAmount: r.ThresholdAmount,
			Action:          action,
			ReducedRate:     r.ReducedRate,
			Capital:         req.Capital,
			MaxLoss:         req.MaxLoss,
			Rate:            r.Rate,
			Status:          "active",
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		h.DB.Create(&rule)
		created = append(created, rule)
	}

	ok(c, gin.H{
		"created_count":   len(created),
		"lottery_type_id": req.LotteryTypeID,
		"rules":           created,
	})
}

// UpdateAutoBanRule แก้ไขกฎอั้น
// PUT /api/v1/auto-ban-rules/:id
func (h *Handler) UpdateAutoBanRule(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var rule model.AutoBanRule
	if err := h.DB.First(&rule, id).Error; err != nil {
		fail(c, 404, "rule not found")
		return
	}
	var req struct {
		ThresholdAmount *float64 `json:"threshold_amount"`
		Action          *string  `json:"action"`
		ReducedRate     *float64 `json:"reduced_rate"`
		BetType         *string  `json:"bet_type"`
		Status          *string  `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	updates := map[string]interface{}{"updated_at": time.Now()}
	if req.ThresholdAmount != nil { updates["threshold_amount"] = *req.ThresholdAmount }
	if req.Action != nil { updates["action"] = *req.Action }
	if req.ReducedRate != nil { updates["reduced_rate"] = *req.ReducedRate }
	if req.BetType != nil { updates["bet_type"] = *req.BetType }
	if req.Status != nil { updates["status"] = *req.Status }
	h.DB.Model(&rule).Updates(updates)
	h.DB.First(&rule, id)
	ok(c, rule)
}

// DeleteAutoBanRule ลบกฎอั้น (soft delete)
// DELETE /api/v1/auto-ban-rules/:id
func (h *Handler) DeleteAutoBanRule(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	h.DB.Model(&model.AutoBanRule{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":     "inactive",
		"updated_at": time.Now(),
	})
	ok(c, gin.H{"id": id, "status": "inactive"})
}

// =============================================================================
// ⭐ Yeekee Monitoring — ดูรอบ + สถิติยี่กี real-time
// =============================================================================

// ListYeekeeRounds แสดงรายการรอบยี่กี (paginated + filter)
// GET /api/v1/yeekee/rounds?status=shooting&date=2026-04-02&page=1&per_page=20
func (h *Handler) ListYeekeeRounds(c *gin.Context) {
	page, perPage := pageParams(c)
	var rounds []model.YeekeeRound
	var total int64

	query := h.DB.Model(&model.YeekeeRound{})

	// Filter by status
	if s := c.Query("status"); s != "" {
		query = query.Where("status = ?", s)
	}

	// Filter by date (ใช้ start_time ของรอบ)
	if d := c.Query("date"); d != "" {
		query = query.Where("DATE(start_time) = ?", d)
	} else {
		// Default: วันนี้
		today := time.Now().Format("2006-01-02")
		query = query.Where("DATE(start_time) = ?", today)
	}

	query.Count(&total)
	query.
		Preload("LotteryRound").
		Order("start_time ASC").
		Offset((page - 1) * perPage).
		Limit(perPage).
		Find(&rounds)

	paginated(c, rounds, total, page, perPage)
}

// GetYeekeeRoundDetail ดูรอบยี่กีรอบเดียว + shoots + bet summary
// GET /api/v1/yeekee/rounds/:id
func (h *Handler) GetYeekeeRoundDetail(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	var round model.YeekeeRound
	if err := h.DB.Preload("LotteryRound").First(&round, id).Error; err != nil {
		fail(c, 404, "yeekee round not found")
		return
	}

	// ดึง shoots (ไม่ paginate — แสดงทั้งหมดเพราะแต่ละรอบไม่มาก)
	var shoots []model.YeekeeShoot
	h.DB.Where("yeekee_round_id = ?", id).Preload("Member").Order("shot_at ASC").Find(&shoots)

	// Bet summary — จำนวน bets + ยอดแทง + ยอดจ่าย ของรอบนี้
	var betSummary struct {
		TotalBets   int64   `json:"total_bets"`
		TotalAmount float64 `json:"total_amount"`
		TotalPayout float64 `json:"total_payout"`
		WinnerCount int64   `json:"winner_count"`
	}
	h.DB.Model(&model.Bet{}).
		Where("lottery_round_id = ?", round.LotteryRoundID).
		Select("COUNT(*) as total_bets, COALESCE(SUM(amount), 0) as total_amount, COALESCE(SUM(win_amount), 0) as total_payout").
		Scan(&betSummary)
	h.DB.Model(&model.Bet{}).
		Where("lottery_round_id = ? AND status = ?", round.LotteryRoundID, "won").
		Count(&betSummary.WinnerCount)

	// ⭐ ดึง bets ทั้งหมดของรอบนี้ (พร้อม member + bet_type)
	var bets []model.Bet
	h.DB.Where("lottery_round_id = ?", round.LotteryRoundID).
		Preload("Member").Preload("BetType").
		Order("created_at DESC").
		Find(&bets)

	ok(c, gin.H{
		"round":       round,
		"shoots":      shoots,
		"bets":        bets,
		"bet_summary": betSummary,
	})
}

// ListYeekeeShoots ดูเลขยิงในรอบ (paginated)
// GET /api/v1/yeekee/rounds/:id/shoots?page=1&per_page=50
func (h *Handler) ListYeekeeShoots(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	page, perPage := pageParams(c)

	var shoots []model.YeekeeShoot
	var total int64

	query := h.DB.Model(&model.YeekeeShoot{}).Where("yeekee_round_id = ?", id)
	query.Count(&total)
	query.
		Preload("Member").
		Order("shot_at ASC").
		Offset((page - 1) * perPage).
		Limit(perPage).
		Find(&shoots)

	paginated(c, shoots, total, page, perPage)
}

// GetYeekeeStats สถิติยี่กีวันนี้
// GET /api/v1/yeekee/stats
func (h *Handler) GetYeekeeStats(c *gin.Context) {
	today := time.Now().Format("2006-01-02")

	var stats struct {
		TotalRounds    int64   `json:"total_rounds"`
		WaitingCount   int64   `json:"waiting_count"`
		ShootingCount  int64   `json:"shooting_count"`
		ResultedCount  int64   `json:"resulted_count"`
		TotalShoots    int64   `json:"total_shoots"`
		TotalBets      int64   `json:"total_bets"`
		TotalBetAmount float64 `json:"total_bet_amount"`
		TotalPayout    float64 `json:"total_payout"`
		Profit         float64 `json:"profit"`
	}

	// นับรอบตาม status
	h.DB.Model(&model.YeekeeRound{}).Where("DATE(start_time) = ?", today).Count(&stats.TotalRounds)
	h.DB.Model(&model.YeekeeRound{}).Where("DATE(start_time) = ? AND status = ?", today, "waiting").Count(&stats.WaitingCount)
	h.DB.Model(&model.YeekeeRound{}).Where("DATE(start_time) = ? AND status = ?", today, "shooting").Count(&stats.ShootingCount)
	h.DB.Model(&model.YeekeeRound{}).Where("DATE(start_time) = ? AND status = ?", today, "resulted").Count(&stats.ResultedCount)

	// นับ shoots วันนี้
	h.DB.Model(&model.YeekeeShoot{}).
		Joins("JOIN yeekee_rounds ON yeekee_shoots.yeekee_round_id = yeekee_rounds.id").
		Where("DATE(yeekee_rounds.start_time) = ?", today).
		Count(&stats.TotalShoots)

	// สถิติ bets — ดึงเฉพาะ bets ของรอบยี่กีวันนี้
	var yeekeeRoundIDs []int64
	h.DB.Model(&model.YeekeeRound{}).Where("DATE(start_time) = ?", today).Pluck("lottery_round_id", &yeekeeRoundIDs)

	if len(yeekeeRoundIDs) > 0 {
		h.DB.Model(&model.Bet{}).Where("lottery_round_id IN ?", yeekeeRoundIDs).Count(&stats.TotalBets)
		h.DB.Model(&model.Bet{}).Where("lottery_round_id IN ?", yeekeeRoundIDs).
			Select("COALESCE(SUM(amount), 0)").Scan(&stats.TotalBetAmount)
		h.DB.Model(&model.Bet{}).Where("lottery_round_id IN ? AND status = ?", yeekeeRoundIDs, "won").
			Select("COALESCE(SUM(win_amount), 0)").Scan(&stats.TotalPayout)
		stats.Profit = stats.TotalBetAmount - stats.TotalPayout
	}

	ok(c, stats)
}

// =============================================================================
// Staff (Admin Users) — CRUD + permissions
// =============================================================================

// ListStaff รายการ admin ทั้งหมด
func (h *Handler) ListStaff(c *gin.Context) {
	var admins []model.Admin
	h.DB.Where("status != ?", "deleted").Order("created_at DESC").Find(&admins)
	ok(c, admins)
}

// CreateStaff เพิ่ม admin ใหม่
func (h *Handler) CreateStaff(c *gin.Context) {
	var req struct {
		Username    string `json:"username" binding:"required,min=3,max=50"`
		Password    string `json:"password" binding:"required,min=6,max=100"`
		Name        string `json:"name" binding:"required,max=100"`
		Role        string `json:"role"`
		Permissions string `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&req); err != nil { fail(c, 400, err.Error()); return }
	if req.Role == "" { req.Role = "admin" }

	var count int64
	h.DB.Model(&model.Admin{}).Where("username = ?", req.Username).Count(&count)
	if count > 0 { fail(c, 400, "username นี้ถูกใช้แล้ว"); return }

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil { fail(c, 500, "failed to hash password"); return }

	admin := model.Admin{
		Username: req.Username, PasswordHash: string(hash),
		Name: req.Name, Role: req.Role, Permissions: req.Permissions, Status: "active",
	}
	if err := h.DB.Create(&admin).Error; err != nil { fail(c, 500, "failed to create admin"); return }
	ok(c, admin)
}

// UpdateStaff แก้ไข admin
func (h *Handler) UpdateStaff(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var admin model.Admin
	if err := h.DB.First(&admin, id).Error; err != nil { fail(c, 404, "admin not found"); return }

	var req struct {
		Name        string `json:"name"`
		Role        string `json:"role"`
		Permissions string `json:"permissions"`
		Password    string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil { fail(c, 400, err.Error()); return }

	if req.Name != "" { admin.Name = req.Name }
	if req.Role != "" { admin.Role = req.Role }
	if req.Permissions != "" { admin.Permissions = req.Permissions }
	if req.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil { fail(c, 500, "failed to hash password"); return }
		admin.PasswordHash = string(hash)
	}
	h.DB.Save(&admin)
	ok(c, admin)
}

// UpdateStaffStatus เปลี่ยนสถานะ admin
func (h *Handler) UpdateStaffStatus(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct { Status string `json:"status" binding:"required"` }
	if err := c.ShouldBindJSON(&req); err != nil { fail(c, 400, err.Error()); return }
	h.DB.Model(&model.Admin{}).Where("id = ?", id).Update("status", req.Status)
	ok(c, gin.H{"id": id, "status": req.Status})
}

// DeleteStaff ลบ admin (soft delete)
func (h *Handler) DeleteStaff(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	adminID := middleware.GetAdminID(c)
	if id == adminID { fail(c, 400, "ไม่สามารถลบตัวเองได้"); return }
	h.DB.Model(&model.Admin{}).Where("id = ?", id).Update("status", "deleted")
	ok(c, gin.H{"id": id, "status": "deleted"})
}
