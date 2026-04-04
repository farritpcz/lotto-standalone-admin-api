// Package handler — promotions.go
// ระบบโปรโมชั่นสำหรับ admin-api (#5)
//
// ⭐ ฟีเจอร์:
// - CRUD โปรโมชั่น (first_deposit, deposit, cashback ฯลฯ)
// - เงื่อนไข: min deposit, max bonus, turnover multiplier
// - ระยะเวลา: start_date → end_date (หมดอายุอัตโนมัติ)
// - สถานะ: active/inactive/expired
// - ใช้ครั้งเดียว / ซ้ำได้ / จำกัดต่อสมาชิก
//
// ความสัมพันธ์:
// - ตาราง promotions → share DB กับ member-api (#3)
// - member-web (#4) แสดงโปรโมชั่นที่กำลังเปิด + ให้สมาชิกเลือกรับ
// - admin-web (#6) ใช้ CRUD + ดูสถิติ
//
// Routes:
//   GET    /api/v1/promotions          → รายการโปรโมชั่น (filter: status, type)
//   POST   /api/v1/promotions          → สร้างโปรโมชั่นใหม่
//   PUT    /api/v1/promotions/:id      → แก้ไขโปรโมชั่น
//   DELETE /api/v1/promotions/:id      → ลบโปรโมชั่น (soft: เปลี่ยน status=deleted)
//   PUT    /api/v1/promotions/:id/status → เปิด/ปิด โปรโมชั่น
package handler

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// =============================================================================
// Inline Model — promotion
// =============================================================================

// promotion โครงสร้างตาราง promotions
// เก็บข้อมูลโปรโมชั่น + เงื่อนไข + ระยะเวลา
type promotion struct {
	ID           int64     `json:"id" gorm:"primaryKey"`
	AgentID      int64     `json:"agent_id" gorm:"not null;default:1;index"`
	Name         string    `json:"name" gorm:"size:200;not null"`           // ชื่อโปร เช่น "สมัครใหม่รับ 100%"
	Type         string    `json:"type" gorm:"size:30;not null"`            // first_deposit, deposit, cashback, free_credit
	Description  string    `json:"description" gorm:"type:text"`           // รายละเอียดโปร (HTML/plain)
	ImageURL     string    `json:"image_url" gorm:"size:500"`              // รูปแบนเนอร์โปร
	BonusPct     float64   `json:"bonus_pct" gorm:"type:decimal(5,2)"`    // โบนัส % เช่น 100 = 100%
	MaxBonus     float64   `json:"max_bonus" gorm:"type:decimal(15,2)"`   // โบนัสสูงสุด (บาท)
	MinDeposit   float64   `json:"min_deposit" gorm:"type:decimal(15,2)"` // ยอดฝากขั้นต่ำ
	Turnover     float64   `json:"turnover" gorm:"type:decimal(5,2)"`     // ทำยอด X เท่าถึงถอนได้
	MaxPerMember int       `json:"max_per_member" gorm:"default:1"`       // ใช้ได้กี่ครั้ง/คน (0=ไม่จำกัด)
	MaxTotal     int       `json:"max_total" gorm:"default:0"`            // จำกัดจำนวนรวม (0=ไม่จำกัด)
	UsedCount    int       `json:"used_count" gorm:"default:0"`           // จำนวนที่ใช้ไปแล้ว
	StartDate    string    `json:"start_date" gorm:"size:10"`             // "2026-04-01"
	EndDate      string    `json:"end_date" gorm:"size:10"`               // "2026-04-30"
	Status       string    `json:"status" gorm:"size:20;not null;default:active"` // active, inactive, expired, deleted
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (promotion) TableName() string { return "promotions" }

// =============================================================================
// ListPromotions — GET /api/v1/promotions
// ดึงรายการโปรโมชั่น (filter ได้ด้วย status, type)
// =============================================================================
func (h *Handler) ListPromotions(c *gin.Context) {
	var promos []promotion

	// ⭐ เริ่มสร้าง query — filter ด้วย agent_id
	q := h.DB.Where("agent_id = ? AND status != ?", 1, "deleted")

	// Filter by status: active, inactive, expired
	if status := c.Query("status"); status != "" {
		q = q.Where("status = ?", status)
	}

	// Filter by type: first_deposit, deposit, cashback, free_credit
	if promoType := c.Query("type"); promoType != "" {
		q = q.Where("type = ?", promoType)
	}

	// เรียงตามวันสร้าง ใหม่สุดก่อน
	if err := q.Order("created_at DESC").Find(&promos).Error; err != nil {
		fail(c, 500, "ดึงข้อมูลโปรโมชั่นไม่สำเร็จ")
		return
	}

	// ⭐ ตรวจ + อัพเดทสถานะหมดอายุ (end_date < today)
	today := time.Now().Format("2006-01-02")
	for i := range promos {
		if promos[i].Status == "active" && promos[i].EndDate != "" && promos[i].EndDate < today {
			promos[i].Status = "expired"
			h.DB.Table("promotions").Where("id = ?", promos[i].ID).Update("status", "expired")
		}
	}

	ok(c, promos)
}

// =============================================================================
// CreatePromotion — POST /api/v1/promotions
// สร้างโปรโมชั่นใหม่ พร้อมเงื่อนไข + ระยะเวลา
// =============================================================================
func (h *Handler) CreatePromotion(c *gin.Context) {
	var req struct {
		Name         string  `json:"name" binding:"required"`
		Type         string  `json:"type" binding:"required"` // first_deposit, deposit, cashback, free_credit
		Description  string  `json:"description"`
		ImageURL     string  `json:"image_url"`
		BonusPct     float64 `json:"bonus_pct"`     // 100 = 100%
		MaxBonus     float64 `json:"max_bonus"`      // สูงสุด (บาท)
		MinDeposit   float64 `json:"min_deposit"`    // ขั้นต่ำ (บาท)
		Turnover     float64 `json:"turnover"`        // ทำยอด X เท่า
		MaxPerMember int     `json:"max_per_member"` // จำกัดต่อคน
		MaxTotal     int     `json:"max_total"`       // จำกัดทั้งหมด
		StartDate    string  `json:"start_date"`     // "2026-04-01"
		EndDate      string  `json:"end_date"`       // "2026-04-30"
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// ⭐ validate type ต้องเป็นค่าที่กำหนด
	validTypes := map[string]bool{"first_deposit": true, "deposit": true, "cashback": true, "free_credit": true}
	if !validTypes[req.Type] {
		fail(c, 400, "type ไม่ถูกต้อง — ต้องเป็น first_deposit, deposit, cashback, free_credit")
		return
	}

	// default max_per_member = 1 ถ้าไม่ส่ง
	if req.MaxPerMember == 0 {
		req.MaxPerMember = 1
	}

	promo := promotion{
		AgentID:      1,
		Name:         req.Name,
		Type:         req.Type,
		Description:  req.Description,
		ImageURL:     req.ImageURL,
		BonusPct:     req.BonusPct,
		MaxBonus:     req.MaxBonus,
		MinDeposit:   req.MinDeposit,
		Turnover:     req.Turnover,
		MaxPerMember: req.MaxPerMember,
		MaxTotal:     req.MaxTotal,
		StartDate:    req.StartDate,
		EndDate:      req.EndDate,
		Status:       "active",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	if err := h.DB.Create(&promo).Error; err != nil {
		fail(c, 500, "สร้างโปรโมชั่นไม่สำเร็จ: "+err.Error())
		return
	}

	ok(c, promo)
}

// =============================================================================
// UpdatePromotion — PUT /api/v1/promotions/:id
// แก้ไขโปรโมชั่น — partial update
// =============================================================================
func (h *Handler) UpdatePromotion(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	var req struct {
		Name         *string  `json:"name"`
		Type         *string  `json:"type"`
		Description  *string  `json:"description"`
		ImageURL     *string  `json:"image_url"`
		BonusPct     *float64 `json:"bonus_pct"`
		MaxBonus     *float64 `json:"max_bonus"`
		MinDeposit   *float64 `json:"min_deposit"`
		Turnover     *float64 `json:"turnover"`
		MaxPerMember *int     `json:"max_per_member"`
		MaxTotal     *int     `json:"max_total"`
		StartDate    *string  `json:"start_date"`
		EndDate      *string  `json:"end_date"`
		Status       *string  `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// ⭐ สร้าง map เฉพาะ fields ที่ส่งมา
	updates := map[string]interface{}{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Type != nil {
		updates["type"] = *req.Type
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.ImageURL != nil {
		updates["image_url"] = *req.ImageURL
	}
	if req.BonusPct != nil {
		updates["bonus_pct"] = *req.BonusPct
	}
	if req.MaxBonus != nil {
		updates["max_bonus"] = *req.MaxBonus
	}
	if req.MinDeposit != nil {
		updates["min_deposit"] = *req.MinDeposit
	}
	if req.Turnover != nil {
		updates["turnover"] = *req.Turnover
	}
	if req.MaxPerMember != nil {
		updates["max_per_member"] = *req.MaxPerMember
	}
	if req.MaxTotal != nil {
		updates["max_total"] = *req.MaxTotal
	}
	if req.StartDate != nil {
		updates["start_date"] = *req.StartDate
	}
	if req.EndDate != nil {
		updates["end_date"] = *req.EndDate
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}

	if len(updates) == 0 {
		fail(c, 400, "ไม่มีข้อมูลให้อัพเดท")
		return
	}

	updates["updated_at"] = time.Now()

	result := h.DB.Table("promotions").Where("id = ? AND agent_id = 1", id).Updates(updates)
	if result.RowsAffected == 0 {
		fail(c, 404, "ไม่พบโปรโมชั่นนี้")
		return
	}

	var promo promotion
	h.DB.First(&promo, id)
	ok(c, promo)
}

// =============================================================================
// DeletePromotion — DELETE /api/v1/promotions/:id
// ลบโปรโมชั่น (soft delete → status = "deleted")
// =============================================================================
func (h *Handler) DeletePromotion(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	// ⭐ soft delete — เปลี่ยน status เป็น deleted (ไม่ลบจาก DB จริง)
	result := h.DB.Table("promotions").Where("id = ? AND agent_id = 1", id).Updates(map[string]interface{}{
		"status":     "deleted",
		"updated_at": time.Now(),
	})
	if result.RowsAffected == 0 {
		fail(c, 404, "ไม่พบโปรโมชั่นนี้")
		return
	}

	ok(c, gin.H{"id": id, "deleted": true})
}

// =============================================================================
// UpdatePromotionStatus — PUT /api/v1/promotions/:id/status
// เปิด/ปิดโปรโมชั่น
// =============================================================================
func (h *Handler) UpdatePromotionStatus(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	var req struct {
		Status string `json:"status" binding:"required"` // active หรือ inactive
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// ⭐ validate status
	if req.Status != "active" && req.Status != "inactive" {
		fail(c, 400, "status ต้องเป็น active หรือ inactive")
		return
	}

	result := h.DB.Table("promotions").Where("id = ? AND agent_id = 1", id).Updates(map[string]interface{}{
		"status":     req.Status,
		"updated_at": time.Now(),
	})
	if result.RowsAffected == 0 {
		fail(c, 404, "ไม่พบโปรโมชั่นนี้")
		return
	}

	ok(c, gin.H{"id": id, "status": req.Status})
}
