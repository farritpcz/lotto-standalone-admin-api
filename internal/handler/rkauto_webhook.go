// Package handler — rkauto_webhook.go
// Webhook handlers สำหรับรับ callback จาก RKAUTO (GobexPay)
//
// ⚠️ SECURITY:
// - endpoints เหล่านี้ถูกป้องกันด้วย WebhookSecurity middleware แล้ว
// - ไม่ต้องเช็ค JWT (เป็น public endpoint)
// - เช็ค idempotency: ถ้า transaction_id ซ้ำ → return 200 OK ไม่ process ซ้ำ
//
// Endpoints:
// - POST /webhooks/rkauto/deposit-notify  → เงินเข้าบัญชี
// - POST /webhooks/rkauto/withdraw-notify → ถอนเงินเสร็จ/ล้มเหลว
package handler

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/farritpcz/lotto-standalone-admin-api/internal/rkauto"
)

// HandleDepositNotify รับ callback เมื่อมีเงินเข้าบัญชีที่ register กับ RKAUTO
//
// Flow:
//  1. Parse payload → DepositWebhookPayload
//  2. เช็ค idempotency (transaction_id ซ้ำ?)
//  3. Match กับ deposit_request ที่ pending (amount exact match)
//  4. ถ้า match → auto-approve + credit balance
//  5. ถ้าไม่ match → บันทึกเป็น unmatched สำหรับ admin ดู
func (h *Handler) HandleDepositNotify(c *gin.Context) {
	// Parse payload
	bodyBytes, _ := c.Get("webhook_body")
	body := bodyBytes.([]byte)

	var payload rkauto.DepositWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		log.Printf("[WEBHOOK DEPOSIT] Parse error: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	log.Printf("[WEBHOOK DEPOSIT] event=%s uuid=%s amount=%.2f from=%s/%s",
		payload.Event, payload.UUID, payload.Amount, payload.FromBank, payload.FromAccountNo)

	// ── Idempotency check: ถ้า RKAUTO UUID เคย process แล้ว → return OK ──
	var existingCount int64
	h.DB.Table("deposit_requests").Where("rkauto_uuid = ?", payload.UUID).Count(&existingCount)
	if existingCount > 0 {
		log.Printf("[WEBHOOK DEPOSIT] Already processed: %s", payload.UUID)
		c.JSON(http.StatusOK, gin.H{"status": "already_processed"})
		return
	}

	// ── Match กับ pending deposit_request (amount exact match, ภายใน 24 ชม.) ──
	type DepositMatch struct {
		ID       int64   `json:"id"`
		MemberID int64   `json:"member_id"`
		Amount   float64 `json:"amount"`
	}
	var match DepositMatch
	err := h.DB.Table("deposit_requests").
		Select("id, member_id, amount").
		Where("status = ? AND amount = ? AND created_at >= ?",
			"pending", payload.Amount, time.Now().Add(-24*time.Hour)).
		Order("created_at ASC").
		Limit(1).
		Scan(&match).Error

	if err != nil || match.ID == 0 {
		// ── ไม่พบ match → บันทึก unmatched deposit ──
		log.Printf("[WEBHOOK DEPOSIT] No match for %.2f from %s/%s — saving as unmatched",
			payload.Amount, payload.FromBank, payload.FromAccountNo)

		h.DB.Exec(`INSERT INTO deposit_requests
			(member_id, agent_id, amount, status, rkauto_uuid, rkauto_transaction_id, auto_matched, created_at, updated_at)
			VALUES (0, 1, ?, 'unmatched', ?, ?, false, ?, ?)`,
			payload.Amount, payload.UUID, payload.TransactionID, time.Now(), time.Now())

		c.JSON(http.StatusOK, gin.H{"status": "unmatched", "amount": payload.Amount})
		return
	}

	// ── Match found → Auto-approve ──
	log.Printf("[WEBHOOK DEPOSIT] Matched! deposit_request #%d member=%d amount=%.2f",
		match.ID, match.MemberID, match.Amount)

	tx := h.DB.Begin()
	now := time.Now()

	// 1. Update deposit_request → approved
	tx.Exec(`UPDATE deposit_requests SET
		status = 'approved', approved_at = ?, rkauto_uuid = ?, rkauto_transaction_id = ?, auto_matched = true
		WHERE id = ?`,
		now, payload.UUID, payload.TransactionID, match.ID)

	// 2. Credit member balance
	var balanceBefore float64
	tx.Table("members").Select("balance").Where("id = ?", match.MemberID).Row().Scan(&balanceBefore)
	tx.Exec("UPDATE members SET balance = balance + ? WHERE id = ?", match.Amount, match.MemberID)

	// 3. Create transaction record
	tx.Exec(`INSERT INTO transactions
		(member_id, type, amount, balance_before, balance_after, reference_type, note, created_at)
		VALUES (?, 'deposit', ?, ?, ?, 'rkauto_auto', ?, ?)`,
		match.MemberID, match.Amount, balanceBefore, balanceBefore+match.Amount,
		"ฝากอัตโนมัติ RKAUTO ("+payload.FromBank+"/"+payload.FromAccountNo+")", now)

	if err := tx.Commit().Error; err != nil {
		log.Printf("[WEBHOOK DEPOSIT] Transaction failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "transaction failed"})
		return
	}

	log.Printf("[WEBHOOK DEPOSIT] Auto-approved deposit #%d member=%d +%.2f",
		match.ID, match.MemberID, match.Amount)

	c.JSON(http.StatusOK, gin.H{"status": "matched", "deposit_request_id": match.ID, "member_id": match.MemberID})
}

// HandleWithdrawNotify รับ callback เมื่อ RKAUTO โอนเงินเสร็จ/ล้มเหลว
//
// Flow:
//  1. Parse payload → WithdrawalWebhookPayload
//  2. หา withdraw_request จาก rkauto_transaction_id
//  3. อัพเดทสถานะ: completed → approved, failed → refund + คืนเงิน
func (h *Handler) HandleWithdrawNotify(c *gin.Context) {
	bodyBytes, _ := c.Get("webhook_body")
	body := bodyBytes.([]byte)

	var payload rkauto.WithdrawalWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		log.Printf("[WEBHOOK WITHDRAW] Parse error: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	log.Printf("[WEBHOOK WITHDRAW] event=%s uuid=%s status=%s amount=%.2f txn=%s",
		payload.Event, payload.UUID, payload.Status, payload.Amount, payload.TransactionID)

	// ── หา withdraw_request จาก transaction_id ──
	type WithdrawMatch struct {
		ID       int64   `json:"id"`
		MemberID int64   `json:"member_id"`
		Amount   float64 `json:"amount"`
		Status   string  `json:"status"`
	}
	var match WithdrawMatch
	h.DB.Table("withdraw_requests").
		Select("id, member_id, amount, status").
		Where("rkauto_transaction_id = ?", payload.TransactionID).
		Scan(&match)

	if match.ID == 0 {
		log.Printf("[WEBHOOK WITHDRAW] No matching withdraw_request for txn=%s", payload.TransactionID)
		c.JSON(http.StatusOK, gin.H{"status": "no_match", "transaction_id": payload.TransactionID})
		return
	}

	now := time.Now()

	switch payload.Status {
	case "completed":
		// ── โอนเงินสำเร็จ → approved ──
		h.DB.Exec(`UPDATE withdraw_requests SET
			status = 'approved', rkauto_uuid = ?, rkauto_status = 'completed', approved_at = ?
			WHERE id = ?`,
			payload.UUID, now, match.ID)

		log.Printf("[WEBHOOK WITHDRAW] Completed: withdraw #%d member=%d %.2f", match.ID, match.MemberID, match.Amount)

	case "failed":
		// ── โอนล้มเหลว → คืนเงิน ──
		errMsg := ""
		if payload.ErrorMessage != nil {
			errMsg = *payload.ErrorMessage
		}

		tx := h.DB.Begin()

		tx.Exec(`UPDATE withdraw_requests SET
			status = 'rejected', rkauto_uuid = ?, rkauto_status = 'failed',
			reject_reason = ?, approved_at = ?
			WHERE id = ?`,
			payload.UUID, "RKAUTO failed: "+errMsg, now, match.ID)

		// คืนเงินให้สมาชิก
		tx.Exec("UPDATE members SET balance = balance + ? WHERE id = ?", match.Amount, match.MemberID)

		var balanceAfter float64
		tx.Table("members").Select("balance").Where("id = ?", match.MemberID).Row().Scan(&balanceAfter)

		tx.Exec(`INSERT INTO transactions
			(member_id, type, amount, balance_before, balance_after, reference_type, note, created_at)
			VALUES (?, 'refund', ?, ?, ?, 'rkauto_failed', ?, ?)`,
			match.MemberID, match.Amount, balanceAfter-match.Amount, balanceAfter,
			"คืนเงินถอน — RKAUTO ล้มเหลว: "+errMsg, now)

		tx.Commit()

		log.Printf("[WEBHOOK WITHDRAW] Failed + refunded: withdraw #%d member=%d +%.2f reason=%s",
			match.ID, match.MemberID, match.Amount, errMsg)
	}

	c.JSON(http.StatusOK, gin.H{"status": "processed", "withdraw_request_id": match.ID})
}
