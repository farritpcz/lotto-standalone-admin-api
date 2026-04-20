// Package handler — downline commission overrides.
// Split from downline_handler.go on 2026-04-20.
// Rule: docs/rules/downline.md
package handler

import (
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

// =============================================================================
// GET /downline/nodes/:id/commission — ดูตั้งค่า % แยกตามประเภทหวย
// =============================================================================
func (h *Handler) GetNodeCommissionSettings(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, 400, "invalid id")
		return
	}

	// เช็คว่า node มีจริง
	var node model.AgentNode
	if err := h.DB.First(&node, id).Error; err != nil {
		fail(c, 404, "ไม่พบ node")
		return
	}

	var settings []model.AgentNodeCommissionSetting
	h.DB.Where("agent_node_id = ?", id).Order("lottery_type ASC").Find(&settings)

	ok(c, gin.H{
		"node":              node,
		"default_percent":   node.SharePercent,
		"lottery_overrides": settings,
	})
}

// =============================================================================
// PUT /downline/nodes/:id/commission — ตั้ง % แยกตามประเภทหวย (bulk upsert)
//
// Request body:
//   - settings: [{ lottery_type: "YEEKEE_5MIN", share_percent: 88 }, ...]
//
// ถ้า share_percent = null หรือ 0 → ลบ override (ใช้ค่าหลัก)
// ทุก share_percent ต้อง < parent ของ node (สำหรับหวยนั้น)
// =============================================================================
func (h *Handler) UpdateNodeCommissionSettings(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, 400, "invalid id")
		return
	}

	var req struct {
		Settings []struct {
			LotteryType  string  `json:"lottery_type"`
			SharePercent float64 `json:"share_percent"`
		} `json:"settings" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// ดึง node
	var node model.AgentNode
	if err := h.DB.First(&node, id).Error; err != nil {
		fail(c, 404, "ไม่พบ node")
		return
	}

	// ดึง parent สำหรับ validate
	parentPercent := 100.0
	if node.ParentID != nil {
		var parent model.AgentNode
		h.DB.First(&parent, *node.ParentID)
		parentPercent = parent.SharePercent
		// ⭐ TODO: ในอนาคตอาจต้องเช็ค parent commission settings ด้วย
		// ตอนนี้ใช้ parent.SharePercent เป็นค่า ceiling
	}

	now := time.Now()

	for _, s := range req.Settings {
		if s.LotteryType == "" {
			continue
		}

		// ลบ override ถ้า share_percent = 0
		if s.SharePercent <= 0 {
			h.DB.Where("agent_node_id = ? AND lottery_type = ?", id, s.LotteryType).
				Delete(&model.AgentNodeCommissionSetting{})
			continue
		}

		// Validate: ต้อง < parent
		if s.SharePercent >= parentPercent {
			fail(c, 400, fmt.Sprintf("%s: share_percent (%.2f) ต้องน้อยกว่าหัวสาย (%.2f)",
				s.LotteryType, s.SharePercent, parentPercent))
			return
		}

		// Upsert (INSERT ... ON DUPLICATE KEY UPDATE)
		h.DB.Exec(`
			INSERT INTO agent_node_commission_settings (agent_node_id, lottery_type, share_percent, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE share_percent = VALUES(share_percent), updated_at = VALUES(updated_at)
		`, id, s.LotteryType, s.SharePercent, now, now)
	}

	// ดึง settings ล่าสุดส่งกลับ
	var settings []model.AgentNodeCommissionSetting
	h.DB.Where("agent_node_id = ?", id).Order("lottery_type ASC").Find(&settings)

	ok(c, gin.H{
		"node":              node,
		"default_percent":   node.SharePercent,
		"lottery_overrides": settings,
	})
}

// =============================================================================
// GET /downline/profits — รายงานกำไรรวมทุก node
//
// Query params:
//   - date_from, date_to (filter ช่วงวัน)
//   - node_id (filter เฉพาะ node)
//   - page, per_page
//
// Response: สรุปกำไรแยกตาม node + paginated detail
