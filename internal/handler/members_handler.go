// Package handler — members admin handlers.
// Split from stubs.go on 2026-04-20.
package handler

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

// =============================================================================
// Members CRUD
// =============================================================================

func (h *Handler) ListMembers(c *gin.Context) {
	page, perPage := pageParams(c)
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope ตามสายงาน
	var members []model.Member
	var total int64
	query := h.DB.Model(&model.Member{})
	query = scope.ScopeByNodeID(query, "agent_node_id") // ⭐ node เห็นเฉพาะ members ในสาย
	if s := c.Query("status"); s != "" {
		query = query.Where("status = ?", s)
	}
	if q := c.Query("q"); q != "" {
		query = query.Where("username LIKE ? OR phone LIKE ?", "%"+q+"%", "%"+q+"%")
	}
	query.Count(&total)
	query.Order("created_at DESC").Offset((page - 1) * perPage).Limit(perPage).Find(&members)
	paginated(c, members, total, page, perPage)
}

func (h *Handler) GetMember(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope ตามสายงาน
	var member model.Member
	if err := h.DB.First(&member, id).Error; err != nil {
		fail(c, 404, "member not found")
		return
	}
	// ⭐ เช็คว่า member อยู่ในสายของ node user
	if !scope.HasMember(id) {
		fail(c, 403, "ไม่มีสิทธิ์เข้าถึงสมาชิกนี้")
		return
	}

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
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope
	var member model.Member
	if err := h.DB.First(&member, id).Error; err != nil {
		fail(c, 404, "member not found")
		return
	}
	if !scope.HasMember(id) {
		fail(c, 403, "ไม่มีสิทธิ์")
		return
	} // ⭐
	var req struct {
		Phone       string `json:"phone"`
		Email       string `json:"email"`
		Password    string `json:"password"`      // ⭐ admin reset password
		AgentNodeID *int64 `json:"agent_node_id"` // ⭐ ย้ายสมาชิกไป node อื่น
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	if req.Phone != "" {
		member.Phone = req.Phone
	}
	if req.Email != "" {
		member.Email = req.Email
	}
	// ⭐ ย้ายสมาชิกไป node อื่น
	if req.AgentNodeID != nil {
		member.AgentNodeID = req.AgentNodeID
	}
	// ⭐ รีเซ็ตรหัสผ่าน (ถ้าส่งมา)
	if req.Password != "" {
		if len(req.Password) < 6 {
			fail(c, 400, "รหัสผ่านต้องมีอย่างน้อย 6 ตัวอักษร")
			return
		}
		hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			fail(c, 500, "failed to hash password")
			return
		}
		member.PasswordHash = string(hashed)
	}
	h.DB.Save(&member)
	ok(c, member)
}

// AdjustMemberBalance แอดมินเติม/หักเครดิตสมาชิกตรง
// PUT /api/v1/members/:id/balance
// Body: { "amount": 500, "note": "เติมเครดิตโดยแอดมิน" }
// amount เป็นบวก = เติม, ลบ = หัก
func (h *Handler) AdjustMemberBalance(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope
	var req struct {
		Amount float64 `json:"amount" binding:"required"`
		Note   string  `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	var member model.Member
	if err := h.DB.First(&member, id).Error; err != nil {
		fail(c, 404, "member not found")
		return
	}
	if !scope.HasMember(id) {
		fail(c, 403, "ไม่มีสิทธิ์")
		return
	} // ⭐

	// เช็คว่าหักแล้วไม่ติดลบ
	if req.Amount < 0 && member.Balance+req.Amount < 0 {
		fail(c, 400, "ยอดเงินไม่เพียงพอ")
		return
	}

	tx := h.DB.Begin()
	now := time.Now()

	// อัพเดทยอดเงิน
	tx.Model(&model.Member{}).Where("id = ?", id).Update("balance", h.DB.Raw("balance + ?", req.Amount))

	// สร้าง transaction record
	txType := "admin_credit"
	if req.Amount < 0 {
		txType = "admin_debit"
	}
	tx.Exec(`INSERT INTO transactions (agent_node_id, member_id, type, amount, balance_before, balance_after, reference_type, note, created_at)
		VALUES (1, ?, ?, ?, ?, ?, 'admin_adjust', ?, ?)`,
		id, txType, req.Amount, member.Balance, member.Balance+req.Amount, req.Note, now)

	tx.Commit()
	ok(c, gin.H{"member_id": id, "amount": req.Amount, "balance_before": member.Balance, "balance_after": member.Balance + req.Amount})
}

func (h *Handler) UpdateMemberStatus(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope
	if !scope.HasMember(id) {
		fail(c, 403, "ไม่มีสิทธิ์")
		return
	} // ⭐
	var req struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	h.DB.Model(&model.Member{}).Where("id = ?", id).Update("status", req.Status)
	ok(c, gin.H{"id": id, "status": req.Status})
}
