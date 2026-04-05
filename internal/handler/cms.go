// Package handler — cms.go
// ระบบจัดการเนื้อหาเว็บไซต์ (Content Management System) สำหรับ admin-api (#5)
//
// ⭐ 3 ส่วนหลัก:
// 1. แบนเนอร์ (Banners) — รูปภาพสไลด์หน้าแรก + ลิงก์
// 2. ตัวอักษรวิ่ง (Ticker) — ข้อความ marquee แถบด้านบน
// 3. รูปประเภทหวย (Lottery Images) — จัดการรูปหน้าปกแต่ละประเภทหวย
//
// ความสัมพันธ์:
// - ตาราง cms_banners, settings (key=ticker_text) → share DB กับ member-api (#3)
// - member-web (#4) ดึงข้อมูลแสดงหน้าแรก
// - admin-web (#6) ใช้ CRUD + จัดการ
//
// Routes:
//   GET    /api/v1/cms/banners          → รายการแบนเนอร์
//   POST   /api/v1/cms/banners          → เพิ่มแบนเนอร์
//   PUT    /api/v1/cms/banners/:id      → แก้ไขแบนเนอร์
//   DELETE /api/v1/cms/banners/:id      → ลบแบนเนอร์
//   PUT    /api/v1/cms/banners/reorder  → จัดลำดับแบนเนอร์
//   GET    /api/v1/cms/ticker           → ดึงข้อความ ticker
//   PUT    /api/v1/cms/ticker           → อัพเดทข้อความ ticker
//   PUT    /api/v1/cms/lottery-images/:id → อัพเดทรูป lottery type (ใช้ endpoint เดิม)
package handler

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
)

// =============================================================================
// Inline Model — cmsBanner
// =============================================================================

// cmsBanner แบนเนอร์สไลด์หน้าแรก
// แต่ละ banner มี: รูป, ลิงก์คลิก, ลำดับแสดง, สถานะ
type cmsBanner struct {
	ID          int64     `json:"id" gorm:"primaryKey"`
	AgentID     int64     `json:"agent_id" gorm:"not null;default:1;index"`
	AgentNodeID *int64    `json:"agent_node_id" gorm:"index"`                    // ⭐ NULL=ระบบกลาง (admin), มีค่า=เฉพาะ node
	Title       string    `json:"title" gorm:"size:200"`                          // ชื่อแบนเนอร์ (internal)
	ImageURL    string    `json:"image_url" gorm:"size:500;not null"`             // URL รูปภาพ
	LinkURL     string    `json:"link_url" gorm:"size:500"`                       // ลิงก์เมื่อคลิก
	SortOrder   int       `json:"sort_order" gorm:"not null;default:0"`           // ลำดับแสดง (น้อย = แสดงก่อน)
	Status      string    `json:"status" gorm:"size:20;not null;default:active"`  // active/inactive
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (cmsBanner) TableName() string { return "cms_banners" }

// =============================================================================
// Banners — CRUD
// =============================================================================

// ListBanners — GET /api/v1/cms/banners
// ดึงแบนเนอร์ทั้งหมด เรียงตาม sort_order
// ⭐ Node Scope: node เห็นเฉพาะแบนเนอร์ของตัวเอง, admin เห็นของระบบกลาง
func (h *Handler) ListBanners(c *gin.Context) {
	// ⭐ ดึง scope — ถ้าเป็น node จะ filter เฉพาะข้อมูลของ node นั้น
	scope := mw.GetNodeScope(c, h.DB)

	var banners []cmsBanner
	query := h.DB.Where("agent_id = ?", 1)
	// ⭐ scope ตามสายงาน: node เห็นเฉพาะของตัวเอง, admin เห็นของระบบกลาง
	if scope.IsNode {
		query = query.Where("agent_node_id = ?", scope.NodeID)
	} else {
		query = query.Where("agent_node_id IS NULL")
	}
	query.Order("sort_order ASC, id ASC").Find(&banners)
	ok(c, banners)
}

// CreateBanner — POST /api/v1/cms/banners
// เพิ่มแบนเนอร์ใหม่
// ⭐ Node Scope: set agent_node_id ให้ตรงกับ node ที่สร้าง (admin → NULL, node → nodeID)
func (h *Handler) CreateBanner(c *gin.Context) {
	// ⭐ ดึง scope — ใช้ SettingNodeID() เพื่อ set agent_node_id ตอน INSERT
	scope := mw.GetNodeScope(c, h.DB)

	var req struct {
		Title    string `json:"title"`
		ImageURL string `json:"image_url" binding:"required"` // ⭐ ต้องมี URL รูป
		LinkURL  string `json:"link_url"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// ⭐ หา sort_order สูงสุด + 1 (ให้ banner ใหม่อยู่ท้ายสุด)
	var maxOrder int
	h.DB.Table("cms_banners").Where("agent_id = 1").Select("COALESCE(MAX(sort_order), 0)").Row().Scan(&maxOrder)

	banner := cmsBanner{
		AgentID:     1,
		AgentNodeID: scope.SettingNodeID(), // ⭐ admin=nil (ระบบกลาง), node=&nodeID (เฉพาะ node)
		Title:       req.Title,
		ImageURL:    req.ImageURL,
		LinkURL:     req.LinkURL,
		SortOrder:   maxOrder + 1,
		Status:      "active",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := h.DB.Create(&banner).Error; err != nil {
		fail(c, 500, "สร้างแบนเนอร์ไม่สำเร็จ: "+err.Error())
		return
	}

	ok(c, banner)
}

// UpdateBanner — PUT /api/v1/cms/banners/:id
// แก้ไขแบนเนอร์ (partial update)
// ⭐ Node Scope: node แก้ได้เฉพาะแบนเนอร์ของตัวเอง (agent_node_id = nodeID)
func (h *Handler) UpdateBanner(c *gin.Context) {
	// ⭐ ดึง scope — ใช้ filter WHERE เพื่อป้องกัน node แก้ข้อมูลของ node อื่น
	scope := mw.GetNodeScope(c, h.DB)

	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	var req struct {
		Title    *string `json:"title"`
		ImageURL *string `json:"image_url"`
		LinkURL  *string `json:"link_url"`
		Status   *string `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	updates := map[string]interface{}{}
	if req.Title != nil {
		updates["title"] = *req.Title
	}
	if req.ImageURL != nil {
		updates["image_url"] = *req.ImageURL
	}
	if req.LinkURL != nil {
		updates["link_url"] = *req.LinkURL
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}

	if len(updates) == 0 {
		fail(c, 400, "ไม่มีข้อมูลให้อัพเดท")
		return
	}
	updates["updated_at"] = time.Now()

	// ⭐ scope ตามสายงาน: node แก้ได้เฉพาะแบนเนอร์ของตัวเอง
	query := h.DB.Table("cms_banners").Where("id = ? AND agent_id = 1", id)
	if scope.IsNode {
		query = query.Where("agent_node_id = ?", scope.NodeID)
	}
	result := query.Updates(updates)
	if result.RowsAffected == 0 {
		fail(c, 404, "ไม่พบแบนเนอร์นี้หรือไม่มีสิทธิ์แก้ไข")
		return
	}

	var banner cmsBanner
	h.DB.First(&banner, id)
	ok(c, banner)
}

// DeleteBanner — DELETE /api/v1/cms/banners/:id
// ลบแบนเนอร์ถาวร
// ⭐ Node Scope: node ลบได้เฉพาะแบนเนอร์ของตัวเอง (agent_node_id = nodeID)
func (h *Handler) DeleteBanner(c *gin.Context) {
	// ⭐ ดึง scope — ป้องกัน node ลบข้อมูลของ node อื่น
	scope := mw.GetNodeScope(c, h.DB)

	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	// ⭐ scope ตามสายงาน: node ลบได้เฉพาะของตัวเอง
	query := "DELETE FROM cms_banners WHERE id = ? AND agent_id = 1"
	args := []interface{}{id}
	if scope.IsNode {
		query += " AND agent_node_id = ?"
		args = append(args, scope.NodeID)
	}
	result := h.DB.Exec(query, args...)
	if result.RowsAffected == 0 {
		fail(c, 404, "ไม่พบแบนเนอร์นี้หรือไม่มีสิทธิ์ลบ")
		return
	}

	ok(c, gin.H{"id": id, "deleted": true})
}

// ReorderBanners — PUT /api/v1/cms/banners/reorder
// จัดลำดับแบนเนอร์ (drag & drop)
// ⭐ Node Scope: node จัดลำดับได้เฉพาะแบนเนอร์ของตัวเอง
func (h *Handler) ReorderBanners(c *gin.Context) {
	// ⭐ ดึง scope — ป้องกัน node จัดลำดับข้อมูลของ node อื่น
	scope := mw.GetNodeScope(c, h.DB)

	var req struct {
		Orders []struct {
			ID        int64 `json:"id" binding:"required"`
			SortOrder int   `json:"sort_order"`
		} `json:"orders" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	tx := h.DB.Begin()
	for _, o := range req.Orders {
		// ⭐ scope ตามสายงาน: node อัพเดทได้เฉพาะของตัวเอง
		if scope.IsNode {
			tx.Exec("UPDATE cms_banners SET sort_order = ? WHERE id = ? AND agent_id = 1 AND agent_node_id = ?", o.SortOrder, o.ID, scope.NodeID)
		} else {
			tx.Exec("UPDATE cms_banners SET sort_order = ? WHERE id = ? AND agent_id = 1", o.SortOrder, o.ID)
		}
	}
	tx.Commit()

	ok(c, gin.H{"updated": len(req.Orders)})
}

// =============================================================================
// Ticker — ข้อความวิ่ง (marquee)
// ⭐ เก็บใน settings table (key = "ticker_text")
// =============================================================================

// GetTicker — GET /api/v1/cms/ticker
// ดึงข้อความ ticker ปัจจุบัน
// ⭐ Node Scope: node ใช้ key "ticker_text_node_{nodeID}", admin ใช้ key "ticker_text"
func (h *Handler) GetTicker(c *gin.Context) {
	// ⭐ ดึง scope — node มี ticker แยกต่างหาก
	scope := mw.GetNodeScope(c, h.DB)

	settingKey := "ticker_text"
	if scope.IsNode {
		settingKey = "ticker_text_node_" + strconv.FormatInt(scope.NodeID, 10)
	}

	var text string
	h.DB.Table("settings").Select("value").Where("`key` = ?", settingKey).Row().Scan(&text)
	ok(c, gin.H{"ticker_text": text})
}

// UpdateTicker — PUT /api/v1/cms/ticker
// อัพเดทข้อความ ticker
// ⭐ Node Scope: node ใช้ key "ticker_text_node_{nodeID}", admin ใช้ key "ticker_text"
func (h *Handler) UpdateTicker(c *gin.Context) {
	// ⭐ ดึง scope — node มี ticker แยกต่างหาก
	scope := mw.GetNodeScope(c, h.DB)

	var req struct {
		TickerText string `json:"ticker_text"` // empty string = ปิด ticker
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// ⭐ เลือก key ตาม scope: admin → "ticker_text", node → "ticker_text_node_{nodeID}"
	settingKey := "ticker_text"
	description := "ข้อความวิ่ง (marquee) หน้าแรก"
	if scope.IsNode {
		settingKey = "ticker_text_node_" + strconv.FormatInt(scope.NodeID, 10)
		description = "ข้อความวิ่ง (marquee) หน้าแรก — node " + strconv.FormatInt(scope.NodeID, 10)
	}

	// ⭐ upsert — ถ้ามี key อยู่แล้วให้ update, ไม่มีให้ insert
	result := h.DB.Exec(
		"INSERT INTO settings (`key`, value, description, updated_at) VALUES (?, ?, ?, ?) ON DUPLICATE KEY UPDATE value = VALUES(value), updated_at = VALUES(updated_at)",
		settingKey, req.TickerText, description, time.Now(),
	)
	if result.Error != nil {
		fail(c, 500, "อัพเดท ticker ไม่สำเร็จ")
		return
	}

	ok(c, gin.H{"ticker_text": req.TickerText})
}
