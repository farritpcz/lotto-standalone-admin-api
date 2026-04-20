// Package handler — node_portal_tree_handler.go
// Node Portal: ข้อมูลตัวเอง + tree (ancestors + self + descendants)
//
// รับช่วงจาก node_portal_handler.go (auth) — ดูไฟล์นั้นสำหรับ package comment หลัก
package handler

import (
	"strings"

	"github.com/gin-gonic/gin"

	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

// =============================================================================
// GET /node/me — ดูข้อมูลตัวเอง
// =============================================================================
func (h *Handler) NodeGetMe(c *gin.Context) {
	nodeID := mw.GetNodeID(c)

	var node model.AgentNode
	if err := h.DB.First(&node, nodeID).Error; err != nil {
		fail(c, 404, "ไม่พบข้อมูล node")
		return
	}

	// นับ members + children
	h.DB.Model(&model.Member{}).Where("agent_node_id = ?", nodeID).Count(&node.MemberCount)
	h.DB.Model(&model.AgentNode{}).Where("parent_id = ?", nodeID).Count(&node.ChildCount)

	// ดึง parent info
	if node.ParentID != nil {
		var parent model.AgentNode
		if err := h.DB.First(&parent, *node.ParentID).Error; err == nil {
			node.Parent = &parent
		}
	}

	ok(c, node)
}

// =============================================================================
// GET /node/tree — ดู tree ที่เกี่ยวข้อง
//
// สร้าง tree เฉพาะ:
//   - ancestors: สายบนจากตัวเอง → root (read-only)
//   - self: ตัวเอง
//   - descendants: สายล่างทั้งหมด
//
// Response เพิ่ม field "editable" = true/false ในแต่ละ node
//   - ลูกตรง (parent_id = me) → editable = true
//   - ที่เหลือ → editable = false
//
// =============================================================================
func (h *Handler) NodeGetTree(c *gin.Context) {
	nodeID := mw.GetNodeID(c)
	agentID := mw.GetNodeAgentID(c)

	// ดึง node ปัจจุบัน
	var me model.AgentNode
	if err := h.DB.First(&me, nodeID).Error; err != nil {
		fail(c, 404, "ไม่พบข้อมูล node")
		return
	}

	// ดึง nodes ทั้งหมดของ agent
	var allNodes []model.AgentNode
	h.DB.Where("agent_id = ?", agentID).Order("depth ASC, id ASC").Find(&allNodes)

	// นับ member count + child count
	type countRow struct {
		ID    int64 `gorm:"column:id"`
		Count int64 `gorm:"column:cnt"`
	}
	var memberCounts []countRow
	h.DB.Raw(`SELECT agent_node_id as id, COUNT(*) as cnt FROM members WHERE agent_node_id IS NOT NULL AND agent_id = ? GROUP BY agent_node_id`, agentID).Scan(&memberCounts)
	mcMap := map[int64]int64{}
	for _, mc := range memberCounts {
		mcMap[mc.ID] = mc.Count
	}

	var childCounts []countRow
	h.DB.Raw(`SELECT parent_id as id, COUNT(*) as cnt FROM agent_nodes WHERE agent_id = ? AND parent_id IS NOT NULL GROUP BY parent_id`, agentID).Scan(&childCounts)
	ccMap := map[int64]int64{}
	for _, cc := range childCounts {
		ccMap[cc.ID] = cc.Count
	}

	// สร้าง map id → node
	nodeMap := map[int64]*model.AgentNode{}
	for i := range allNodes {
		allNodes[i].MemberCount = mcMap[allNodes[i].ID]
		allNodes[i].ChildCount = ccMap[allNodes[i].ID]
		nodeMap[allNodes[i].ID] = &allNodes[i]
	}

	// === หา descendants (สายล่าง): ใช้ path LIKE ===
	// เห็นแค่ตัวเอง + สายล่างทั้งหมด (ไม่เห็นสายบน)
	myPath := me.Path
	descendantIDs := map[int64]bool{}
	for _, n := range allNodes {
		if n.ID != nodeID && strings.HasPrefix(n.Path, myPath) {
			descendantIDs[n.ID] = true
		}
	}

	// === filter: ตัวเอง + descendants เท่านั้น ===
	type treeNode struct {
		model.AgentNode
		Editable bool       `json:"editable"` // ⭐ true = แก้ไขได้ (ลูกตรง)
		Children []treeNode `json:"children"`
	}

	type flatNode struct {
		model.AgentNode
		Editable bool `json:"editable"`
	}

	var relatedFlat []flatNode
	for _, n := range allNodes {
		isRelated := n.ID == nodeID || descendantIDs[n.ID]
		if !isRelated {
			continue
		}
		// ⭐ editable = true เฉพาะลูกตรง (parent_id = me)
		editable := n.ParentID != nil && *n.ParentID == nodeID
		relatedFlat = append(relatedFlat, flatNode{AgentNode: n, Editable: editable})
	}

	// Build tree จาก flat list
	flatMap := map[int64]*flatNode{}
	for i := range relatedFlat {
		flatMap[relatedFlat[i].ID] = &relatedFlat[i]
	}

	// Recursive build
	var buildTree func(id int64) treeNode
	buildTree = func(id int64) treeNode {
		fn := flatMap[id]
		tn := treeNode{
			AgentNode: fn.AgentNode,
			Editable:  fn.Editable,
			Children:  []treeNode{},
		}
		for _, n := range relatedFlat {
			if n.ParentID != nil && *n.ParentID == id {
				tn.Children = append(tn.Children, buildTree(n.ID))
			}
		}
		return tn
	}

	// Root = ตัวเอง (ไม่มีสายบน)
	var result []treeNode
	result = append(result, buildTree(nodeID))

	ok(c, gin.H{
		"tree":    result,
		"my_node": me,
	})
}
