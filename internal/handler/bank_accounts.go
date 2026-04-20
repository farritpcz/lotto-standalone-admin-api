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

	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
)

type agentBankAccount struct {
	ID            int64  `json:"id" gorm:"primaryKey"`
	AgentNodeID   *int64 `json:"agent_node_id" gorm:"index"` // ⭐ NULL=ระบบกลาง (admin), มีค่า=เฉพาะ node
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
	QRCodeURL     string `json:"qr_code_url"` // ⭐ QR ของบัญชีธนาคาร (optional)
	CreatedAt     string `json:"created_at"`
}

func (agentBankAccount) TableName() string { return "agent_bank_accounts" }

// ListAgentBankAccounts ดูบัญชีทั้งหมด
// ⭐ Node Scope: node เห็นเฉพาะบัญชีของตัวเอง (agent_node_id = nodeID)
//    admin เห็นบัญชีระดับระบบ (agent_node_id IS NULL)
func (h *Handler) ListAgentBankAccounts(c *gin.Context) {
	// ⭐ ดึง scope — ถ้าเป็น node จะ filter เฉพาะข้อมูลของ node นั้น
	scope := mw.GetNodeScope(c, h.DB)

	var accounts []agentBankAccount
	// ⭐ scope ตามสายงาน: node เห็นเฉพาะของตัวเอง, admin เห็นของ root node
	query := h.DB.Model(&agentBankAccount{})
	if scope.IsNode {
		query = query.Where("agent_node_id = ?", scope.NodeID)
	} else {
		query = query.Where("agent_node_id = ?", scope.RootNodeID)
	}
	query.Order("id ASC").Find(&accounts)
	ok(c, accounts)
}

// CreateAgentBankAccount เพิ่มบัญชีใหม่
// ⭐ Node Scope: set agent_node_id ให้ตรงกับ node ที่สร้าง (admin → NULL, node → nodeID)
func (h *Handler) CreateAgentBankAccount(c *gin.Context) {
	// ⭐ ดึง scope — ใช้ SettingNodeID() เพื่อ set agent_node_id ตอน INSERT
	scope := mw.GetNodeScope(c, h.DB)

	var req struct {
		BankCode      string `json:"bank_code" binding:"required"`
		BankName      string `json:"bank_name"`
		AccountNumber string `json:"account_number" binding:"required"`
		AccountName   string `json:"account_name" binding:"required"`
		AccountType   string `json:"account_type"`   // deposit | withdraw
		TransferMode  string `json:"transfer_mode"`  // manual | auto
		IsDefault     bool   `json:"is_default"`
		BankSystem    string `json:"bank_system"`    // SMS | BANK | KBIZ (auto only)
		QRCodeURL     string `json:"qr_code_url"`    // ⭐ URL ของ QR code (จาก /upload)
		RKAutoToken1  string `json:"rkauto_token1"`  // ไม่เก็บ DB
		RKAutoToken2  string `json:"rkauto_token2"`  // ไม่เก็บ DB
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error()); return
	}
	if req.AccountType == "" { req.AccountType = "deposit" }
	if req.TransferMode == "" { req.TransferMode = "manual" }

	now := time.Now().Format("2006-01-02 15:04:05")
	// ⭐ INSERT พร้อม agent_node_id — admin=NULL, node=nodeID
	// ⭐ qr_code_url — optional (frontend อัพรูปผ่าน /upload ก่อนแล้วส่ง URL มา)
	result := h.DB.Exec(`INSERT INTO agent_bank_accounts
		(agent_node_id, bank_code, bank_name, account_number, account_name, account_type, transfer_mode, is_default, status, bank_system, qr_code_url, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?)`,
		scope.SettingNodeID(), req.BankCode, req.BankName, req.AccountNumber, req.AccountName,
		req.AccountType, req.TransferMode, req.IsDefault, req.BankSystem, req.QRCodeURL, now)

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
// ⭐ Node Scope: node แก้ได้เฉพาะบัญชีของตัวเอง (agent_node_id = nodeID)
func (h *Handler) UpdateAgentBankAccount(c *gin.Context) {
	// ⭐ ดึง scope — ใช้ filter WHERE เพื่อป้องกัน node แก้ข้อมูลของ node อื่น
	scope := mw.GetNodeScope(c, h.DB)

	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		BankCode      string `json:"bank_code"`
		BankName      string `json:"bank_name"`
		AccountNumber string `json:"account_number"`
		AccountName   string `json:"account_name"`
		AccountType   string `json:"account_type"`
		TransferMode  string `json:"transfer_mode"`
		IsDefault     *bool  `json:"is_default"`
		Status        string `json:"status"`
		BankSystem    string `json:"bank_system"`
		QRCodeURL     *string `json:"qr_code_url"` // ⭐ ใช้ pointer → ส่ง "" ได้ (เพื่อลบ QR)
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error()); return
	}

	updates := map[string]interface{}{}
	if req.BankCode != "" { updates["bank_code"] = req.BankCode }
	if req.BankName != "" { updates["bank_name"] = req.BankName }
	if req.AccountNumber != "" { updates["account_number"] = req.AccountNumber }
	if req.AccountName != "" { updates["account_name"] = req.AccountName }
	if req.AccountType != "" { updates["account_type"] = req.AccountType }
	if req.TransferMode != "" { updates["transfer_mode"] = req.TransferMode }
	if req.IsDefault != nil { updates["is_default"] = *req.IsDefault }
	if req.Status != "" { updates["status"] = req.Status }
	if req.BankSystem != "" { updates["bank_system"] = req.BankSystem }
	if req.QRCodeURL != nil { updates["qr_code_url"] = *req.QRCodeURL } // ⭐ รองรับลบ QR (ส่ง "")

	if len(updates) == 0 {
		fail(c, 400, "ไม่มีข้อมูลให้อัพเดท"); return
	}

	// ⭐ scope ตามสายงาน: node แก้ได้เฉพาะบัญชีของตัวเอง
	query := h.DB.Table("agent_bank_accounts").Where("id = ?", id)
	if scope.IsNode {
		query = query.Where("agent_node_id = ?", scope.NodeID)
	}
	result := query.Updates(updates)
	if result.RowsAffected == 0 {
		fail(c, 404, "ไม่พบบัญชีนี้หรือไม่มีสิทธิ์แก้ไข"); return
	}
	ok(c, gin.H{"id": id, "updated": updates})
}

// DeleteAgentBankAccount ลบบัญชี
// ⭐ Node Scope: node ลบได้เฉพาะบัญชีของตัวเอง (agent_node_id = nodeID)
func (h *Handler) DeleteAgentBankAccount(c *gin.Context) {
	// ⭐ ดึง scope — ป้องกัน node ลบข้อมูลของ node อื่น
	scope := mw.GetNodeScope(c, h.DB)

	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	// ⭐ scope ตามสายงาน: node ลบได้เฉพาะของตัวเอง
	query := "DELETE FROM agent_bank_accounts WHERE id = ?"
	args := []interface{}{id}
	if scope.IsNode {
		query += " AND agent_node_id = ?"
		args = append(args, scope.NodeID)
	}
	result := h.DB.Exec(query, args...)
	if result.RowsAffected == 0 {
		fail(c, 404, "ไม่พบบัญชีนี้หรือไม่มีสิทธิ์ลบ"); return
	}
	ok(c, gin.H{"id": id, "deleted": true})
}
