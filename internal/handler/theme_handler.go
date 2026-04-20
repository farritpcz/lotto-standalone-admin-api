// Package handler — theme admin handlers.
// Split from stubs.go on 2026-04-20.
package handler

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
)

// =============================================================================
// Settings
// =============================================================================

// =============================================================================
// Agent Theme — ตั้งค่าสีธีม per-agent
//
// GET  /api/v1/agent/theme    → ดึงสีปัจจุบัน
// PUT  /api/v1/agent/theme    → อัพเดทสี + bump theme_version (เคลีย cache หน้าบ้าน)
// =============================================================================

func (h *Handler) GetAgentTheme(c *gin.Context) {
	// ⭐ ดึง scope — ใช้สำหรับ per-node theme ในอนาคต
	// ตอนนี้ standalone มี 1 agent → theme เป็น global
	// node ยังอ่าน theme ของ agent ได้ (ใช้แสดงหน้าเว็บ)
	scope := mw.GetNodeScope(c, h.DB)
	_ = scope // ⭐ reserved สำหรับ per-node theme override ในอนาคต

	type ThemeRow struct {
		ThemePrimaryColor   string `json:"theme_primary_color"`
		ThemeSecondaryColor string `json:"theme_secondary_color"`
		ThemeBGColor        string `json:"theme_bg_color"`
		ThemeAccentColor    string `json:"theme_accent_color"`
		ThemeCardGradient1  string `json:"theme_card_gradient1"`
		ThemeCardGradient2  string `json:"theme_card_gradient2"`
		ThemeNavBG          string `json:"theme_nav_bg"`
		ThemeHeaderBG       string `json:"theme_header_bg"`
		ThemeVersion        int    `json:"theme_version"`
	}
	var theme ThemeRow
	// ⭐ ดึง theme จาก root node (agent_nodes) แทน agents table เดิม
	if err := h.DB.Table("agent_nodes").Where("role = 'admin' AND parent_id IS NULL").First(&theme).Error; err != nil {
		fail(c, 404, "agent not found")
		return
	}
	ok(c, theme)
}

func (h *Handler) UpdateAgentTheme(c *gin.Context) {
	// ⭐ ดึง scope — node ไม่ควรแก้ theme ของระบบกลาง (เฉพาะ admin)
	// ในอนาคตจะรองรับ per-node theme override
	scope := mw.GetNodeScope(c, h.DB)
	if scope.IsNode {
		fail(c, 403, "node ไม่สามารถแก้ไข theme ของระบบได้")
		return
	}

	var req struct {
		PrimaryColor   *string `json:"theme_primary_color"`
		SecondaryColor *string `json:"theme_secondary_color"`
		BGColor        *string `json:"theme_bg_color"`
		AccentColor    *string `json:"theme_accent_color"`
		CardGradient1  *string `json:"theme_card_gradient1"`
		CardGradient2  *string `json:"theme_card_gradient2"`
		NavBG          *string `json:"theme_nav_bg"`
		HeaderBG       *string `json:"theme_header_bg"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	updates := map[string]interface{}{}
	if req.PrimaryColor != nil {
		updates["theme_primary_color"] = *req.PrimaryColor
	}
	if req.SecondaryColor != nil {
		updates["theme_secondary_color"] = *req.SecondaryColor
	}
	if req.BGColor != nil {
		updates["theme_bg_color"] = *req.BGColor
	}
	if req.AccentColor != nil {
		updates["theme_accent_color"] = *req.AccentColor
	}
	if req.CardGradient1 != nil {
		updates["theme_card_gradient1"] = *req.CardGradient1
	}
	if req.CardGradient2 != nil {
		updates["theme_card_gradient2"] = *req.CardGradient2
	}
	if req.NavBG != nil {
		updates["theme_nav_bg"] = *req.NavBG
	}
	if req.HeaderBG != nil {
		updates["theme_header_bg"] = *req.HeaderBG
	}

	if len(updates) == 0 {
		fail(c, 400, "no fields to update")
		return
	}

	// ⭐ Bump theme_version → หน้าบ้านเห็น version ไม่ตรง → refetch สีใหม่
	// ⭐ อัพเดท theme ใน root node (agent_nodes) แทน agents table เดิม
	if err := h.DB.Table("agent_nodes").Where("role = 'admin' AND parent_id IS NULL").
		Updates(updates).
		Update("theme_version", gorm.Expr("theme_version + 1")).Error; err != nil {
		fail(c, 500, err.Error())
		return
	}

	ok(c, gin.H{"message": "theme updated", "fields_updated": len(updates)})
}

// =============================================================================
// Themes — รายการธีมสำเร็จรูป
// =============================================================================

// ListThemes ดึงรายการธีมทั้งหมด (สำหรับ dropdown ตอนสร้าง/แก้เว็บ)
// GET /api/v1/themes
func (h *Handler) ListThemes(c *gin.Context) {
	type themeItem struct {
		ID             int64  `json:"id"`
		Code           string `json:"code"`
		Name           string `json:"name"`
		PreviewURL     string `json:"preview_url"`
		PrimaryColor   string `json:"primary_color"`
		SecondaryColor string `json:"secondary_color"`
		BGColor        string `json:"bg_color"`
		AccentColor    string `json:"accent_color"`
		CardGradient1  string `json:"card_gradient1"`
		CardGradient2  string `json:"card_gradient2"`
		NavBG          string `json:"nav_bg"`
		HeaderBG       string `json:"header_bg"`
	}
	var themes []themeItem
	h.DB.Table("themes").Where("status = 'active'").Order("sort_order ASC, id ASC").Scan(&themes)
	ok(c, themes)
}
