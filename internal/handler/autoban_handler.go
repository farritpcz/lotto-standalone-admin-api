// Package handler — autoban admin handlers.
// Split from stubs.go on 2026-04-20.
package handler

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

// =============================================================================
// ⭐ Auto-Ban Rules — กฎอั้นเลขอัตโนมัติ
// =============================================================================

// ListAutoBanRules ดูกฎอั้นทั้งหมด (filter by lottery_type_id)
// GET /api/v1/auto-ban-rules?lottery_type_id=1
func (h *Handler) ListAutoBanRules(c *gin.Context) {
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope
	query := h.DB.Model(&model.AutoBanRule{}).Where("status = ?", "active")
	// ⭐ node เห็นเฉพาะกฎของตัวเอง (ไม่เห็นของคนอื่น)
	if scope.IsNode {
		query = query.Where("agent_node_id = ?", scope.NodeID)
	}
	if lt := c.Query("lottery_type_id"); lt != "" {
		query = query.Where("lottery_type_id = ?", lt)
	}
	var rules []model.AutoBanRule
	query.Preload("LotteryType").Order("lottery_type_id, bet_type").Find(&rules)
	ok(c, rules)
}

// CreateAutoBanRule สร้างกฎอั้น 1 กฎ
// POST /api/v1/auto-ban-rules
func (h *Handler) CreateAutoBanRule(c *gin.Context) {
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope
	var rule model.AutoBanRule
	if err := c.ShouldBindJSON(&rule); err != nil {
		fail(c, 400, err.Error())
		return
	}
	rule.Status = "active"
	rule.CreatedAt = time.Now()
	rule.UpdatedAt = time.Now()
	// ⭐ node user: ตั้ง agent_node_id ให้กฎเป็นของ node ตัวเอง
	// AIDEV-NOTE: AutoBanRule.AgentNodeID is *int64 (NULL = ระบบ)
	if scope.IsNode {
		nid := scope.NodeID
		rule.AgentNodeID = &nid
	} else if rule.AgentNodeID == nil || *rule.AgentNodeID == 0 {
		one := int64(1) // default root node
		rule.AgentNodeID = &one
	}
	if err := h.DB.Create(&rule).Error; err != nil {
		fail(c, 500, "failed to create auto-ban rule")
		return
	}
	ok(c, rule)
}

// BulkCreateAutoBanRules สร้างกฎอั้นหลายกฎพร้อมกัน (จากคำนวณอัตโนมัติ)
// POST /api/v1/auto-ban-rules/bulk
// Body: { "rules": [...], "lottery_type_id": 1, "capital": 100000, "max_loss": 20000 }
func (h *Handler) BulkCreateAutoBanRules(c *gin.Context) {
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope
	var req struct {
		LotteryTypeID int64   `json:"lottery_type_id" binding:"required"`
		Capital       float64 `json:"capital"`
		MaxLoss       float64 `json:"max_loss"`
		Rules         []struct {
			BetType         string  `json:"bet_type"`
			ThresholdAmount float64 `json:"threshold_amount"`
			Action          string  `json:"action"`
			Rate            float64 `json:"rate"`
			ReducedRate     float64 `json:"reduced_rate"`
		} `json:"rules" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// ลบกฎเก่าของ lottery type นี้ก่อน (soft delete)
	// ⭐ node user: ลบเฉพาะกฎของ node ตัวเอง (ไม่ลบกฎของระบบ)
	deleteQ := h.DB.Model(&model.AutoBanRule{}).
		Where("lottery_type_id = ? AND status = ?", req.LotteryTypeID, "active")
	if scope.IsNode {
		deleteQ = deleteQ.Where("agent_node_id IN ?", scope.NodeIDs)
	}
	deleteQ.Update("status", "inactive")

	// สร้างกฎใหม่ทั้งหมด
	now := time.Now()
	batchNodeID := int64(1) // default root
	if scope.IsNode {
		batchNodeID = scope.NodeID
	}
	created := make([]model.AutoBanRule, 0, len(req.Rules))
	for _, r := range req.Rules {
		action := r.Action
		if action == "" {
			action = "full_ban"
		}
		bnid := batchNodeID // ⭐ local copy ต่อ iteration (หลีกเลี่ยง pointer aliasing)
		rule := model.AutoBanRule{
			AgentNodeID:     &bnid, // ⭐ root/node ID (pointer)
			LotteryTypeID:   req.LotteryTypeID,
			BetType:         r.BetType,
			ThresholdAmount: r.ThresholdAmount,
			Action:          action,
			ReducedRate:     r.ReducedRate,
			Capital:         req.Capital,
			MaxLoss:         req.MaxLoss,
			Rate:            r.Rate,
			Status:          "active",
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		h.DB.Create(&rule)
		created = append(created, rule)
	}

	ok(c, gin.H{
		"created_count":   len(created),
		"lottery_type_id": req.LotteryTypeID,
		"rules":           created,
	})
}

// UpdateAutoBanRule แก้ไขกฎอั้น
// PUT /api/v1/auto-ban-rules/:id
func (h *Handler) UpdateAutoBanRule(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope
	var rule model.AutoBanRule
	if err := h.DB.First(&rule, id).Error; err != nil {
		fail(c, 404, "rule not found")
		return
	}
	// ⭐ node user: แก้ได้เฉพาะกฎของ node ตัวเอง
	if scope.IsNode && !func() bool {
		if rule.AgentNodeID == nil {
			return false // NULL = กฎของระบบ → node แก้ไม่ได้
		}
		for _, nid := range scope.NodeIDs {
			if nid == *rule.AgentNodeID {
				return true
			}
		}
		return false
	}() {
		fail(c, 403, "ไม่สามารถแก้ไขกฎของระบบได้")
		return
	}
	var req struct {
		ThresholdAmount *float64 `json:"threshold_amount"`
		Action          *string  `json:"action"`
		ReducedRate     *float64 `json:"reduced_rate"`
		BetType         *string  `json:"bet_type"`
		Status          *string  `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	updates := map[string]interface{}{"updated_at": time.Now()}
	if req.ThresholdAmount != nil {
		updates["threshold_amount"] = *req.ThresholdAmount
	}
	if req.Action != nil {
		updates["action"] = *req.Action
	}
	if req.ReducedRate != nil {
		updates["reduced_rate"] = *req.ReducedRate
	}
	if req.BetType != nil {
		updates["bet_type"] = *req.BetType
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}
	h.DB.Model(&rule).Updates(updates)
	h.DB.First(&rule, id)
	ok(c, rule)
}

// DeleteAutoBanRule ลบกฎอั้น (soft delete)
// DELETE /api/v1/auto-ban-rules/:id
func (h *Handler) DeleteAutoBanRule(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope
	// ⭐ node user: ลบได้เฉพาะกฎของ node ตัวเอง
	if scope.IsNode {
		var rule model.AutoBanRule
		h.DB.First(&rule, id)
		if !func() bool {
			if rule.AgentNodeID == nil {
				return false
			}
			for _, nid := range scope.NodeIDs {
				if nid == *rule.AgentNodeID {
					return true
				}
			}
			return false
		}() {
			fail(c, 403, "ไม่สามารถลบกฎของระบบได้")
			return
		}
	}
	h.DB.Model(&model.AutoBanRule{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":     "inactive",
		"updated_at": time.Now(),
	})
	ok(c, gin.H{"id": id, "status": "inactive"})
}
