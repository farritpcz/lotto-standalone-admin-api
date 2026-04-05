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

	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
)

type contactChannel struct {
	ID          int64  `json:"id" gorm:"primaryKey"`
	AgentID     int64  `json:"agent_id"`
	AgentNodeID *int64 `json:"agent_node_id" gorm:"index"` // ⭐ NULL=ระบบกลาง (admin), มีค่า=เฉพาะ node
	Platform    string `json:"platform"`
	Name        string `json:"name"`
	Value       string `json:"value"`
	LinkURL     string `json:"link_url"`
	IconURL     string `json:"icon_url"`
	SortOrder   int    `json:"sort_order"`
	IsActive    bool   `json:"is_active"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func (contactChannel) TableName() string { return "contact_channels" }

// ListContactChannels ดูช่องทางติดต่อทั้งหมด (admin — ทุกสถานะ)
// ⭐ Node Scope: node เห็นเฉพาะช่องทางของตัวเอง, admin เห็นของระบบกลาง
func (h *Handler) ListContactChannels(c *gin.Context) {
	// ⭐ ดึง scope — ถ้าเป็น node จะ filter เฉพาะข้อมูลของ node นั้น
	scope := mw.GetNodeScope(c, h.DB)

	var channels []contactChannel
	query := h.DB.Where("agent_id = ?", 1)
	// ⭐ scope ตามสายงาน: node เห็นเฉพาะของตัวเอง, admin เห็นของระบบกลาง
	if scope.IsNode {
		query = query.Where("agent_node_id = ?", scope.NodeID)
	} else {
		query = query.Where("agent_node_id IS NULL")
	}
	query.Order("sort_order ASC, id ASC").Find(&channels)
	ok(c, channels)
}

// ListPublicContactChannels ดูช่องทางติดต่อ (public — เฉพาะ active)
// ⭐ Node Scope: node เห็นเฉพาะช่องทางของตัวเอง, admin เห็นของระบบกลาง
func (h *Handler) ListPublicContactChannels(c *gin.Context) {
	// ⭐ ดึง scope — ถ้าเป็น node จะ filter เฉพาะข้อมูลของ node นั้น
	scope := mw.GetNodeScope(c, h.DB)

	var channels []contactChannel
	query := h.DB.Where("agent_id = ? AND is_active = ?", 1, true)
	// ⭐ scope ตามสายงาน: node เห็นเฉพาะของตัวเอง, admin เห็นของระบบกลาง
	if scope.IsNode {
		query = query.Where("agent_node_id = ?", scope.NodeID)
	} else {
		query = query.Where("agent_node_id IS NULL")
	}
	query.Order("sort_order ASC").Find(&channels)
	ok(c, channels)
}

// CreateContactChannel เพิ่มช่องทางติดต่อ
// ⭐ Node Scope: set agent_node_id ให้ตรงกับ node ที่สร้าง (admin → NULL, node → nodeID)
func (h *Handler) CreateContactChannel(c *gin.Context) {
	// ⭐ ดึง scope — ใช้ SettingNodeID() เพื่อ set agent_node_id ตอน INSERT
	scope := mw.GetNodeScope(c, h.DB)

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
	// ⭐ INSERT พร้อม agent_node_id — admin=NULL, node=nodeID
	h.DB.Exec(`INSERT INTO contact_channels (agent_id, agent_node_id, platform, name, value, link_url, icon_url, sort_order, is_active, created_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, 1, ?)`,
		scope.SettingNodeID(), req.Platform, req.Name, req.Value, req.LinkURL, req.IconURL, req.SortOrder, now)

	ok(c, gin.H{"status": "created", "platform": req.Platform, "name": req.Name})
}

// UpdateContactChannel แก้ไขช่องทาง
// ⭐ Node Scope: node แก้ได้เฉพาะช่องทางของตัวเอง (agent_node_id = nodeID)
func (h *Handler) UpdateContactChannel(c *gin.Context) {
	// ⭐ ดึง scope — ใช้ filter WHERE เพื่อป้องกัน node แก้ข้อมูลของ node อื่น
	scope := mw.GetNodeScope(c, h.DB)

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

	// ⭐ scope ตามสายงาน: node แก้ได้เฉพาะของตัวเอง
	query := h.DB.Table("contact_channels").Where("id = ? AND agent_id = 1", id)
	if scope.IsNode {
		query = query.Where("agent_node_id = ?", scope.NodeID)
	}
	result := query.Updates(updates)
	if result.RowsAffected == 0 {
		fail(c, 404, "ไม่พบช่องทางนี้หรือไม่มีสิทธิ์แก้ไข"); return
	}
	ok(c, gin.H{"id": id, "updated": true})
}

// DeleteContactChannel ลบช่องทาง
// ⭐ Node Scope: node ลบได้เฉพาะช่องทางของตัวเอง (agent_node_id = nodeID)
func (h *Handler) DeleteContactChannel(c *gin.Context) {
	// ⭐ ดึง scope — ป้องกัน node ลบข้อมูลของ node อื่น
	scope := mw.GetNodeScope(c, h.DB)

	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	// ⭐ scope ตามสายงาน: node ลบได้เฉพาะของตัวเอง
	query := "DELETE FROM contact_channels WHERE id = ? AND agent_id = 1"
	args := []interface{}{id}
	if scope.IsNode {
		query += " AND agent_node_id = ?"
		args = append(args, scope.NodeID)
	}
	result := h.DB.Exec(query, args...)
	if result.RowsAffected == 0 {
		fail(c, 404, "ไม่พบช่องทางนี้หรือไม่มีสิทธิ์ลบ"); return
	}
	ok(c, gin.H{"id": id, "deleted": true})
}
