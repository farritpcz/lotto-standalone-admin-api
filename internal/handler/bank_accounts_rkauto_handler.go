// Package handler — rkauto bank accounts admin handlers.
// Split from stubs.go on 2026-04-20.
package handler

import (
	"log"
	"strconv"

	"github.com/gin-gonic/gin"

	rkautoLib "github.com/farritpcz/lotto-standalone-admin-api/internal/rkauto"
)

// =============================================================================
// RKAUTO — Bank Account Registration
// =============================================================================

// RegisterBankAccountRKAuto ลงทะเบียนบัญชีกับ RKAUTO
// POST /api/v1/bank-accounts/:id/register-rkauto
// Body: { "bank_system": "SMS|BANK|KBIZ", "username": "...", "password": "...",
//
//	"mobile_number": "..." (SMS), "bank_code": "..." (BANK) }
func (h *Handler) RegisterBankAccountRKAuto(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	if h.RKAutoClient == nil {
		fail(c, 400, "RKAUTO ไม่ได้เปิดใช้งาน (set RKAUTO_ENABLED=true)")
		return
	}
	rkautoClient, _ := h.RKAutoClient.(*rkautoLib.Client)
	if rkautoClient == nil {
		fail(c, 500, "RKAUTO client error")
		return
	}

	var req struct {
		BankSystem   string `json:"bank_system" binding:"required"` // SMS, BANK, KBIZ
		Username     string `json:"username"`
		Password     string `json:"password"`
		MobileNumber string `json:"mobile_number,omitempty"`
		BankCode     string `json:"bank_code,omitempty"`
		IsDeposit    bool   `json:"is_deposit"`
		IsWithdraw   bool   `json:"is_withdraw"`
		RKAutoToken1 string `json:"rkauto_token1,omitempty"` // Token จากการเจน RKAUTO (ไม่เก็บ DB)
		RKAutoToken2 string `json:"rkauto_token2,omitempty"` // Token ตัวที่ 2 (ไม่เก็บ DB)
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
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
		fail(c, 404, "ไม่พบบัญชี")
		return
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
		fail(c, 500, "RKAUTO register failed: "+err.Error())
		return
	}

	if !resp.Success {
		fail(c, 400, "RKAUTO: "+resp.Message)
		return
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
	if h.RKAutoClient == nil {
		fail(c, 400, "RKAUTO disabled")
		return
	}
	rkautoClient, _ := h.RKAutoClient.(*rkautoLib.Client)

	var uuid string
	h.DB.Table("agent_bank_accounts").Select("rkauto_uuid").Where("id = ?", id).Row().Scan(&uuid)
	if uuid == "" {
		fail(c, 400, "บัญชีนี้ยังไม่ได้ register กับ RKAUTO")
		return
	}

	_, err := rkautoClient.ActivateBankAccount(uuid)
	if err != nil {
		fail(c, 500, "RKAUTO activate failed: "+err.Error())
		return
	}

	h.DB.Exec("UPDATE agent_bank_accounts SET rkauto_status = 'active' WHERE id = ?", id)
	ok(c, gin.H{"id": id, "rkauto_status": "active"})
}

// DeactivateBankAccountRKAuto ปิดใช้บัญชีกับ RKAUTO
// POST /api/v1/bank-accounts/:id/deactivate-rkauto
func (h *Handler) DeactivateBankAccountRKAuto(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if h.RKAutoClient == nil {
		fail(c, 400, "RKAUTO disabled")
		return
	}
	rkautoClient, _ := h.RKAutoClient.(*rkautoLib.Client)

	var uuid string
	h.DB.Table("agent_bank_accounts").Select("rkauto_uuid").Where("id = ?", id).Row().Scan(&uuid)
	if uuid == "" {
		fail(c, 400, "บัญชีนี้ยังไม่ได้ register กับ RKAUTO")
		return
	}

	_, err := rkautoClient.DeactivateBankAccount(uuid)
	if err != nil {
		fail(c, 500, "RKAUTO deactivate failed: "+err.Error())
		return
	}

	h.DB.Exec("UPDATE agent_bank_accounts SET rkauto_status = 'deactivated' WHERE id = ?", id)
	ok(c, gin.H{"id": id, "rkauto_status": "deactivated"})
}

// GetAvailablePermissions คืน permissions ทั้งหมดที่ตั้งได้
// GET /api/v1/staff/permissions
