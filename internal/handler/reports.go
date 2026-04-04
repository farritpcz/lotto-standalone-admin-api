// Package handler — reports.go
// รายงานเครดิตสมาชิก (Member Credit Report) สำหรับ admin-api (#5)
//
// ⭐ ฟีเจอร์:
// - ค้นหาสมาชิกด้วย username/phone/ID
// - แสดงประวัติ credit: ฝาก/ถอน/แทง/ชนะ/ปรับยอด
// - สรุปยอด: ฝากรวม, ถอนรวม, แทงรวม, ชนะรวม, กำไร/ขาดทุน
// - ช่วงเวลา: from_date — to_date
//
// ความสัมพันธ์:
// - ใช้ transactions table (share DB กับ member-api)
// - admin-web (#6) แสดงรายงาน + กราฟ
//
// Routes:
//   GET    /api/v1/reports/member-credit   → รายงานเครดิตสมาชิก (params: member_id, q, from, to)
package handler

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// =============================================================================
// GetMemberCreditReport — GET /api/v1/reports/member-credit
// รายงานเครดิตสมาชิก — ค้นหาด้วย member_id หรือ q (username/phone)
//
// Query params:
//   member_id  — ค้นหาด้วย ID ตรงๆ
//   q          — ค้นหาด้วย username หรือ phone
//   from       — วันที่เริ่มต้น (YYYY-MM-DD) default: 30 วันก่อน
//   to         — วันที่สิ้นสุด (YYYY-MM-DD) default: วันนี้
//   page       — หน้า (default: 1)
//   per_page   — จำนวนต่อหน้า (default: 50)
// =============================================================================
func (h *Handler) GetMemberCreditReport(c *gin.Context) {
	// ─── 1. Parse parameters ─────────────────────────────────────────────
	memberIDStr := c.Query("member_id")
	search := c.Query("q")
	dateFrom := c.DefaultQuery("from", time.Now().AddDate(0, 0, -30).Format("2006-01-02"))
	dateTo := c.DefaultQuery("to", time.Now().Format("2006-01-02"))
	page, perPage := pageParams(c)

	// ⭐ ต้องระบุ member_id หรือ q อย่างใดอย่างหนึ่ง
	if memberIDStr == "" && search == "" {
		fail(c, 400, "กรุณาระบุ member_id หรือ q (username/phone)")
		return
	}

	// ─── 2. หาสมาชิก ─────────────────────────────────────────────────────
	var memberID int64
	if memberIDStr != "" {
		// ค้นหาด้วย ID ตรงๆ
		memberID, _ = strconv.ParseInt(memberIDStr, 10, 64)
	} else {
		// ⭐ ค้นหาด้วย username หรือ phone
		h.DB.Table("members").Select("id").
			Where("username = ? OR phone = ?", search, search).
			Row().Scan(&memberID)
	}

	if memberID == 0 {
		fail(c, 404, "ไม่พบสมาชิก")
		return
	}

	// ─── 3. ดึงข้อมูลสมาชิก ──────────────────────────────────────────────
	type memberInfo struct {
		ID       int64   `json:"id"`
		Username string  `json:"username"`
		Phone    string  `json:"phone"`
		Balance  float64 `json:"balance"`
		Status   string  `json:"status"`
	}
	var member memberInfo
	h.DB.Table("members").Where("id = ?", memberID).First(&member)

	// ─── 4. สรุปยอดรวม (ในช่วงเวลา) ────────────────────────────────────
	type summary struct {
		TotalDeposit  float64 `json:"total_deposit" gorm:"column:total_deposit"`
		TotalWithdraw float64 `json:"total_withdraw" gorm:"column:total_withdraw"`
		TotalBet      float64 `json:"total_bet" gorm:"column:total_bet"`
		TotalWin      float64 `json:"total_win" gorm:"column:total_win"`
		TotalCredit   float64 `json:"total_credit" gorm:"column:total_credit"`   // admin เติมเงิน
		TotalDebit    float64 `json:"total_debit" gorm:"column:total_debit"`     // admin หักเงิน
	}
	var sum summary

	// ⭐ สรุปยอดแต่ละประเภท ด้วย CASE WHEN
	h.DB.Raw(`
		SELECT
			SUM(CASE WHEN type = 'deposit' THEN amount ELSE 0 END) as total_deposit,
			SUM(CASE WHEN type = 'withdraw' THEN ABS(amount) ELSE 0 END) as total_withdraw,
			SUM(CASE WHEN type = 'bet' THEN ABS(amount) ELSE 0 END) as total_bet,
			SUM(CASE WHEN type = 'win' THEN amount ELSE 0 END) as total_win,
			SUM(CASE WHEN type = 'admin_credit' THEN amount ELSE 0 END) as total_credit,
			SUM(CASE WHEN type = 'admin_debit' THEN ABS(amount) ELSE 0 END) as total_debit
		FROM transactions
		WHERE member_id = ? AND created_at >= ? AND created_at < DATE_ADD(?, INTERVAL 1 DAY)
	`, memberID, dateFrom, dateTo).Scan(&sum)

	// ─── 5. ดึง transactions (paginated) ─────────────────────────────────
	type txRow struct {
		ID            int64     `json:"id"`
		Type          string    `json:"type"`
		Amount        float64   `json:"amount"`
		BalanceBefore float64   `json:"balance_before"`
		BalanceAfter  float64   `json:"balance_after"`
		Note          string    `json:"note"`
		ReferenceType string    `json:"reference_type"`
		ReferenceID   *int64    `json:"reference_id"`
		CreatedAt     time.Time `json:"created_at"`
	}

	var transactions []txRow
	var total int64

	// นับจำนวนทั้งหมด
	h.DB.Table("transactions").
		Where("member_id = ? AND created_at >= ? AND created_at < DATE_ADD(?, INTERVAL 1 DAY)", memberID, dateFrom, dateTo).
		Count(&total)

	// ดึง paginated (เรียงใหม่สุดก่อน)
	h.DB.Table("transactions").
		Where("member_id = ? AND created_at >= ? AND created_at < DATE_ADD(?, INTERVAL 1 DAY)", memberID, dateFrom, dateTo).
		Order("created_at DESC").
		Offset((page - 1) * perPage).
		Limit(perPage).
		Find(&transactions)

	// ─── 6. ส่ง response รวม ─────────────────────────────────────────────
	c.JSON(200, gin.H{
		"success": true,
		"data": gin.H{
			"member":       member,
			"summary":      sum,
			"transactions": gin.H{"items": transactions, "total": total, "page": page, "per_page": perPage},
			"date_range":   gin.H{"from": dateFrom, "to": dateTo},
		},
	})
}
