// Package handler — rates admin handlers.
// Split from stubs.go on 2026-04-20.
package handler

import (
	"sort"
	"strconv"

	"github.com/gin-gonic/gin"

	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

// =============================================================================
// Pay Rates
// =============================================================================

func (h *Handler) ListRates(c *gin.Context) {
	// ⭐ ดึง scope — ใช้หลัก NULL = default, agent_node_id = override
	scope := mw.GetNodeScope(c, h.DB)

	// หา node ID สำหรับ filter override
	var nodeID int64
	if scope.IsNode {
		nodeID = scope.NodeID
	} else {
		nodeID = scope.RootNodeID
	}

	// ⭐ ดึง default (NULL) + override ของเว็บนี้
	// ถ้าเว็บมี override สำหรับ lottery_type + bet_type → แสดง override
	// ถ้าไม่มี → แสดง default (NULL)
	var rates []model.PayRate
	query := h.DB.Preload("BetType").Preload("LotteryType").
		Where("status = ?", "active").
		Where("agent_node_id IS NULL OR agent_node_id = ?", nodeID)
	if lt := c.Query("lottery_type_id"); lt != "" {
		query = query.Where("lottery_type_id = ?", lt)
	}
	query.Find(&rates)

	// ⭐ Merge: ถ้ามี override (agent_node_id != nil) ใช้แทน default
	// สร้าง map key = "lotteryTypeID-betTypeID"
	merged := make(map[string]model.PayRate)
	for _, r := range rates {
		key := strconv.FormatInt(r.LotteryTypeID, 10) + "-" + strconv.FormatInt(r.BetTypeID, 10)
		existing, exists := merged[key]
		if !exists {
			merged[key] = r
		} else {
			// ถ้า r เป็น override (มี agent_node_id) → ใช้แทน default
			if r.AgentNodeID != nil {
				merged[key] = r
			} else if existing.AgentNodeID == nil {
				// ทั้งคู่เป็น default → ใช้ตัวแรก
			}
		}
	}

	// แปลง map กลับเป็น slice แล้วเรียงตาม BetType.SortOrder
	result := make([]model.PayRate, 0, len(merged))
	for _, r := range merged {
		result = append(result, r)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].BetType.SortOrder < result[j].BetType.SortOrder
	})
	ok(c, result)
}

func (h *Handler) UpdateRate(c *gin.Context) {
	// ⭐ ดึง scope — ป้องกัน node แก้ rates ของ node อื่น
	scope := mw.GetNodeScope(c, h.DB)

	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		Rate            *float64 `json:"rate"`
		MaxBetPerNumber *float64 `json:"max_bet_per_number"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	updates := map[string]interface{}{}
	if req.Rate != nil {
		updates["rate"] = *req.Rate
	}
	if req.MaxBetPerNumber != nil {
		updates["max_bet_per_number"] = *req.MaxBetPerNumber
	}
	// ⭐ scope ตามสายงาน: node แก้ได้เฉพาะ rates ของตัวเอง
	query := h.DB.Model(&model.PayRate{}).Where("id = ?", id)
	if scope.IsNode {
		query = query.Where("agent_node_id = ?", scope.NodeID)
	}
	query.Updates(updates)
	ok(c, gin.H{"id": id, "updated": updates})
}
