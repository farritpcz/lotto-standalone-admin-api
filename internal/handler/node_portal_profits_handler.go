// Package handler — node_portal_profits_handler.go
// Node Portal: กำไร/ขาดทุนของตัวเอง + สายล่าง (list + aggregate)
//
// Query params: date_from, date_to, page, per_page
// รับช่วงจาก node_portal_handler.go (auth) — ดูไฟล์นั้นสำหรับ package comment หลัก
package handler

import (
	"math"
	"net/http"

	"github.com/gin-gonic/gin"

	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

// =============================================================================
// GET /node/profits — กำไร/ขาดทุนของตัวเอง + สายล่าง
// =============================================================================
func (h *Handler) NodeGetProfits(c *gin.Context) {
	nodeID := mw.GetNodeID(c)
	page, perPage := pageParams(c)

	// ดึง me
	var me model.AgentNode
	h.DB.First(&me, nodeID)

	// หา descendant node IDs (ทุก node ที่ path ขึ้นต้นด้วย path ของเรา)
	var descendantIDs []int64
	h.DB.Model(&model.AgentNode{}).
		Where("path LIKE ? AND id != ?", me.Path+"%", nodeID).
		Pluck("id", &descendantIDs)

	// รวม node IDs = ตัวเอง + descendants
	allNodeIDs := append([]int64{nodeID}, descendantIDs...)

	// ดึง profit transactions
	query := h.DB.Model(&model.AgentProfitTransaction{}).Where("agent_node_id IN ?", allNodeIDs)
	if from := c.Query("date_from"); from != "" {
		query = query.Where("created_at >= ?", from)
	}
	if to := c.Query("date_to"); to != "" {
		query = query.Where("created_at < DATE_ADD(?, INTERVAL 1 DAY)", to)
	}

	var total int64
	query.Count(&total)

	var transactions []model.AgentProfitTransaction
	query.Preload("AgentNode").
		Order("created_at DESC").
		Offset((page - 1) * perPage).
		Limit(perPage).
		Find(&transactions)

	// สรุปยอด
	var totalProfit float64
	var totalBets int64
	sumQuery := h.DB.Model(&model.AgentProfitTransaction{}).Where("agent_node_id IN ?", allNodeIDs)
	if from := c.Query("date_from"); from != "" {
		sumQuery = sumQuery.Where("created_at >= ?", from)
	}
	if to := c.Query("date_to"); to != "" {
		sumQuery = sumQuery.Where("created_at < DATE_ADD(?, INTERVAL 1 DAY)", to)
	}
	sumQuery.Select("COALESCE(SUM(profit_amount), 0)").Row().Scan(&totalProfit)
	sumQuery.Count(&totalBets)

	// สรุปกำไรเฉพาะตัวเอง
	var myProfit float64
	myQuery := h.DB.Model(&model.AgentProfitTransaction{}).Where("agent_node_id = ?", nodeID)
	if from := c.Query("date_from"); from != "" {
		myQuery = myQuery.Where("created_at >= ?", from)
	}
	if to := c.Query("date_to"); to != "" {
		myQuery = myQuery.Where("created_at < DATE_ADD(?, INTERVAL 1 DAY)", to)
	}
	myQuery.Select("COALESCE(SUM(profit_amount), 0)").Row().Scan(&myProfit)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"my_node":      me,
			"my_profit":    math.Round(myProfit*100) / 100,
			"total_profit": math.Round(totalProfit*100) / 100,
			"total_bets":   totalBets,
			"transactions": gin.H{
				"items":    transactions,
				"total":    total,
				"page":     page,
				"per_page": perPage,
			},
		},
	})
}
