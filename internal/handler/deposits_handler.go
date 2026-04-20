// Package handler — deposits admin handlers.
// Split from stubs.go on 2026-04-20.
package handler

import (
	"log"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
)

// =============================================================================
// Deposit Requests — อนุมัติ/ปฏิเสธคำขอฝากเงิน
// =============================================================================

func (h *Handler) ListDepositRequests(c *gin.Context) {
	page, perPage := pageParams(c)
	status := c.DefaultQuery("status", "")

	type DepositRow struct {
		ID          int64   `json:"id"`
		MemberID    int64   `json:"member_id"`
		Username    string  `json:"username"`
		Amount      float64 `json:"amount"`
		Status      string  `json:"status"`
		SlipURL     *string `json:"slip_url"`
		AutoMatched bool    `json:"auto_matched"`
		CreatedAt   string  `json:"created_at"`
	}

	var rows []DepositRow
	var total int64

	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope ตามสายงาน

	query := h.DB.Table("deposit_requests d").
		Select("d.id, d.member_id, m.username, d.amount, d.status, d.slip_url, COALESCE(d.auto_matched, 0) AS auto_matched, d.created_at").
		Joins("LEFT JOIN members m ON m.id = d.member_id")
	// ⭐ node เห็นเฉพาะ deposits ของ members ในสาย
	if scope.IsNode {
		query = query.Where("d.member_id IN ?", scope.MemberIDsForSQL())
	}
	if status != "" {
		query = query.Where("d.status = ?", status)
	}
	// ⭐ Date filter — date_from / date_to (format: 2006-01-02)
	if df := c.Query("date_from"); df != "" {
		query = query.Where("d.created_at >= ?", df+" 00:00:00")
	}
	if dt := c.Query("date_to"); dt != "" {
		query = query.Where("d.created_at <= ?", dt+" 23:59:59")
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
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope

	// ดึง request
	var amount float64
	var memberID int64
	var reqStatus string
	row := h.DB.Table("deposit_requests").Select("amount, member_id, status").Where("id = ?", id).Row()
	if err := row.Scan(&amount, &memberID, &reqStatus); err != nil {
		fail(c, 404, "ไม่พบคำขอ")
		return
	}
	if reqStatus != "pending" {
		fail(c, 400, "คำขอนี้ไม่ใช่สถานะรอดำเนินการ (สถานะ: "+reqStatus+")")
		return
	}
	if !scope.HasMember(memberID) {
		fail(c, 403, "ไม่มีสิทธิ์")
		return
	} // ⭐ scope

	tx := h.DB.Begin()

	now := time.Now()
	tx.Exec("UPDATE deposit_requests SET status = 'approved', approved_at = ?, approved_by = ? WHERE id = ?", now, adminID, id)

	// เพิ่มเงินให้สมาชิก
	var balanceBefore float64
	tx.Table("members").Select("balance").Where("id = ?", memberID).Row().Scan(&balanceBefore)
	tx.Exec("UPDATE members SET balance = balance + ? WHERE id = ?", amount, memberID)

	// สร้าง transaction record
	tx.Exec(`INSERT INTO transactions (agent_node_id, member_id, type, amount, balance_before, balance_after, reference_id, reference_type, note, created_at)
		VALUES (1, ?, 'deposit', ?, ?, ?, ?, 'deposit_request', ?, ?)`,
		memberID, amount, balanceBefore, balanceBefore+amount, id, "อนุมัติโดยแอดมิน #"+strconv.FormatInt(adminID, 10), now)

	// ─── First Deposit Bonus ──────────────────────────────────
	// เช็คว่าเป็นการฝากครั้งแรกหรือไม่ + settings เปิดโบนัสอยู่
	//
	// Settings ที่เกี่ยวข้อง (ตั้งค่าใน admin panel):
	//   - first_deposit_bonus_enabled: "true"/"false"
	//   - first_deposit_bonus_percent: 100 (= 100% ของยอดฝาก)
	//   - first_deposit_bonus_max: 500 (= โบนัสสูงสุด 500 บาท)
	//   - first_deposit_bonus_turnover: 5 (= ต้องเล่น 5 เท่าก่อนถอน)
	{
		// เช็ค setting ว่าเปิดโบนัสหรือไม่
		var bonusEnabled string
		tx.Table("settings").Select("value").Where("`key` = 'first_deposit_bonus_enabled'").Scan(&bonusEnabled)

		if bonusEnabled == "true" {
			// เช็คว่าเป็นการฝากครั้งแรกจริงๆ (ดูจาก transactions ว่ามี deposit ก่อนหน้าไหม)
			var prevDepositCount int64
			tx.Raw("SELECT COUNT(*) FROM transactions WHERE member_id = ? AND type = 'deposit' AND created_at < ?", memberID, now).Scan(&prevDepositCount)

			if prevDepositCount == 0 {
				// ─── คำนวณโบนัส ──────────────────────────────
				var bonusPercentStr, bonusMaxStr, turnoverStr string
				tx.Table("settings").Select("value").Where("`key` = 'first_deposit_bonus_percent'").Scan(&bonusPercentStr)
				tx.Table("settings").Select("value").Where("`key` = 'first_deposit_bonus_max'").Scan(&bonusMaxStr)
				tx.Table("settings").Select("value").Where("`key` = 'first_deposit_bonus_turnover'").Scan(&turnoverStr)

				bonusPercent := parseFloat(bonusPercentStr, 100) // default 100%
				bonusMax := parseFloat(bonusMaxStr, 500)         // default สูงสุด 500 บาท
				turnoverMultiplier := parseFloat(turnoverStr, 5) // default 5 เท่า

				// คำนวณโบนัส: amount * percent / 100, cap ที่ max
				bonus := amount * bonusPercent / 100
				if bonus > bonusMax {
					bonus = bonusMax
				}

				if bonus > 0 {
					// เพิ่มโบนัสให้สมาชิก
					balanceAfterDeposit := balanceBefore + amount
					tx.Exec("UPDATE members SET balance = balance + ? WHERE id = ?", bonus, memberID)

					// สร้าง bonus transaction
					tx.Exec(`INSERT INTO transactions (agent_node_id, member_id, type, amount, balance_before, balance_after, reference_id, reference_type, note, created_at)
						VALUES (1, ?, 'bonus', ?, ?, ?, ?, 'first_deposit', ?, ?)`,
						memberID, bonus, balanceAfterDeposit, balanceAfterDeposit+bonus, id, "โบนัสฝากครั้งแรก", now)

					// ─── ตั้ง turnover requirement ───────────
					// turnover = (ยอดฝาก + โบนัส) * turnover_multiplier
					turnoverRequired := (amount + bonus) * turnoverMultiplier
					tx.Exec("UPDATE members SET turnover_required = ?, turnover_completed = 0 WHERE id = ?", turnoverRequired, memberID)

					log.Printf("🎁 First deposit bonus: member=%d, deposit=%.2f, bonus=%.2f, turnover_req=%.2f",
						memberID, amount, bonus, turnoverRequired)
				}
			}
		}
	}

	tx.Commit()
	ok(c, gin.H{"id": id, "status": "approved", "amount": amount, "member_id": memberID})
}

// RejectDeposit ปฏิเสธคำขอฝากเงิน
// PUT /api/v1/deposits/:id/reject
// Body (optional): { "reason": "เหตุผล" }
func (h *Handler) RejectDeposit(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	adminID := middleware.GetAdminID(c)
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope
	var depMemberID int64
	h.DB.Table("deposit_requests").Select("member_id").Where("id = ?", id).Row().Scan(&depMemberID)
	if !scope.HasMember(depMemberID) {
		fail(c, 403, "ไม่มีสิทธิ์")
		return
	} // ⭐

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
		fail(c, 400, "คำขอนี้ไม่ใช่สถานะรอดำเนินการ")
		return
	}

	ok(c, gin.H{"id": id, "status": "rejected", "reason": req.Reason})
}

// CancelDeposit ยกเลิกรายการฝากที่อนุมัติแล้ว (reverse — หักเงินคืน)
// PUT /api/v1/deposits/:id/cancel
// Body (optional): { "reason": "เหตุผล" }
// CancelDeposit ยกเลิกรายการฝากที่อนุมัติแล้ว
// ⭐ รองรับ 2 โหมด:
//   - refund=true (default): ยกเลิก + หักเครดิตคืน (ฝากผิด/ซ้ำ)
//   - refund=false: ยกเลิก + ไม่หักเครดิต (แอดมินเติมให้เอง/กรณีพิเศษ)
func (h *Handler) CancelDeposit(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	adminID := middleware.GetAdminID(c)
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope
	var cdMemberID int64
	h.DB.Table("deposit_requests").Select("member_id").Where("id = ?", id).Row().Scan(&cdMemberID)
	if !scope.HasMember(cdMemberID) {
		fail(c, 403, "ไม่มีสิทธิ์")
		return
	} // ⭐

	var req struct {
		Reason string `json:"reason"`
		Refund *bool  `json:"refund"` // nil = default true (หักเครดิตคืน)
	}
	c.ShouldBindJSON(&req)
	if req.Reason == "" {
		req.Reason = "ยกเลิกโดยแอดมิน"
	}
	// ⭐ default refund = true (หักเครดิตคืน)
	shouldRefund := true
	if req.Refund != nil {
		shouldRefund = *req.Refund
	}

	// ดึงข้อมูลคำขอ
	var amount float64
	var memberID int64
	var reqStatus string
	row := h.DB.Table("deposit_requests").Select("amount, member_id, status").Where("id = ?", id).Row()
	if err := row.Scan(&amount, &memberID, &reqStatus); err != nil {
		fail(c, 404, "ไม่พบคำขอ")
		return
	}
	if reqStatus != "approved" {
		fail(c, 400, "ยกเลิกได้เฉพาะคำขอที่อนุมัติแล้ว (สถานะปัจจุบัน: "+reqStatus+")")
		return
	}

	tx := h.DB.Begin()
	now := time.Now()

	// อัพเดท status → cancelled
	tx.Exec("UPDATE deposit_requests SET status = 'cancelled', reject_reason = ?, approved_by = ? WHERE id = ?",
		req.Reason, adminID, id)

	if shouldRefund {
		// ⭐ หักเงินคืน (atomic — ตรวจว่ายอดพอ)
		debitResult := tx.Exec("UPDATE members SET balance = balance - ? WHERE id = ? AND balance >= ?", amount, memberID, amount)
		if debitResult.RowsAffected == 0 {
			tx.Rollback()
			fail(c, 400, "สมาชิกมียอดเงินไม่เพียงพอสำหรับหักคืน")
			return
		}

		// ดึง balance ล่าสุด
		var balanceAfter float64
		tx.Table("members").Select("balance").Where("id = ?", memberID).Row().Scan(&balanceAfter)

		// บันทึก transaction — หักเครดิตคืน
		tx.Exec(`INSERT INTO transactions (agent_node_id, member_id, type, amount, balance_before, balance_after, reference_id, reference_type, note, created_at)
			VALUES (1, ?, 'admin_debit', ?, ?, ?, ?, 'deposit_cancel', ?, ?)`,
			memberID, -amount, balanceAfter+amount, balanceAfter, id,
			"ยกเลิกฝาก #"+strconv.FormatInt(id, 10)+": "+req.Reason, now)
	} else {
		// ⭐ refund=false → ยกเลิกอย่างเดียว ไม่หักเงิน แต่บันทึก audit trail
		var balance float64
		tx.Table("members").Select("balance").Where("id = ?", memberID).Row().Scan(&balance)
		tx.Exec(`INSERT INTO transactions (agent_node_id, member_id, type, amount, balance_before, balance_after, reference_id, reference_type, note, created_at)
			VALUES (1, ?, 'admin_debit', 0, ?, ?, ?, 'deposit_cancel_no_refund', ?, ?)`,
			memberID, balance, balance, id,
			"ยกเลิกฝาก #"+strconv.FormatInt(id, 10)+" (ไม่หักเครดิต): "+req.Reason, now)
	}

	tx.Commit()
	ok(c, gin.H{"id": id, "status": "cancelled", "amount": amount, "member_id": memberID, "reason": req.Reason, "refund": shouldRefund})
}
