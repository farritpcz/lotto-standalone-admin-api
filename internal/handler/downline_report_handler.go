// Package handler — downline profit reports.
// Split from downline_handler.go on 2026-04-20.
// Rule: docs/rules/downline.md
// Formulas: memory/downline_report_formulas.md
package handler

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

// =============================================================================
func (h *Handler) GetDownlineProfits(c *gin.Context) {
	agentID := int64(1)
	page, perPage := pageParams(c)
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope ตามสายงาน

	// === สรุปกำไรรวมแยกตาม node ===
	type profitSummary struct {
		AgentNodeID  int64   `json:"agent_node_id" gorm:"column:agent_node_id"`
		NodeName     string  `json:"node_name" gorm:"column:node_name"`
		NodeRole     string  `json:"node_role" gorm:"column:node_role"`
		SharePercent float64 `json:"share_percent" gorm:"column:share_percent"`
		TotalProfit  float64 `json:"total_profit" gorm:"column:total_profit"`
		TotalBets    int64   `json:"total_bets" gorm:"column:total_bets"`
	}

	summaryQuery := `
		SELECT
			pt.agent_node_id,
			n.name as node_name,
			n.role as node_role,
			n.share_percent,
			SUM(pt.profit_amount) as total_profit,
			COUNT(pt.id) as total_bets
		FROM agent_profit_transactions pt
		JOIN agent_nodes n ON n.id = pt.agent_node_id
		WHERE pt.agent_id = ?
	`
	args := []interface{}{agentID}

	// ⭐ node user: เห็นเฉพาะ profits ของ nodes ในสายตัวเอง
	if scope.IsNode {
		summaryQuery += " AND pt.agent_node_id IN (?)"
		args = append(args, scope.NodeIDs)
	}

	// Filter by date
	if from := c.Query("date_from"); from != "" {
		summaryQuery += " AND pt.created_at >= ?"
		args = append(args, from)
	}
	if to := c.Query("date_to"); to != "" {
		summaryQuery += " AND pt.created_at < DATE_ADD(?, INTERVAL 1 DAY)"
		args = append(args, to)
	}
	// Filter by node
	if nodeID := c.Query("node_id"); nodeID != "" {
		summaryQuery += " AND pt.agent_node_id = ?"
		args = append(args, nodeID)
	}

	summaryQuery += " GROUP BY pt.agent_node_id, n.name, n.role, n.share_percent ORDER BY total_profit DESC"

	var summaries []profitSummary
	h.DB.Raw(summaryQuery, args...).Scan(&summaries)

	// === Detail transactions (paginated) ===
	var transactions []model.AgentProfitTransaction
	var total int64

	detailQuery := h.DB.Model(&model.AgentProfitTransaction{}).Where("agent_id = ?", agentID)
	if scope.IsNode {
		detailQuery = detailQuery.Where("agent_node_id IN ?", scope.NodeIDs) // ⭐ scope
	}
	if from := c.Query("date_from"); from != "" {
		detailQuery = detailQuery.Where("created_at >= ?", from)
	}
	if to := c.Query("date_to"); to != "" {
		detailQuery = detailQuery.Where("created_at < DATE_ADD(?, INTERVAL 1 DAY)", to)
	}
	if nodeID := c.Query("node_id"); nodeID != "" {
		detailQuery = detailQuery.Where("agent_node_id = ?", nodeID)
	}

	detailQuery.Count(&total)
	detailQuery.Preload("AgentNode").
		Order("created_at DESC").
		Offset((page - 1) * perPage).
		Limit(perPage).
		Find(&transactions)

	// === Grand total ===
	var grandTotal float64
	for _, s := range summaries {
		grandTotal += s.TotalProfit
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"summary":     summaries,
			"grand_total": math.Round(grandTotal*100) / 100,
			"transactions": gin.H{
				"items":    transactions,
				"total":    total,
				"page":     page,
				"per_page": perPage,
			},
		},
	})
}

// =============================================================================
// GET /downline/profits/:nodeId — รายงานกำไรของ node เดียว
//
// Query params:
//   - date_from, date_to
//   - page, per_page
//
// =============================================================================
func (h *Handler) GetNodeProfits(c *gin.Context) {
	nodeID, err := strconv.ParseInt(c.Param("nodeId"), 10, 64)
	if err != nil {
		fail(c, 400, "invalid nodeId")
		return
	}

	page, perPage := pageParams(c)

	// ดึง node info
	var node model.AgentNode
	if err := h.DB.First(&node, nodeID).Error; err != nil {
		fail(c, 404, "ไม่พบ node")
		return
	}

	// ดึง profit transactions
	var transactions []model.AgentProfitTransaction
	var total int64

	query := h.DB.Model(&model.AgentProfitTransaction{}).Where("agent_node_id = ?", nodeID)
	if from := c.Query("date_from"); from != "" {
		query = query.Where("created_at >= ?", from)
	}
	if to := c.Query("date_to"); to != "" {
		query = query.Where("created_at < DATE_ADD(?, INTERVAL 1 DAY)", to)
	}

	query.Count(&total)
	query.Order("created_at DESC").
		Offset((page - 1) * perPage).
		Limit(perPage).
		Find(&transactions)

	// สรุปยอด
	type summary struct {
		TotalProfit float64 `gorm:"column:total_profit"`
		TotalBets   int64   `gorm:"column:total_bets"`
	}
	var sum summary
	sumQuery := h.DB.Model(&model.AgentProfitTransaction{}).Where("agent_node_id = ?", nodeID)
	if from := c.Query("date_from"); from != "" {
		sumQuery = sumQuery.Where("created_at >= ?", from)
	}
	if to := c.Query("date_to"); to != "" {
		sumQuery = sumQuery.Where("created_at < DATE_ADD(?, INTERVAL 1 DAY)", to)
	}
	sumQuery.Select("COALESCE(SUM(profit_amount), 0) as total_profit, COUNT(*) as total_bets").
		Row().Scan(&sum.TotalProfit, &sum.TotalBets)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"node":         node,
			"total_profit": math.Round(sum.TotalProfit*100) / 100,
			"total_bets":   sum.TotalBets,
			"transactions": gin.H{
				"items":    transactions,
				"total":    total,
				"page":     page,
				"per_page": perPage,
			},
		},
	})
}

// =============================================================================
// GET /downline/report — รายงานเคลียสายงาน (v1 re-implementation 2026-04-20)
//
// Rule: memory/downline_report_formulas.md
//
// Response shape (matches admin-web/src/app/downline/report/page.tsx):
//
//	{
//	  my_node:  {id, name, username, role, share_percent},
//	  parent:   {name, share_percent, diff_percent},
//	  is_root:  bool,
//	  direct:   {net_result, my_profit, bets, member_count},
//	  children: [{node_id, name, username, role, share_percent, diff_percent,
//	              tree_net, settlement, bets, member_count}],
//	  summary:  {direct_profit, downline_profit, total_profit,
//	             total_tree_net, parent_settlement},
//	}
//
// Formulas:
//
//	direct_profit     = direct.net_result × my_share% / 100
//	child.settlement  = child.tree_net × (100 - child.share_percent) / 100   (เคลียใต้สาย)
//	downline_profit   = Σ(child.tree_net × diff% / 100)   where diff = my% - child%
//	total_tree_net    = direct.net_result + Σ(children.tree_net)
//	parent_settlement = total_tree_net × (100 - my_share%) / 100   (เคลียหัวสาย)
//	total_profit      = direct_profit + downline_profit
//
// AIDEV-NOTE: Data source = agent_profit_transactions WHERE child_percent = 0
// (leaf records — direct member bets). Avoids double-counting when subtree
// is walked via path LIKE.
//
// Query params: date_from, date_to (YYYY-MM-DD; inclusive-inclusive)
// =============================================================================
func (h *Handler) GetDownlineReport(c *gin.Context) {
	scope := mw.GetNodeScope(c, h.DB)

	// 1. หา my_node — node ตัวเอง หรือ root ของ scope admin
	var myNodeID int64
	if scope.IsNode {
		myNodeID = scope.NodeID
	} else {
		myNodeID = scope.RootNodeID
	}
	if myNodeID == 0 {
		fail(c, 400, "ไม่พบข้อมูล node — scope ไม่ถูกต้อง")
		return
	}
	var myNode model.AgentNode
	if err := h.DB.First(&myNode, myNodeID).Error; err != nil {
		fail(c, 404, "ไม่พบ node")
		return
	}

	// 2. date range (default = today)
	today := time.Now().Format("2006-01-02")
	dateFrom := c.DefaultQuery("date_from", today)
	dateTo := c.DefaultQuery("date_to", today)

	// 3. direct — bets ของ member ที่สังกัด node ตัวเอง (child_percent=0)
	type directRow struct {
		NetResult float64 `gorm:"column:net_result"`
		Bets      int64   `gorm:"column:bets"`
	}
	var direct directRow
	h.DB.Raw(`
		SELECT COALESCE(SUM(net_result),0) AS net_result, COUNT(*) AS bets
		FROM agent_profit_transactions
		WHERE agent_node_id = ? AND child_percent = 0
		  AND created_at >= ? AND created_at < DATE_ADD(?, INTERVAL 1 DAY)
	`, myNode.ID, dateFrom, dateTo).Scan(&direct)

	var directMemberCount int64
	h.DB.Raw(`SELECT COUNT(*) FROM members WHERE agent_node_id = ?`, myNode.ID).Scan(&directMemberCount)

	directProfit := math.Round(direct.NetResult*myNode.SharePercent) / 100

	// 4. children — ลูกสายตรง (parent_id = my.id)
	var childNodes []model.AgentNode
	h.DB.Where("parent_id = ?", myNode.ID).Order("id ASC").Find(&childNodes)

	// 5. สำหรับแต่ละ child: คำนวณ tree_net (ตัวเอง + descendants ทั้งหมด)
	type childReport struct {
		NodeID       int64   `json:"node_id"`
		Name         string  `json:"name"`
		Username     string  `json:"username"`
		Role         string  `json:"role"`
		SharePercent float64 `json:"share_percent"`
		DiffPercent  float64 `json:"diff_percent"`
		TreeNet      float64 `json:"tree_net"`
		Settlement   float64 `json:"settlement"`
		Bets         int64   `json:"bets"`
		MemberCount  int64   `json:"member_count"`
	}
	children := make([]childReport, 0, len(childNodes))
	var totalChildrenTreeNet, downlineProfit float64

	for _, ch := range childNodes {
		// subtree = nodes ที่ path LIKE ch.path%
		type treeAgg struct {
			Net  float64 `gorm:"column:net"`
			Bets int64   `gorm:"column:bets"`
		}
		var agg treeAgg
		h.DB.Raw(`
			SELECT COALESCE(SUM(pt.net_result),0) AS net, COUNT(*) AS bets
			FROM agent_profit_transactions pt
			JOIN agent_nodes n ON n.id = pt.agent_node_id
			WHERE n.path LIKE ? AND pt.child_percent = 0
			  AND pt.created_at >= ? AND pt.created_at < DATE_ADD(?, INTERVAL 1 DAY)
		`, ch.Path+"%", dateFrom, dateTo).Scan(&agg)

		var memberCount int64
		h.DB.Raw(`
			SELECT COUNT(*) FROM members m
			JOIN agent_nodes n ON n.id = m.agent_node_id
			WHERE n.path LIKE ?
		`, ch.Path+"%").Scan(&memberCount)

		diff := myNode.SharePercent - ch.SharePercent
		settlement := math.Round(agg.Net*(100-ch.SharePercent)) / 100
		myShareOfChild := math.Round(agg.Net*diff) / 100

		children = append(children, childReport{
			NodeID:       ch.ID,
			Name:         ch.Name,
			Username:     ch.Username,
			Role:         ch.Role,
			SharePercent: ch.SharePercent,
			DiffPercent:  diff,
			TreeNet:      math.Round(agg.Net*100) / 100,
			Settlement:   settlement,
			Bets:         agg.Bets,
			MemberCount:  memberCount,
		})
		totalChildrenTreeNet += agg.Net
		downlineProfit += myShareOfChild
	}

	// 6. summary
	totalTreeNet := direct.NetResult + totalChildrenTreeNet
	parentSettlement := math.Round(totalTreeNet*(100-myNode.SharePercent)) / 100
	totalProfit := directProfit + downlineProfit

	// 7. parent info
	isRoot := myNode.ParentID == nil
	parentInfo := gin.H{"name": "", "share_percent": 0.0, "diff_percent": 0.0}
	if !isRoot {
		var parent model.AgentNode
		if err := h.DB.First(&parent, *myNode.ParentID).Error; err == nil {
			parentInfo = gin.H{
				"name":          parent.Name,
				"share_percent": parent.SharePercent,
				"diff_percent":  parent.SharePercent - myNode.SharePercent,
			}
		}
	}

	// 8. response
	ok(c, gin.H{
		"my_node": gin.H{
			"id":            myNode.ID,
			"name":          myNode.Name,
			"username":      myNode.Username,
			"role":          myNode.Role,
			"share_percent": myNode.SharePercent,
		},
		"parent":  parentInfo,
		"is_root": isRoot,
		"direct": gin.H{
			"net_result":   math.Round(direct.NetResult*100) / 100,
			"my_profit":    directProfit,
			"bets":         direct.Bets,
			"member_count": directMemberCount,
		},
		"children": children,
		"summary": gin.H{
			"direct_profit":     directProfit,
			"downline_profit":   math.Round(downlineProfit*100) / 100,
			"total_profit":      math.Round(totalProfit*100) / 100,
			"total_tree_net":    math.Round(totalTreeNet*100) / 100,
			"parent_settlement": parentSettlement,
		},
	})
}
