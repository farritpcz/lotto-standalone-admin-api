// Package handler — withdraws admin handlers.
// Split from stubs.go on 2026-04-20.
package handler

import (
	"log"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	rkautoLib "github.com/farritpcz/lotto-standalone-admin-api/internal/rkauto"
)

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

	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope ตามสายงาน

	query := h.DB.Table("withdraw_requests w").
		Select("w.id, w.member_id, m.username, w.amount, w.bank_code, w.bank_account_number, w.bank_account_name, w.status, w.created_at").
		Joins("LEFT JOIN members m ON m.id = w.member_id")
	// ⭐ node เห็นเฉพาะ withdrawals ของ members ในสาย
	if scope.IsNode {
		query = query.Where("w.member_id IN ?", scope.MemberIDsForSQL())
	}
	if status != "" {
		query = query.Where("w.status = ?", status)
	}
	// ⭐ Date filter — date_from / date_to (format: 2006-01-02)
	if df := c.Query("date_from"); df != "" {
		query = query.Where("w.created_at >= ?", df+" 00:00:00")
	}
	if dt := c.Query("date_to"); dt != "" {
		query = query.Where("w.created_at <= ?", dt+" 23:59:59")
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
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope
	var awMemberID int64
	h.DB.Table("withdraw_requests").Select("member_id").Where("id = ?", id).Row().Scan(&awMemberID)
	if !scope.HasMember(awMemberID) {
		fail(c, 403, "ไม่มีสิทธิ์")
		return
	} // ⭐

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
		fail(c, 404, "ไม่พบคำขอ")
		return
	}
	if reqStatus != "pending" {
		fail(c, 400, "คำขอนี้ไม่ใช่สถานะรอดำเนินการ (สถานะ: "+reqStatus+")")
		return
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
				Where("agent_node_id IS NOT NULL AND rkauto_uuid != '' AND status = 'active'").
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
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope
	var rwMemberID int64
	h.DB.Table("withdraw_requests").Select("member_id").Where("id = ?", id).Row().Scan(&rwMemberID)
	if !scope.HasMember(rwMemberID) {
		fail(c, 403, "ไม่มีสิทธิ์")
		return
	} // ⭐

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
		fail(c, 404, "ไม่พบคำขอ")
		return
	}
	if reqStatus != "pending" {
		fail(c, 400, "คำขอนี้ไม่ใช่สถานะรอดำเนินการ (สถานะ: "+reqStatus+")")
		return
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
		tx.Exec(`INSERT INTO transactions (agent_node_id, member_id, type, amount, balance_before, balance_after, reference_id, reference_type, note, created_at)
			VALUES (1, ?, 'refund', ?, ?, ?, ?, 'withdraw_reject', ?, ?)`,
			memberID, amount, balanceAfter-amount, balanceAfter, id,
			"คืนเงินถอน #"+strconv.FormatInt(id, 10)+": "+req.Reason, now)
	} else {
		// ⭐ refund=false → ไม่คืนเงิน (ทุจริต) แต่บันทึก audit trail
		var balance float64
		tx.Table("members").Select("balance").Where("id = ?", memberID).Row().Scan(&balance)
		tx.Exec(`INSERT INTO transactions (agent_node_id, member_id, type, amount, balance_before, balance_after, reference_id, reference_type, note, created_at)
			VALUES (1, ?, 'admin_debit', 0, ?, ?, ?, 'withdraw_reject_no_refund', ?, ?)`,
			memberID, balance, balance, id,
			"ปฏิเสธถอน #"+strconv.FormatInt(id, 10)+" (ไม่คืนเครดิต/ทุจริต): "+req.Reason, now)
	}

	tx.Commit()
	ok(c, gin.H{"id": id, "status": "rejected", "refund": shouldRefund, "reason": req.Reason, "amount": amount})
}
