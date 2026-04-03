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
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/farritpcz/lotto-standalone-admin-api/internal/job"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
	rkautoLib "github.com/farritpcz/lotto-standalone-admin-api/internal/rkauto"
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

	// อัพเดท last login + IP
	now := time.Now()
	ip := c.ClientIP()
	h.DB.Model(&admin).Updates(map[string]interface{}{"last_login_at": &now, "last_login_ip": ip})

	// บันทึก login history
	h.DB.Create(&model.AdminLoginHistory{
		AdminID:   admin.ID,
		IP:        ip,
		UserAgent: c.GetHeader("User-Agent"),
		Success:   true,
		CreatedAt: now,
	})

	// สร้าง JWT token จริง
	token, err := middleware.GenerateAdminToken(admin.ID, admin.Username, admin.Role, h.AdminJWTSecret, h.AdminJWTExpiryHours)
	if err != nil {
		fail(c, 500, "failed to generate token")
		return
	}

	// ⭐ ตั้ง httpOnly cookie สำหรับ admin JWT token + CSRF cookie
	middleware.SetAdminTokenCookie(c, token, h.AdminJWTExpiryHours*3600, h.cookieConfig())
	middleware.SetCSRFCookie(c, h.cookieConfig())

	ok(c, gin.H{"admin": admin, "token": token, "permissions": admin.Permissions})
}

// AdminLogout ออกจากระบบ — ลบ httpOnly cookie
//
// POST /api/v1/auth/logout
func (h *Handler) AdminLogout(c *gin.Context) {
	middleware.ClearAdminTokenCookie(c, h.cookieConfig())
	middleware.ClearCSRFCookie(c, h.cookieConfig())
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "logged out"})
}

// cookieConfig ดึง CookieConfig จาก Handler fields
func (h *Handler) cookieConfig() middleware.CookieConfig {
	return middleware.CookieConfig{
		Domain: h.CookieDomain,
		Secure: h.CookieSecure,
	}
}

// =============================================================================
// Dashboard
// =============================================================================

func (h *Handler) GetDashboard(c *gin.Context) {
	// ⚠️ Redis cache 60 วินาที — ลด DB load สำหรับ dashboard ที่เรียกบ่อย
	ctx := c.Request.Context()
	cacheKey := "admin:dashboard:" + time.Now().Format("2006-01-02")

	if h.Redis != nil {
		cached, err := h.Redis.Get(ctx, cacheKey).Result()
		if err == nil {
			// ส่ง cached JSON กลับตรงๆ
			c.Data(200, "application/json", []byte(cached))
			return
		}
	}

	var stats struct {
		TotalMembers  int64   `json:"total_members"`
		ActiveMembers int64   `json:"active_members"`
		TotalBets     int64   `json:"total_bets_today"`
		TotalAmount   float64 `json:"total_amount_today"`
		TotalWin      float64 `json:"total_win_today"`
		OpenRounds    int64   `json:"open_rounds"`
	}

	// ⚠️ ใช้ range query แทน DATE(created_at) — ให้ MySQL ใช้ index ได้
	todayStart := time.Now().Truncate(24 * time.Hour)
	todayEnd := todayStart.Add(24 * time.Hour)

	h.DB.Model(&model.Member{}).Count(&stats.TotalMembers)
	h.DB.Model(&model.Member{}).Where("status = ?", "active").Count(&stats.ActiveMembers)
	h.DB.Model(&model.Bet{}).Where("created_at >= ? AND created_at < ?", todayStart, todayEnd).Count(&stats.TotalBets)
	h.DB.Model(&model.Bet{}).Where("created_at >= ? AND created_at < ?", todayStart, todayEnd).
		Select("COALESCE(SUM(amount), 0)").Scan(&stats.TotalAmount)
	h.DB.Model(&model.Bet{}).Where("created_at >= ? AND created_at < ? AND status = ?", todayStart, todayEnd, "won").
		Select("COALESCE(SUM(win_amount), 0)").Scan(&stats.TotalWin)
	h.DB.Model(&model.LotteryRound{}).Where("status = ?", "open").Count(&stats.OpenRounds)

	// Cache ใน Redis 60 วินาที
	if h.Redis != nil {
		jsonBytes, _ := json.Marshal(gin.H{"success": true, "data": stats})
		h.Redis.Set(ctx, cacheKey, string(jsonBytes), 60*time.Second)
	}

	ok(c, stats)
}

// GetDashboardV2 — dashboard ใหม่ครบทุก section (แบบเจริญดี88)
// GET /api/v1/dashboard/v2?from=2026-04-01&to=2026-04-02
//
// Presets ที่ frontend ส่งมา:
//   - วันนี้: from=today, to=today
//   - เมื่อวาน: from=yesterday, to=yesterday
//   - อาทิตย์นี้: from=2026-03-31, to=2026-04-06
//   - เดือนนี้: from=2026-04-01, to=2026-04-30
//   - ต้นเดือน: from=2026-04-01, to=2026-04-15
//   - ท้ายเดือน: from=2026-04-16, to=2026-04-30
func (h *Handler) GetDashboardV2(c *gin.Context) {
	// ─── parse from/to params ───
	now := time.Now()
	todayStart := now.Truncate(24 * time.Hour)
	todayEnd := todayStart.Add(24 * time.Hour)

	// default = เดือนนี้
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	rangeFrom := monthStart
	rangeTo := monthStart.AddDate(0, 1, 0)

	if fromStr := c.Query("from"); fromStr != "" {
		if parsed, err := time.Parse("2006-01-02", fromStr); err == nil {
			rangeFrom = parsed
		}
	}
	if toStr := c.Query("to"); toStr != "" {
		if parsed, err := time.Parse("2006-01-02", toStr); err == nil {
			rangeTo = parsed.Add(24 * time.Hour) // inclusive end date
		}
	}

	// สำหรับ % เปรียบเทียบ: ช่วงเวลาเดียวกันก่อนหน้า
	rangeDuration := rangeTo.Sub(rangeFrom)
	prevFrom := rangeFrom.Add(-rangeDuration)
	prevTo := rangeFrom

	// ─── Redis cache 30s (สั้นลงเพราะ filter เปลี่ยนบ่อย) ───
	ctx := c.Request.Context()
	cacheKey := "admin:dashboardV2:" + rangeFrom.Format("20060102") + ":" + rangeTo.Format("20060102")
	if h.Redis != nil {
		if cached, err := h.Redis.Get(ctx, cacheKey).Result(); err == nil {
			c.Data(200, "application/json", []byte(cached))
			return
		}
	}

	// ═══ 1. Summary: เดือนนี้ vs เดือนก่อน ═══
	type MonthSummary struct {
		DepositsThisMonth    float64 `json:"deposits_this_month"`
		DepositsLastMonth    float64 `json:"deposits_last_month"`
		WithdrawalsThisMonth float64 `json:"withdrawals_this_month"`
		WithdrawalsLastMonth float64 `json:"withdrawals_last_month"`
		ProfitThisMonth      float64 `json:"profit_this_month"`
		ProfitLastMonth      float64 `json:"profit_last_month"`
		NewMembersThisMonth  int64   `json:"new_members_this_month"`
		NewMembersLastMonth  int64   `json:"new_members_last_month"`
	}
	var summary MonthSummary

	// ฝาก
	h.DB.Table("transactions").Where("type IN ('deposit','admin_credit') AND created_at >= ? AND created_at < ?", rangeFrom, rangeTo).
		Select("COALESCE(SUM(ABS(amount)),0)").Scan(&summary.DepositsThisMonth)
	h.DB.Table("transactions").Where("type IN ('deposit','admin_credit') AND created_at >= ? AND created_at < ?", prevFrom, prevTo).
		Select("COALESCE(SUM(ABS(amount)),0)").Scan(&summary.DepositsLastMonth)
	// ถอน
	h.DB.Table("transactions").Where("type IN ('withdraw','admin_debit') AND created_at >= ? AND created_at < ?", rangeFrom, rangeTo).
		Select("COALESCE(SUM(ABS(amount)),0)").Scan(&summary.WithdrawalsThisMonth)
	h.DB.Table("transactions").Where("type IN ('withdraw','admin_debit') AND created_at >= ? AND created_at < ?", prevFrom, prevTo).
		Select("COALESCE(SUM(ABS(amount)),0)").Scan(&summary.WithdrawalsLastMonth)
	// กำไร = ยอดแทง - ยอดจ่าย
	h.DB.Table("bets").Where("created_at >= ? AND created_at < ?", rangeFrom, rangeTo).
		Select("COALESCE(SUM(amount),0) - COALESCE(SUM(CASE WHEN status='won' THEN win_amount ELSE 0 END),0)").Scan(&summary.ProfitThisMonth)
	h.DB.Table("bets").Where("created_at >= ? AND created_at < ?", prevFrom, prevTo).
		Select("COALESCE(SUM(amount),0) - COALESCE(SUM(CASE WHEN status='won' THEN win_amount ELSE 0 END),0)").Scan(&summary.ProfitLastMonth)
	// สมาชิกใหม่
	h.DB.Model(&model.Member{}).Where("created_at >= ? AND created_at < ?", rangeFrom, rangeTo).Count(&summary.NewMembersThisMonth)
	h.DB.Model(&model.Member{}).Where("created_at >= ? AND created_at < ?", prevFrom, prevTo).Count(&summary.NewMembersLastMonth)

	// ═══ 2. Chart: ฝาก/ถอน 30 วันย้อนหลัง ═══
	type ChartPoint struct {
		Date        string  `json:"date"`
		Deposits    float64 `json:"deposits"`
		Withdrawals float64 `json:"withdrawals"`
	}
	var chartData []ChartPoint
	thirtyDaysAgo := todayStart.AddDate(0, 0, -30)
	h.DB.Raw(`
		SELECT DATE(created_at) as date,
			COALESCE(SUM(CASE WHEN type IN ('deposit','admin_credit') THEN ABS(amount) ELSE 0 END),0) as deposits,
			COALESCE(SUM(CASE WHEN type IN ('withdraw','admin_debit') THEN ABS(amount) ELSE 0 END),0) as withdrawals
		FROM transactions WHERE created_at >= ?
		GROUP BY DATE(created_at) ORDER BY date
	`, thirtyDaysAgo).Scan(&chartData)

	// ═══ 3. Top 10 สมาชิกแทงเยอะสุด ═══
	type TopBettor struct {
		MemberID int64   `json:"member_id"`
		Username string  `json:"username"`
		TotalBet float64 `json:"total_bet"`
		TotalWin float64 `json:"total_win"`
		Profit   float64 `json:"profit"`
	}
	var topBettors []TopBettor
	h.DB.Raw(`
		SELECT b.member_id, m.username, SUM(b.amount) as total_bet,
			COALESCE(SUM(CASE WHEN b.status='won' THEN b.win_amount ELSE 0 END),0) as total_win,
			SUM(b.amount) - COALESCE(SUM(CASE WHEN b.status='won' THEN b.win_amount ELSE 0 END),0) as profit
		FROM bets b LEFT JOIN members m ON m.id = b.member_id
		WHERE b.created_at >= ? AND b.created_at < ?
		GROUP BY b.member_id, m.username ORDER BY total_bet DESC LIMIT 10
	`, rangeFrom, rangeTo).Scan(&topBettors)

	// ═══ 4. Top 10 สมาชิกยอดฝาก/ถอนสูงสุด ═══
	type TopDepositor struct {
		MemberID     int64   `json:"member_id"`
		Username     string  `json:"username"`
		TotalDeposit float64 `json:"total_deposit"`
		TotalWithdraw float64 `json:"total_withdraw"`
	}
	var topDepositors []TopDepositor
	h.DB.Raw(`
		SELECT t.member_id, m.username,
			COALESCE(SUM(CASE WHEN t.type IN ('deposit','admin_credit') THEN ABS(t.amount) ELSE 0 END),0) as total_deposit,
			COALESCE(SUM(CASE WHEN t.type IN ('withdraw','admin_debit') THEN ABS(t.amount) ELSE 0 END),0) as total_withdraw
		FROM transactions t LEFT JOIN members m ON m.id = t.member_id
		WHERE t.created_at >= ? AND t.created_at < ?
		GROUP BY t.member_id, m.username ORDER BY total_deposit DESC LIMIT 10
	`, rangeFrom, rangeTo).Scan(&topDepositors)

	// ═══ 5. ธุรกรรมล่าสุด (5 ฝาก + 5 ถอน) ═══
	type RecentTx struct {
		ID        int64   `json:"id"`
		MemberID  int64   `json:"member_id"`
		Username  string  `json:"username"`
		Amount    float64 `json:"amount"`
		Status    string  `json:"status"`
		BankCode  string  `json:"bank_code"`
		CreatedAt string  `json:"created_at"`
	}
	var recentDeposits, recentWithdrawals []RecentTx
	h.DB.Raw(`SELECT d.id, d.member_id, m.username, d.amount, d.status, '' as bank_code, d.created_at
		FROM deposit_requests d LEFT JOIN members m ON m.id = d.member_id
		ORDER BY d.created_at DESC LIMIT 5`).Scan(&recentDeposits)
	h.DB.Raw(`SELECT w.id, w.member_id, m.username, w.amount, w.status, w.bank_code, w.created_at
		FROM withdraw_requests w LEFT JOIN members m ON m.id = w.member_id
		ORDER BY w.created_at DESC LIMIT 5`).Scan(&recentWithdrawals)

	// ═══ 6. ติดตามสมาชิกรายวัน ═══
	type MemberTracking struct {
		DirectSignups   int64 `json:"direct_signups"`
		ReferralSignups int64 `json:"referral_signups"`
		DepositedToday  int64 `json:"deposited_today"`
	}
	var tracking MemberTracking
	h.DB.Model(&model.Member{}).Where("created_at >= ? AND created_at < ? AND referred_by IS NULL", todayStart, todayEnd).Count(&tracking.DirectSignups)
	h.DB.Model(&model.Member{}).Where("created_at >= ? AND created_at < ? AND referred_by IS NOT NULL", todayStart, todayEnd).Count(&tracking.ReferralSignups)
	h.DB.Raw("SELECT COUNT(DISTINCT member_id) FROM deposit_requests WHERE status='approved' AND created_at >= ? AND created_at < ?", todayStart, todayEnd).Scan(&tracking.DepositedToday)

	// ═══ 7. Credit Stats ═══
	type CreditStats struct {
		CreditAdded      float64 `json:"credit_added"`
		CreditDeducted   float64 `json:"credit_deducted"`
		DepositCount     int64   `json:"deposit_count"`
		CommissionTotal  float64 `json:"commission_total"`
		CancelledDeposit float64 `json:"cancelled_deposits"`
		CancelledWithdraw float64 `json:"cancelled_withdrawals"`
	}
	var credits CreditStats
	h.DB.Table("transactions").Where("type='admin_credit' AND created_at >= ? AND created_at < ?", rangeFrom, rangeTo).
		Select("COALESCE(SUM(ABS(amount)),0)").Scan(&credits.CreditAdded)
	h.DB.Table("transactions").Where("type='admin_debit' AND created_at >= ? AND created_at < ?", rangeFrom, rangeTo).
		Select("COALESCE(SUM(ABS(amount)),0)").Scan(&credits.CreditDeducted)
	h.DB.Table("deposit_requests").Where("status='approved' AND created_at >= ? AND created_at < ?", rangeFrom, rangeTo).Count(&credits.DepositCount)
	h.DB.Table("referral_commissions").Where("created_at >= ? AND created_at < ?", rangeFrom, rangeTo).
		Select("COALESCE(SUM(amount),0)").Scan(&credits.CommissionTotal)
	h.DB.Table("deposit_requests").Where("status='cancelled' AND created_at >= ? AND created_at < ?", rangeFrom, rangeTo).
		Select("COALESCE(SUM(amount),0)").Scan(&credits.CancelledDeposit)
	h.DB.Table("withdraw_requests").Where("status='rejected' AND created_at >= ? AND created_at < ?", rangeFrom, rangeTo).
		Select("COALESCE(SUM(amount),0)").Scan(&credits.CancelledWithdraw)

	// ═══ 8. บัญชีธนาคาร ═══
	type BankAccount struct {
		ID         int64  `json:"id"`
		BankCode   string `json:"bank_code"`
		BankName   string `json:"bank_name"`
		AccountNo  string `json:"account_number"`
		AccountName string `json:"account_name"`
		Balance    float64 `json:"balance"`
	}
	var bankAccounts []BankAccount
	h.DB.Table("agent_bank_accounts").Scan(&bankAccounts)

	// ═══ Build response ═══
	resp := gin.H{
		"summary":              summary,
		"chart_data":           chartData,
		"top_bettors":          topBettors,
		"top_depositors":       topDepositors,
		"recent_deposits":      recentDeposits,
		"recent_withdrawals":   recentWithdrawals,
		"member_tracking":      tracking,
		"credit_stats":         credits,
		"bank_accounts":        bankAccounts,
	}

	// Cache 60s
	if h.Redis != nil {
		jsonBytes, _ := json.Marshal(gin.H{"success": true, "data": resp})
		h.Redis.Set(ctx, cacheKey, string(jsonBytes), 60*time.Second)
	}

	ok(c, resp)
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

// UpdateLotteryImage อัพเดทรูปประเภทหวย
// PUT /api/v1/lotteries/:id/image
// Body: { "image_url": "https://..." }
func (h *Handler) UpdateLotteryImage(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		ImageURL string `json:"image_url" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error()); return
	}
	h.DB.Model(&model.LotteryType{}).Where("id = ?", id).Update("image_url", req.ImageURL)
	ok(c, gin.H{"id": id, "image_url": req.ImageURL})
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

// =============================================================================
// Agent Theme — ตั้งค่าสีธีม per-agent
//
// GET  /api/v1/agent/theme    → ดึงสีปัจจุบัน
// PUT  /api/v1/agent/theme    → อัพเดทสี + bump theme_version (เคลีย cache หน้าบ้าน)
// =============================================================================

func (h *Handler) GetAgentTheme(c *gin.Context) {
	type ThemeRow struct {
		ThemePrimaryColor   string `json:"theme_primary_color"`
		ThemeSecondaryColor string `json:"theme_secondary_color"`
		ThemeBGColor        string `json:"theme_bg_color"`
		ThemeAccentColor    string `json:"theme_accent_color"`
		ThemeCardGradient1  string `json:"theme_card_gradient1"`
		ThemeCardGradient2  string `json:"theme_card_gradient2"`
		ThemeNavBG          string `json:"theme_nav_bg"`
		ThemeHeaderBG       string `json:"theme_header_bg"`
		ThemeVersion        int    `json:"theme_version"`
	}
	var theme ThemeRow
	// ⭐ agent_id=1 สำหรับ standalone (multi-agent ใช้ jwt claims)
	if err := h.DB.Table("agents").Where("id = 1").First(&theme).Error; err != nil {
		fail(c, 404, "agent not found"); return
	}
	ok(c, theme)
}

func (h *Handler) UpdateAgentTheme(c *gin.Context) {
	var req struct {
		PrimaryColor   *string `json:"theme_primary_color"`
		SecondaryColor *string `json:"theme_secondary_color"`
		BGColor        *string `json:"theme_bg_color"`
		AccentColor    *string `json:"theme_accent_color"`
		CardGradient1  *string `json:"theme_card_gradient1"`
		CardGradient2  *string `json:"theme_card_gradient2"`
		NavBG          *string `json:"theme_nav_bg"`
		HeaderBG       *string `json:"theme_header_bg"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error()); return
	}

	updates := map[string]interface{}{}
	if req.PrimaryColor != nil   { updates["theme_primary_color"] = *req.PrimaryColor }
	if req.SecondaryColor != nil { updates["theme_secondary_color"] = *req.SecondaryColor }
	if req.BGColor != nil        { updates["theme_bg_color"] = *req.BGColor }
	if req.AccentColor != nil    { updates["theme_accent_color"] = *req.AccentColor }
	if req.CardGradient1 != nil  { updates["theme_card_gradient1"] = *req.CardGradient1 }
	if req.CardGradient2 != nil  { updates["theme_card_gradient2"] = *req.CardGradient2 }
	if req.NavBG != nil          { updates["theme_nav_bg"] = *req.NavBG }
	if req.HeaderBG != nil       { updates["theme_header_bg"] = *req.HeaderBG }

	if len(updates) == 0 {
		fail(c, 400, "no fields to update"); return
	}

	// ⭐ Bump theme_version → หน้าบ้านเห็น version ไม่ตรง → refetch สีใหม่
	if err := h.DB.Table("agents").Where("id = 1").
		Updates(updates).
		Update("theme_version", gorm.Expr("theme_version + 1")).Error; err != nil {
		fail(c, 500, err.Error()); return
	}

	ok(c, gin.H{"message": "theme updated", "fields_updated": len(updates)})
}

func (h *Handler) GetSettings(c *gin.Context) {
	var settings []model.Setting
	h.DB.Find(&settings)
	ok(c, settings)
}

func (h *Handler) UpdateSettings(c *gin.Context) {
	var req map[string]string
	if err := c.ShouldBindJSON(&req); err != nil { fail(c, 400, err.Error()); return }
	for key, value := range req {
		// ⭐ Upsert: ถ้ามี key → update, ถ้ายังไม่มี → insert
		var existing model.Setting
		if err := h.DB.Where("`key` = ?", key).First(&existing).Error; err != nil {
			// ไม่มี → สร้างใหม่
			h.DB.Create(&model.Setting{Key: key, Value: value})
		} else {
			// มีอยู่แล้ว → update value
			h.DB.Model(&existing).Update("value", value)
		}
	}
	ok(c, gin.H{"updated": len(req)})
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
	h.DB.Preload("LotteryType").Where("status = ?", "active").Order("lottery_type_id ASC").Find(&settings)
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

	// ⭐ Upsert: หาทุก status (รวม inactive) — ป้องกัน duplicate
	var existing model.AffiliateSettings
	query := h.DB.Where("agent_id = ?", agentID)
	if req.LotteryTypeID == nil {
		query = query.Where("lottery_type_id IS NULL")
	} else {
		query = query.Where("lottery_type_id = ?", *req.LotteryTypeID)
	}

	if err := query.First(&existing).Error; err != nil {
		// ไม่มีเลย → สร้างใหม่
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

	// มีอยู่แล้ว → อัพเดท (reactivate ถ้าเป็น inactive)
	updates := map[string]interface{}{
		"commission_rate": req.CommissionRate,
		"withdrawal_min":  req.WithdrawalMin,
		"withdrawal_note": req.WithdrawalNote,
		"status":          "active",
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
// Share Templates — ข้อความสำเร็จรูปสำหรับแชร์ลิงก์เชิญ (admin จัดการ)
// =============================================================================

// ListShareTemplates ดึง templates ทั้งหมดของ agent
func (h *Handler) ListShareTemplates(c *gin.Context) {
	agentID := int64(1)
	var templates []model.ShareTemplate
	h.DB.Where("agent_id = ?", agentID).Order("sort_order ASC, id ASC").Find(&templates)
	ok(c, templates)
}

// CreateShareTemplate สร้าง template ใหม่
// Body: { "name": "...", "content": "สมัครเลย! {link}", "platform": "all", "sort_order": 0 }
func (h *Handler) CreateShareTemplate(c *gin.Context) {
	var req struct {
		Name      string `json:"name" binding:"required"`
		Content   string `json:"content" binding:"required"`
		Platform  string `json:"platform"`
		SortOrder int    `json:"sort_order"`
	}
	if err := c.ShouldBindJSON(&req); err != nil { fail(c, 400, err.Error()); return }

	agentID := int64(1)
	if req.Platform == "" {
		req.Platform = "all"
	}

	tmpl := model.ShareTemplate{
		AgentID:   agentID,
		Name:      req.Name,
		Content:   req.Content,
		Platform:  req.Platform,
		SortOrder: req.SortOrder,
		Status:    "active",
	}
	if err := h.DB.Create(&tmpl).Error; err != nil { fail(c, 500, "สร้าง template ไม่สำเร็จ"); return }
	ok(c, tmpl)
}

// UpdateShareTemplate แก้ไข template
func (h *Handler) UpdateShareTemplate(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	var req struct {
		Name      *string `json:"name"`
		Content   *string `json:"content"`
		Platform  *string `json:"platform"`
		SortOrder *int    `json:"sort_order"`
		Status    *string `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil { fail(c, 400, err.Error()); return }

	var tmpl model.ShareTemplate
	if err := h.DB.First(&tmpl, id).Error; err != nil { fail(c, 404, "ไม่พบ template"); return }

	updates := make(map[string]interface{})
	if req.Name != nil { updates["name"] = *req.Name }
	if req.Content != nil { updates["content"] = *req.Content }
	if req.Platform != nil { updates["platform"] = *req.Platform }
	if req.SortOrder != nil { updates["sort_order"] = *req.SortOrder }
	if req.Status != nil { updates["status"] = *req.Status }

	h.DB.Model(&tmpl).Updates(updates)
	h.DB.First(&tmpl, id)
	ok(c, tmpl)
}

// DeleteShareTemplate ลบ template (soft delete → status=inactive)
func (h *Handler) DeleteShareTemplate(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	h.DB.Model(&model.ShareTemplate{}).Where("id = ?", id).Update("status", "inactive")
	ok(c, gin.H{"id": id, "status": "deleted"})
}

// =============================================================================
// Manual Commission Adjustment — admin ปรับค่าคอมด้วยมือ + audit log
// =============================================================================

// ListCommissionAdjustments ดูประวัติการปรับค่าคอม
// Query: ?member_id=11&page=1&per_page=20
func (h *Handler) ListCommissionAdjustments(c *gin.Context) {
	agentID := int64(1)
	page, perPage := pageParams(c)
	memberIDStr := c.Query("member_id")

	query := h.DB.Model(&model.CommissionAdjustment{}).
		Preload("Member").
		Where("agent_id = ?", agentID)

	if memberIDStr != "" {
		mID, _ := strconv.ParseInt(memberIDStr, 10, 64)
		query = query.Where("member_id = ?", mID)
	}

	var total int64
	query.Count(&total)

	var items []model.CommissionAdjustment
	query.Order("created_at DESC").Offset((page - 1) * perPage).Limit(perPage).Find(&items)

	totalPages := int(total) / perPage
	if int(total)%perPage > 0 { totalPages++ }

	c.JSON(200, gin.H{
		"success": true,
		"data": items,
		"meta": gin.H{"page": page, "per_page": perPage, "total": total, "total_pages": totalPages},
	})
}

// CreateCommissionAdjustment ปรับค่าคอม: เพิ่ม / ลด / ยกเลิก
//
// Body: { "member_id": 11, "type": "add|deduct|cancel", "amount": 100.00, "reason": "...", "commission_id": null }
//
// Logic:
//   - add: เพิ่มค่าคอม pending ให้สมาชิก (สร้าง referral_commission ใหม่)
//   - deduct: หักค่าคอม pending (ลดจาก wallet balance)
//   - cancel: ยกเลิก commission เฉพาะรายการ (เปลี่ยน status เป็น cancelled)
func (h *Handler) CreateCommissionAdjustment(c *gin.Context) {
	adminID := int64(1) // TODO: ดึงจาก JWT เมื่อมี admin auth
	agentID := int64(1)

	var req struct {
		MemberID     int64   `json:"member_id" binding:"required"`
		Type         string  `json:"type" binding:"required,oneof=add deduct cancel"`
		Amount       float64 `json:"amount" binding:"required,gt=0"`
		Reason       string  `json:"reason" binding:"required,min=3"`
		CommissionID *int64  `json:"commission_id"` // สำหรับ cancel เฉพาะรายการ
	}
	if err := c.ShouldBindJSON(&req); err != nil { fail(c, 400, err.Error()); return }

	// ดึงข้อมูลสมาชิก
	var member model.Member
	if err := h.DB.First(&member, req.MemberID).Error; err != nil {
		fail(c, 404, "ไม่พบสมาชิก"); return
	}

	balanceBefore := member.Balance
	balanceAfter := balanceBefore

	tx := h.DB.Begin()

	switch req.Type {
	case "add":
		// เพิ่มค่าคอม → สร้าง referral_commission ใหม่ (status=pending)
		comm := model.ReferralCommission{
			ReferrerID:       req.MemberID,
			ReferredID:       req.MemberID, // admin ปรับเอง ไม่มี referred จริง
			AgentID:          agentID,
			BetAmount:        0,
			CommissionRate:   0,
			CommissionAmount: req.Amount,
			Status:           "pending",
		}
		if err := tx.Create(&comm).Error; err != nil {
			tx.Rollback(); fail(c, 500, "สร้างค่าคอมไม่สำเร็จ"); return
		}
		// ไม่เพิ่ม balance ทันที — ให้สมาชิกถอนเอง (เหมือน commission ปกติ)

	case "deduct":
		// หักค่าคอม pending → ลด pending commissions
		// อัพเดท commissions ล่าสุดเป็น cancelled จนครบ amount
		var pendingComms []model.ReferralCommission
		tx.Where("referrer_id = ? AND agent_id = ? AND status = ?", req.MemberID, agentID, "pending").
			Order("created_at DESC").Find(&pendingComms)

		remaining := req.Amount
		for _, pc := range pendingComms {
			if remaining <= 0 { break }
			if pc.CommissionAmount <= remaining {
				tx.Model(&pc).Update("status", "cancelled")
				remaining -= pc.CommissionAmount
			} else {
				// partial: ลดจำนวนลง
				tx.Model(&pc).Update("commission_amount", pc.CommissionAmount-remaining)
				remaining = 0
			}
		}

	case "cancel":
		// ยกเลิก commission เฉพาะรายการ
		if req.CommissionID == nil {
			tx.Rollback(); fail(c, 400, "กรุณาระบุ commission_id สำหรับการยกเลิก"); return
		}
		result := tx.Model(&model.ReferralCommission{}).
			Where("id = ? AND referrer_id = ? AND status = ?", *req.CommissionID, req.MemberID, "pending").
			Update("status", "cancelled")
		if result.RowsAffected == 0 {
			tx.Rollback(); fail(c, 404, "ไม่พบรายการค่าคอมที่ต้องการยกเลิก หรือยกเลิกไปแล้ว"); return
		}
	}

	// สร้าง audit log
	adjustment := model.CommissionAdjustment{
		AgentID:       agentID,
		MemberID:      req.MemberID,
		AdminID:       adminID,
		Type:          req.Type,
		Amount:        req.Amount,
		Reason:        req.Reason,
		CommissionID:  req.CommissionID,
		BalanceBefore: balanceBefore,
		BalanceAfter:  balanceAfter,
	}
	tx.Create(&adjustment)

	tx.Commit()

	ok(c, gin.H{
		"adjustment": adjustment,
		"message":    "ปรับค่าคอมสำเร็จ",
	})
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

	// ── mode=auto + RKAUTO enabled → สั่งถอนผ่าน RKAUTO ──
	if req.Mode == "auto" && h.RKAutoClient != nil {
		rkautoClient, ok2 := h.RKAutoClient.(*rkautoLib.Client)
		if ok2 {
			// ดึงข้อมูลบัญชีสมาชิก
			var bankCode, bankAccountNo string
			h.DB.Table("withdraw_requests").Select("bank_code, bank_account_number").Where("id = ?", id).Row().Scan(&bankCode, &bankAccountNo)

			// ดึงบัญชีต้นทาง (agent bank account ที่ register กับ RKAUTO)
			var sourceBankUUID string
			h.DB.Table("agent_bank_accounts").Select("rkauto_uuid").
				Where("agent_id = 1 AND rkauto_uuid != '' AND status = 'active'").
				Limit(1).Row().Scan(&sourceBankUUID)

			if sourceBankUUID != "" && bankAccountNo != "" {
				txnID := "WD-" + strconv.FormatInt(id, 10) + "-" + strconv.FormatInt(now.Unix(), 10)
				wdResp, wdErr := rkautoClient.CreateWithdrawal(rkautoLib.CreateWithdrawalRequest{
					TransactionID:   txnID,
					BankAccountUUID: sourceBankUUID,
					ToAccountNo:     bankAccountNo,
					ToBank:          bankCode,
					Amount:          amount,
					Currency:        "THB",
				})

				if wdErr != nil {
					// RKAUTO error → ยังคง approve manual แต่ log warning
					log.Printf("⚠️ RKAUTO withdrawal failed for #%d: %v — falling back to manual", id, wdErr)
					req.Mode = "manual"
				} else {
					// สำเร็จ → บันทึก RKAUTO UUID + transaction_id
					h.DB.Exec("UPDATE withdraw_requests SET rkauto_uuid = ?, rkauto_transaction_id = ?, rkauto_status = 'processing' WHERE id = ?",
						wdResp.Data.UUID, txnID, id)
					log.Printf("✅ RKAUTO withdrawal created: #%d → %s (track: %s)", id, wdResp.Data.UUID, wdResp.Data.TrackID)
				}
			} else {
				log.Printf("⚠️ No RKAUTO source bank for auto withdraw #%d — falling back to manual", id)
				req.Mode = "manual"
			}
		}
	}

	// อัพเดทสถานะ
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

// GetStaffLoginHistory ดูประวัติ login ของพนักงาน
// GET /api/v1/staff/:id/login-history
func (h *Handler) GetStaffLoginHistory(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var history []model.AdminLoginHistory
	h.DB.Where("admin_id = ?", id).Order("created_at DESC").Limit(50).Find(&history)
	ok(c, history)
}

// GetStaffActivity ดู activity log ของพนักงานคนเดียว
// GET /api/v1/staff/:id/activity
func (h *Handler) GetStaffActivity(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var logs []model.ActivityLog
	h.DB.Where("admin_id = ?", id).Order("created_at DESC").Limit(50).Find(&logs)
	ok(c, logs)
}

// =============================================================================
// RKAUTO — Bank Account Registration
// =============================================================================

// RegisterBankAccountRKAuto ลงทะเบียนบัญชีกับ RKAUTO
// POST /api/v1/bank-accounts/:id/register-rkauto
// Body: { "bank_system": "SMS|BANK|KBIZ", "username": "...", "password": "...",
//         "mobile_number": "..." (SMS), "bank_code": "..." (BANK) }
func (h *Handler) RegisterBankAccountRKAuto(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	if h.RKAutoClient == nil {
		fail(c, 400, "RKAUTO ไม่ได้เปิดใช้งาน (set RKAUTO_ENABLED=true)"); return
	}
	rkautoClient, _ := h.RKAutoClient.(*rkautoLib.Client)
	if rkautoClient == nil {
		fail(c, 500, "RKAUTO client error"); return
	}

	var req struct {
		BankSystem    string `json:"bank_system" binding:"required"`   // SMS, BANK, KBIZ
		Username      string `json:"username"`
		Password      string `json:"password"`
		MobileNumber  string `json:"mobile_number,omitempty"`
		BankCode      string `json:"bank_code,omitempty"`
		IsDeposit     bool   `json:"is_deposit"`
		IsWithdraw    bool   `json:"is_withdraw"`
		RKAutoToken1  string `json:"rkauto_token1,omitempty"`  // Token จากการเจน RKAUTO (ไม่เก็บ DB)
		RKAutoToken2  string `json:"rkauto_token2,omitempty"`  // Token ตัวที่ 2 (ไม่เก็บ DB)
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error()); return
	}

	// ดึงข้อมูลบัญชีจาก DB
	type BankInfo struct {
		AccountNumber string
		AccountName   string
		BankCode      string
	}
	var bank BankInfo
	h.DB.Table("agent_bank_accounts").Select("account_number, account_name, bank_code").Where("id = ?", id).Scan(&bank)
	if bank.AccountName == "" {
		fail(c, 404, "ไม่พบบัญชี"); return
	}

	// ⚠️ ใช้ RKAUTO tokens (จากการเจน) เป็น username/password ถ้ามี
	username := req.Username
	password := req.Password
	if req.RKAutoToken1 != "" {
		username = req.RKAutoToken1
	}
	if req.RKAutoToken2 != "" {
		password = req.RKAutoToken2
	}

	// เรียก RKAUTO register
	registerReq := rkautoLib.RegisterBankAccountRequest{
		BankSystem:      req.BankSystem,
		BankAccountName: bank.AccountName,
		Username:        username,
		Password:        password,
		IsDeposit:       req.IsDeposit,
		IsWithdraw:      req.IsWithdraw,
	}

	// เพิ่ม fields ตาม bank_system
	switch req.BankSystem {
	case "SMS":
		registerReq.MobileNumber = req.MobileNumber
	case "BANK":
		registerReq.BankCode = req.BankCode
		if registerReq.BankCode == "" {
			registerReq.BankCode = bank.BankCode
		}
		registerReq.BankAccountNo = bank.AccountNumber
	case "KBIZ":
		registerReq.BankAccountNo = bank.AccountNumber
	}

	resp, err := rkautoClient.RegisterBankAccount(registerReq)
	if err != nil {
		log.Printf("⚠️ RKAUTO register failed for bank #%d: %v", id, err)
		fail(c, 500, "RKAUTO register failed: "+err.Error()); return
	}

	if !resp.Success {
		fail(c, 400, "RKAUTO: "+resp.Message); return
	}

	// อัพเดท DB — ⚠️ encrypt bank credentials ด้วย AES-256
	encUsername, _ := rkautoLib.Encrypt(username, h.EncryptionKey)
	encPassword, _ := rkautoLib.Encrypt(password, h.EncryptionKey)

	h.DB.Exec(`UPDATE agent_bank_accounts SET
		rkauto_uuid = ?, rkauto_status = 'registered', bank_system = ?,
		bank_username = ?, bank_password = ?
		WHERE id = ?`,
		resp.Data.UUID, req.BankSystem, encUsername, encPassword, id)

	log.Printf("✅ RKAUTO registered bank #%d → UUID: %s", id, resp.Data.UUID)
	ok(c, gin.H{"id": id, "rkauto_uuid": resp.Data.UUID, "status": "registered"})
}

// ActivateBankAccountRKAuto เปิดใช้บัญชีกับ RKAUTO
// POST /api/v1/bank-accounts/:id/activate-rkauto
func (h *Handler) ActivateBankAccountRKAuto(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if h.RKAutoClient == nil { fail(c, 400, "RKAUTO disabled"); return }
	rkautoClient, _ := h.RKAutoClient.(*rkautoLib.Client)

	var uuid string
	h.DB.Table("agent_bank_accounts").Select("rkauto_uuid").Where("id = ?", id).Row().Scan(&uuid)
	if uuid == "" { fail(c, 400, "บัญชีนี้ยังไม่ได้ register กับ RKAUTO"); return }

	_, err := rkautoClient.ActivateBankAccount(uuid)
	if err != nil { fail(c, 500, "RKAUTO activate failed: "+err.Error()); return }

	h.DB.Exec("UPDATE agent_bank_accounts SET rkauto_status = 'active' WHERE id = ?", id)
	ok(c, gin.H{"id": id, "rkauto_status": "active"})
}

// DeactivateBankAccountRKAuto ปิดใช้บัญชีกับ RKAUTO
// POST /api/v1/bank-accounts/:id/deactivate-rkauto
func (h *Handler) DeactivateBankAccountRKAuto(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if h.RKAutoClient == nil { fail(c, 400, "RKAUTO disabled"); return }
	rkautoClient, _ := h.RKAutoClient.(*rkautoLib.Client)

	var uuid string
	h.DB.Table("agent_bank_accounts").Select("rkauto_uuid").Where("id = ?", id).Row().Scan(&uuid)
	if uuid == "" { fail(c, 400, "บัญชีนี้ยังไม่ได้ register กับ RKAUTO"); return }

	_, err := rkautoClient.DeactivateBankAccount(uuid)
	if err != nil { fail(c, 500, "RKAUTO deactivate failed: "+err.Error()); return }

	h.DB.Exec("UPDATE agent_bank_accounts SET rkauto_status = 'deactivated' WHERE id = ?", id)
	ok(c, gin.H{"id": id, "rkauto_status": "deactivated"})
}

// GetAvailablePermissions คืน permissions ทั้งหมดที่ตั้งได้
// GET /api/v1/staff/permissions
func (h *Handler) GetAvailablePermissions(c *gin.Context) {
	type PermGroup struct {
		Group string   `json:"group"`
		Label string   `json:"label"`
		Perms []struct {
			Key   string `json:"key"`
			Label string `json:"label"`
		} `json:"perms"`
	}

	permissions := []gin.H{
		{
			"group": "members", "label": "สมาชิก",
			"perms": []gin.H{
				{"key": "members.view", "label": "ดูรายการสมาชิก"},
				{"key": "members.detail", "label": "ดูรายละเอียดสมาชิก"},
				{"key": "members.edit", "label": "แก้ไขข้อมูลสมาชิก"},
				{"key": "members.suspend", "label": "ระงับ/เปิดบัญชี"},
				{"key": "members.adjust_balance", "label": "ปรับยอดเงิน (เติม/หัก)"},
			},
		},
		{
			"group": "lottery", "label": "หวย",
			"perms": []gin.H{
				{"key": "lotteries.view", "label": "ดูประเภทหวย"},
				{"key": "rounds.create", "label": "สร้างรอบหวย"},
				{"key": "results.submit", "label": "กรอกผลหวย"},
				{"key": "bans.manage", "label": "จัดการเลขอั้น"},
				{"key": "rates.manage", "label": "แก้ไขอัตราจ่าย"},
			},
		},
		{
			"group": "finance", "label": "การเงิน",
			"perms": []gin.H{
				{"key": "deposits.view", "label": "ดูรายการฝาก"},
				{"key": "deposits.approve", "label": "อนุมัติ/ปฏิเสธฝาก"},
				{"key": "withdrawals.view", "label": "ดูรายการถอน"},
				{"key": "withdrawals.approve", "label": "อนุมัติ/ปฏิเสธถอน"},
			},
		},
		{
			"group": "reports", "label": "รายงาน",
			"perms": []gin.H{
				{"key": "dashboard.view", "label": "ดู Dashboard"},
				{"key": "reports.view", "label": "ดูรายงาน"},
				{"key": "bets.view", "label": "ดูรายการแทง"},
				{"key": "transactions.view", "label": "ดูธุรกรรม"},
			},
		},
		{
			"group": "system", "label": "ระบบ",
			"perms": []gin.H{
				{"key": "staff.manage", "label": "จัดการพนักงาน"},
				{"key": "settings.manage", "label": "ตั้งค่าระบบ"},
				{"key": "cms.manage", "label": "จัดการหน้าเว็บ"},
				{"key": "affiliate.manage", "label": "จัดการ Affiliate"},
			},
		},
	}

	ok(c, permissions)
}
