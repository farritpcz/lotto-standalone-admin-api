// Package handler — bank_accounts.go
// CRUD บัญชีธนาคารของ agent (ฝาก/ถอน)
//
// GET    /api/v1/agent/bank-accounts          → list
// POST   /api/v1/agent/bank-accounts          → create
// PUT    /api/v1/agent/bank-accounts/:id       → update
// DELETE /api/v1/agent/bank-accounts/:id       → delete
package handler

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

type agentBankAccount struct {
	ID            int64  `json:"id" gorm:"primaryKey"`
	AgentID       int64  `json:"agent_id"`
	BankCode      string `json:"bank_code"`
	BankName      string `json:"bank_name"`
	AccountNumber string `json:"account_number"`
	AccountName   string `json:"account_name"`
	AccountType   string `json:"account_type"`   // deposit | withdraw
	TransferMode  string `json:"transfer_mode"`  // manual | auto
	IsDefault     bool   `json:"is_default"`
	Status        string `json:"status"`
	RKAutoUUID    string `json:"rkauto_uuid"`
	RKAutoStatus  string `json:"rkauto_status"`
	BankSystem    string `json:"bank_system"`
	CreatedAt     string `json:"created_at"`
}

func (agentBankAccount) TableName() string { return "agent_bank_accounts" }

// ListAgentBankAccounts ดูบัญชีทั้งหมด
func (h *Handler) ListAgentBankAccounts(c *gin.Context) {
	var accounts []agentBankAccount
	h.DB.Where("agent_id = ?", 1).Order("id ASC").Find(&accounts)
	ok(c, accounts)
}

// CreateAgentBankAccount เพิ่มบัญชีใหม่
func (h *Handler) CreateAgentBankAccount(c *gin.Context) {
	var req struct {
		BankCode      string `json:"bank_code" binding:"required"`
		BankName      string `json:"bank_name"`
		AccountNumber string `json:"account_number" binding:"required"`
		AccountName   string `json:"account_name" binding:"required"`
		AccountType   string `json:"account_type"`   // deposit | withdraw
		TransferMode  string `json:"transfer_mode"`  // manual | auto
		IsDefault     bool   `json:"is_default"`
		BankSystem    string `json:"bank_system"`    // SMS | BANK | KBIZ (auto only)
		RKAutoToken1  string `json:"rkauto_token1"`  // ไม่เก็บ DB
		RKAutoToken2  string `json:"rkauto_token2"`  // ไม่เก็บ DB
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error()); return
	}
	if req.AccountType == "" { req.AccountType = "deposit" }
	if req.TransferMode == "" { req.TransferMode = "manual" }

	now := time.Now().Format("2006-01-02 15:04:05")
	result := h.DB.Exec(`INSERT INTO agent_bank_accounts
		(agent_id, bank_code, bank_name, account_number, account_name, account_type, transfer_mode, is_default, status, bank_system, created_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, 'active', ?, ?)`,
		req.BankCode, req.BankName, req.AccountNumber, req.AccountName,
		req.AccountType, req.TransferMode, req.IsDefault, req.BankSystem, now)

	if result.Error != nil {
		fail(c, 500, "ไม่สามารถเพิ่มบัญชีได้"); return
	}

	// ถ้า auto mode + มี token → register RKAUTO ทันที
	if req.TransferMode == "auto" && req.RKAutoToken1 != "" && h.RKAutoClient != nil {
		// จะ register ในอนาคต — ตอนนี้บันทึก bank_system ไว้ก่อน
		// TODO: call RKAUTO register with tokens
	}

	ok(c, gin.H{"status": "created", "bank_code": req.BankCode, "account_type": req.AccountType, "transfer_mode": req.TransferMode})
}

// UpdateAgentBankAccount แก้ไขบัญชี
func (h *Handler) UpdateAgentBankAccount(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		BankName      string `json:"bank_name"`
		AccountName   string `json:"account_name"`
		AccountType   string `json:"account_type"`
		TransferMode  string `json:"transfer_mode"`
		IsDefault     *bool  `json:"is_default"`
		Status        string `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error()); return
	}

	updates := map[string]interface{}{}
	if req.BankName != "" { updates["bank_name"] = req.BankName }
	if req.AccountName != "" { updates["account_name"] = req.AccountName }
	if req.AccountType != "" { updates["account_type"] = req.AccountType }
	if req.TransferMode != "" { updates["transfer_mode"] = req.TransferMode }
	if req.IsDefault != nil { updates["is_default"] = *req.IsDefault }
	if req.Status != "" { updates["status"] = req.Status }

	if len(updates) == 0 {
		fail(c, 400, "ไม่มีข้อมูลให้อัพเดท"); return
	}

	h.DB.Table("agent_bank_accounts").Where("id = ?", id).Updates(updates)
	ok(c, gin.H{"id": id, "updated": updates})
}

// DeleteAgentBankAccount ลบบัญชี
func (h *Handler) DeleteAgentBankAccount(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	h.DB.Exec("DELETE FROM agent_bank_accounts WHERE id = ?", id)
	ok(c, gin.H{"id": id, "deleted": true})
}
