// Package handler — contact_channels.go
// CRUD ช่องทางติดต่อ (Line, Telegram, Facebook, Phone, etc.)
//
// Admin: GET/POST/PUT/DELETE /api/v1/contact-channels
// Public: GET /api/v1/public/contact-channels (ไม่ต้อง auth — member เรียกใช้)
package handler

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

type contactChannel struct {
	ID        int64  `json:"id" gorm:"primaryKey"`
	AgentID   int64  `json:"agent_id"`
	Platform  string `json:"platform"`
	Name      string `json:"name"`
	Value     string `json:"value"`
	LinkURL   string `json:"link_url"`
	IconURL   string `json:"icon_url"`
	SortOrder int    `json:"sort_order"`
	IsActive  bool   `json:"is_active"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func (contactChannel) TableName() string { return "contact_channels" }

// ListContactChannels ดูช่องทางติดต่อทั้งหมด (admin — ทุกสถานะ)
func (h *Handler) ListContactChannels(c *gin.Context) {
	var channels []contactChannel
	h.DB.Where("agent_id = ?", 1).Order("sort_order ASC, id ASC").Find(&channels)
	ok(c, channels)
}

// ListPublicContactChannels ดูช่องทางติดต่อ (public — เฉพาะ active)
func (h *Handler) ListPublicContactChannels(c *gin.Context) {
	var channels []contactChannel
	h.DB.Where("agent_id = ? AND is_active = ?", 1, true).Order("sort_order ASC").Find(&channels)
	ok(c, channels)
}

// CreateContactChannel เพิ่มช่องทางติดต่อ
func (h *Handler) CreateContactChannel(c *gin.Context) {
	var req struct {
		Platform  string `json:"platform" binding:"required"`
		Name      string `json:"name" binding:"required"`
		Value     string `json:"value" binding:"required"`
		LinkURL   string `json:"link_url"`
		IconURL   string `json:"icon_url"`
		SortOrder int    `json:"sort_order"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error()); return
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	h.DB.Exec(`INSERT INTO contact_channels (agent_id, platform, name, value, link_url, icon_url, sort_order, is_active, created_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, 1, ?)`,
		req.Platform, req.Name, req.Value, req.LinkURL, req.IconURL, req.SortOrder, now)

	ok(c, gin.H{"status": "created", "platform": req.Platform, "name": req.Name})
}

// UpdateContactChannel แก้ไขช่องทาง
func (h *Handler) UpdateContactChannel(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		Platform  string `json:"platform"`
		Name      string `json:"name"`
		Value     string `json:"value"`
		LinkURL   string `json:"link_url"`
		IconURL   string `json:"icon_url"`
		SortOrder *int   `json:"sort_order"`
		IsActive  *bool  `json:"is_active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error()); return
	}

	updates := map[string]interface{}{}
	if req.Platform != "" { updates["platform"] = req.Platform }
	if req.Name != "" { updates["name"] = req.Name }
	if req.Value != "" { updates["value"] = req.Value }
	if req.LinkURL != "" { updates["link_url"] = req.LinkURL }
	if req.IconURL != "" { updates["icon_url"] = req.IconURL }
	if req.SortOrder != nil { updates["sort_order"] = *req.SortOrder }
	if req.IsActive != nil { updates["is_active"] = *req.IsActive }

	h.DB.Table("contact_channels").Where("id = ?", id).Updates(updates)
	ok(c, gin.H{"id": id, "updated": true})
}

// DeleteContactChannel ลบช่องทาง
func (h *Handler) DeleteContactChannel(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	h.DB.Exec("DELETE FROM contact_channels WHERE id = ?", id)
	ok(c, gin.H{"id": id, "deleted": true})
}
