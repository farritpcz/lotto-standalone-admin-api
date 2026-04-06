// Package handler — easyslip_config.go
//
// ⭐ จัดการตั้งค่า EasySlip per agent node
// CRUD config + test connection
//
// Endpoints:
//   GET    /api/v1/easyslip/config         → ดึง config ปัจจุบัน
//   POST   /api/v1/easyslip/config         → สร้าง/อัพเดท config (upsert)
//   DELETE /api/v1/easyslip/config         → ลบ config (ปิด EasySlip)
//   POST   /api/v1/easyslip/test           → ทดสอบ API key
//   GET    /api/v1/easyslip/verifications  → ดู verify history
//
// ⚠️ Permission: system.settings
package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
)

// =============================================================================
// DTOs — Request/Response สำหรับ EasySlip Config
// =============================================================================

// EasySlipConfigRequest — request สำหรับสร้าง/อัพเดท config
type EasySlipConfigRequest struct {
	APIKey              string  `json:"api_key"`                                      // Bearer token (ว่าง = ใช้ key เดิม)
	Enabled             *bool   `json:"enabled"`                                      // เปิด/ปิด (default true)
	BankVerifyEnabled   *bool   `json:"bank_verify_enabled"`                          // ตรวจสลิปธนาคาร
	TruewalletEnabled   *bool   `json:"truewallet_enabled"`                           // ตรวจ TrueMoney
	MatchAccount        *bool   `json:"match_account"`                                // เทียบบัญชีผู้รับ
	CheckDuplicate      *bool   `json:"check_duplicate"`                              // ตรวจสลิปซ้ำ
	AutoApproveOnMatch  *bool   `json:"auto_approve_on_match"`                        // auto-approve
	AmountTolerance     float64 `json:"amount_tolerance"`                             // ยอมรับส่วนต่างยอด
}

// EasySlipConfigResponse — response ดึง config
type EasySlipConfigResponse struct {
	ID                  int64   `json:"id"`
	AgentNodeID         int64   `json:"agent_node_id"`
	HasAPIKey           bool    `json:"has_api_key"`             // ไม่ส่ง key จริง → แค่บอกว่ามีหรือไม่
	APIKeyMasked        string  `json:"api_key_masked"`          // แสดงแค่ 4 ตัวหลัง
	Enabled             bool    `json:"enabled"`
	BankVerifyEnabled   bool    `json:"bank_verify_enabled"`
	TruewalletEnabled   bool    `json:"truewallet_enabled"`
	MatchAccount        bool    `json:"match_account"`
	CheckDuplicate      bool    `json:"check_duplicate"`
	AutoApproveOnMatch  bool    `json:"auto_approve_on_match"`
	AmountTolerance     float64 `json:"amount_tolerance"`
	CreatedAt           string  `json:"created_at"`
	UpdatedAt           string  `json:"updated_at"`
}

// EasySlipVerificationRow — 1 row ใน verify history
type EasySlipVerificationRow struct {
	ID              int64    `json:"id"`
	DepositID       int64    `json:"deposit_id"`
	MemberID        int64    `json:"member_id"`
	MemberUsername  string   `json:"member_username"`
	VerifyType      string   `json:"verify_type"`
	TransRef        *string  `json:"trans_ref"`
	SlipAmount      *float64 `json:"slip_amount"`
	SenderBank      *string  `json:"sender_bank"`
	SenderName      *string  `json:"sender_name"`
	ReceiverBank    *string  `json:"receiver_bank"`
	IsDuplicate     bool     `json:"is_duplicate"`
	IsAccountMatch  *bool    `json:"is_account_match"`
	IsAmountMatch   *bool    `json:"is_amount_match"`
	Status          string   `json:"status"`
	ErrorCode       *string  `json:"error_code"`
	CreatedAt       string   `json:"created_at"`
}

// =============================================================================
// Handlers
// =============================================================================

// GetEasySlipConfig ดึง EasySlip config สำหรับ agent node ปัจจุบัน
// GET /api/v1/easyslip/config [auth + system.settings]
func (h *Handler) GetEasySlipConfig(c *gin.Context) {
	rootNodeID := mw.GetRootNodeID(c)

	var row struct {
		ID                 int64   `gorm:"column:id"`
		AgentNodeID        int64   `gorm:"column:agent_node_id"`
		APIKey             string  `gorm:"column:api_key"`
		Enabled            bool    `gorm:"column:enabled"`
		BankVerifyEnabled  bool    `gorm:"column:bank_verify_enabled"`
		TruewalletEnabled  bool    `gorm:"column:truewallet_enabled"`
		MatchAccount       bool    `gorm:"column:match_account"`
		CheckDuplicate     bool    `gorm:"column:check_duplicate"`
		AutoApproveOnMatch bool    `gorm:"column:auto_approve_on_match"`
		AmountTolerance    float64 `gorm:"column:amount_tolerance"`
		CreatedAt          string  `gorm:"column:created_at"`
		UpdatedAt          string  `gorm:"column:updated_at"`
	}

	err := h.DB.Table("easyslip_configs").
		Where("agent_node_id = ?", rootNodeID).
		First(&row).Error
	if err != nil {
		// ไม่มี config → ส่ง null (ยังไม่ตั้งค่า)
		c.JSON(http.StatusOK, gin.H{"success": true, "data": nil})
		return
	}

	// ⚠️ Mask API key — แสดงแค่ 4 ตัวหลัง
	masked := "****"
	if len(row.APIKey) >= 4 {
		masked = "****" + row.APIKey[len(row.APIKey)-4:]
	}

	resp := EasySlipConfigResponse{
		ID:                 row.ID,
		AgentNodeID:        row.AgentNodeID,
		HasAPIKey:          len(row.APIKey) > 0,
		APIKeyMasked:       masked,
		Enabled:            row.Enabled,
		BankVerifyEnabled:  row.BankVerifyEnabled,
		TruewalletEnabled:  row.TruewalletEnabled,
		MatchAccount:       row.MatchAccount,
		CheckDuplicate:     row.CheckDuplicate,
		AutoApproveOnMatch: row.AutoApproveOnMatch,
		AmountTolerance:    row.AmountTolerance,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": resp})
}

// UpsertEasySlipConfig สร้างหรืออัพเดท EasySlip config
// POST /api/v1/easyslip/config [auth + system.settings]
//
// ⭐ Upsert: ถ้ามี config อยู่แล้ว → update, ถ้ายังไม่มี → insert
func (h *Handler) UpsertEasySlipConfig(c *gin.Context) {
	rootNodeID := mw.GetRootNodeID(c)

	var req EasySlipConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}

	// ⭐ ถ้าไม่ส่ง api_key มา → ดึง key เดิมจาก DB (ไม่บังคับกรอกใหม่ทุกครั้ง)
	if req.APIKey == "" {
		var savedKey string
		h.DB.Table("easyslip_configs").Select("api_key").
			Where("agent_node_id = ?", rootNodeID).Scan(&savedKey)
		if savedKey == "" {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "กรุณากรอก API Key"})
			return
		}
		req.APIKey = savedKey
	}

	now := time.Now()

	// Default values สำหรับ boolean pointers
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	bankVerify := true
	if req.BankVerifyEnabled != nil {
		bankVerify = *req.BankVerifyEnabled
	}
	truewallet := false
	if req.TruewalletEnabled != nil {
		truewallet = *req.TruewalletEnabled
	}
	matchAccount := true
	if req.MatchAccount != nil {
		matchAccount = *req.MatchAccount
	}
	checkDuplicate := true
	if req.CheckDuplicate != nil {
		checkDuplicate = *req.CheckDuplicate
	}
	autoApprove := true
	if req.AutoApproveOnMatch != nil {
		autoApprove = *req.AutoApproveOnMatch
	}

	// ⭐ Upsert ด้วย ON DUPLICATE KEY UPDATE
	result := h.DB.Exec(`
		INSERT INTO easyslip_configs
			(agent_node_id, api_key, enabled, bank_verify_enabled, truewallet_enabled,
			 match_account, check_duplicate, auto_approve_on_match, amount_tolerance,
			 created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			api_key = VALUES(api_key),
			enabled = VALUES(enabled),
			bank_verify_enabled = VALUES(bank_verify_enabled),
			truewallet_enabled = VALUES(truewallet_enabled),
			match_account = VALUES(match_account),
			check_duplicate = VALUES(check_duplicate),
			auto_approve_on_match = VALUES(auto_approve_on_match),
			amount_tolerance = VALUES(amount_tolerance),
			updated_at = VALUES(updated_at)`,
		rootNodeID, req.APIKey, enabled, bankVerify, truewallet,
		matchAccount, checkDuplicate, autoApprove, req.AmountTolerance,
		now, now,
	)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "ไม่สามารถบันทึกการตั้งค่าได้"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "บันทึกการตั้งค่า EasySlip สำเร็จ",
	})
}

// DeleteEasySlipConfig ลบ EasySlip config (ปิด EasySlip สำหรับ agent)
// DELETE /api/v1/easyslip/config [auth + system.settings]
func (h *Handler) DeleteEasySlipConfig(c *gin.Context) {
	rootNodeID := mw.GetRootNodeID(c)

	result := h.DB.Exec("DELETE FROM easyslip_configs WHERE agent_node_id = ?", rootNodeID)
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "ไม่พบการตั้งค่า EasySlip"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "ลบการตั้งค่า EasySlip สำเร็จ ระบบจะใช้การฝากเงินแบบ manual",
	})
}

// TestEasySlipConnection ทดสอบ API key ด้วยการเรียก GET /v2/info
// POST /api/v1/easyslip/test [auth + system.settings]
//
// ⭐ ใช้สำหรับ admin ทดสอบว่า API key ใช้ได้หรือไม่ก่อนบันทึก
// ถ้าไม่ส่ง api_key มา → ดึง key จาก DB (config ที่บันทึกไว้แล้ว)
func (h *Handler) TestEasySlipConnection(c *gin.Context) {
	rootNodeID := mw.GetRootNodeID(c)

	var req struct {
		APIKey string `json:"api_key"` // optional — ถ้าว่างจะดึงจาก DB
	}
	c.ShouldBindJSON(&req)

	// ถ้าไม่ส่ง key มา → ดึงจาก DB
	apiKey := req.APIKey
	if apiKey == "" {
		var savedKey string
		h.DB.Table("easyslip_configs").Select("api_key").
			Where("agent_node_id = ?", rootNodeID).Scan(&savedKey)
		if savedKey == "" {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "กรุณากรอก API Key หรือบันทึก config ก่อน"})
			return
		}
		apiKey = savedKey
	}

	// เรียก GET /v2/info เพื่อเช็คว่า key ใช้ได้
	httpClient := &http.Client{Timeout: 10 * time.Second}
	httpReq, _ := http.NewRequest("GET", "https://api.easyslip.com/v2/info", nil)
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"error":   "ไม่สามารถเชื่อมต่อ EasySlip ได้",
			"detail":  err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"error":   "API Key ไม่ถูกต้องหรือหมดอายุ",
		})
		return
	}

	// Parse response เพื่อดึง quota info (ถ้ามี)
	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "เชื่อมต่อ EasySlip สำเร็จ",
		"data":    body,
	})
}

// ListEasySlipVerifications ดู verify history (admin ดู audit log)
// GET /api/v1/easyslip/verifications [auth + finance.deposits]
//
// Query params:
//   - page, per_page: pagination
//   - status: filter by status (verified, mismatch, duplicate, error)
//   - member_id: filter by member
//   - date_from, date_to: date range
func (h *Handler) ListEasySlipVerifications(c *gin.Context) {
	rootNodeID := mw.GetRootNodeID(c)

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "20"))
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}

	// Build query
	query := h.DB.Table("easyslip_verifications ev").
		Select(`ev.id, ev.deposit_id, ev.member_id, m.username as member_username,
				ev.verify_type, ev.trans_ref, ev.slip_amount, ev.sender_bank, ev.sender_name,
				ev.receiver_bank, ev.is_duplicate, ev.is_account_match, ev.is_amount_match,
				ev.status, ev.error_code, ev.created_at`).
		Joins("LEFT JOIN members m ON m.id = ev.member_id").
		Where("ev.agent_node_id = ?", rootNodeID)

	// Filters
	if status := c.Query("status"); status != "" {
		query = query.Where("ev.status = ?", status)
	}
	if memberID := c.Query("member_id"); memberID != "" {
		query = query.Where("ev.member_id = ?", memberID)
	}
	if dateFrom := c.Query("date_from"); dateFrom != "" {
		query = query.Where("ev.created_at >= ?", dateFrom)
	}
	if dateTo := c.Query("date_to"); dateTo != "" {
		query = query.Where("ev.created_at <= ?", dateTo+" 23:59:59")
	}

	// Count total
	var total int64
	countQuery := *query
	countQuery.Count(&total)

	// Fetch rows
	var rows []EasySlipVerificationRow
	query.Order("ev.created_at DESC").
		Offset((page - 1) * perPage).Limit(perPage).
		Scan(&rows)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items":    rows,
			"total":    total,
			"page":     page,
			"per_page": perPage,
		},
	})
}

// GetDepositVerification ดูผล verify ของ deposit request เฉพาะรายการ
// GET /api/v1/deposits/:id/verification [auth + finance.deposits]
func (h *Handler) GetDepositVerification(c *gin.Context) {
	rootNodeID := mw.GetRootNodeID(c)
	depositID, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	var row struct {
		ID              int64   `json:"id"`
		DepositID       int64   `json:"deposit_id"`
		VerifyType      string  `json:"verify_type"`
		TransRef        *string `json:"trans_ref"`
		SlipDate        *string `json:"slip_date"`
		SlipAmount      *float64 `json:"slip_amount"`
		SenderBank      *string `json:"sender_bank"`
		SenderAccount   *string `json:"sender_account"`
		SenderName      *string `json:"sender_name"`
		ReceiverBank    *string `json:"receiver_bank"`
		ReceiverAccount *string `json:"receiver_account"`
		ReceiverName    *string `json:"receiver_name"`
		IsDuplicate     bool    `json:"is_duplicate"`
		IsAccountMatch  *bool   `json:"is_account_match"`
		IsAmountMatch   *bool   `json:"is_amount_match"`
		Status          string  `json:"status"`
		ErrorCode       *string `json:"error_code"`
		ErrorMessage    *string `json:"error_message"`
		CreatedAt       string  `json:"created_at"`
	}

	err := h.DB.Table("easyslip_verifications").
		Where("deposit_id = ? AND agent_node_id = ?", depositID, rootNodeID).
		Order("created_at DESC").
		First(&row).Error
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   fmt.Sprintf("ไม่พบผลตรวจสลิปสำหรับรายการ #%d", depositID),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": row})
}
