// Package handler — downline_handler.go
// จัดการ CRUD + รายงานกำไรสำหรับระบบปล่อยสาย (Agent Downline)
//
// ความสัมพันธ์:
//   - ใช้ model.AgentNode, AgentNodeCommissionSetting, AgentProfitTransaction
//   - share DB กับ member-api (#3) ตาราง agent_nodes, agent_node_commission_settings, agent_profit_transactions
//   - frontend: admin-web (#6) หน้า /downline
//
// Endpoints:
//
//	GET    /downline/tree            → ดึง tree ทั้งหมด
//	GET    /downline/nodes           → ดึง nodes (flat, paginated)
//	GET    /downline/nodes/:id       → ดึง node detail + children
//	POST   /downline/nodes           → สร้าง node ใหม่
//	PUT    /downline/nodes/:id       → แก้ไข node
//	DELETE /downline/nodes/:id       → ลบ node (ต้องไม่มีลูก)
//	GET    /downline/nodes/:id/commission  → ดูตั้งค่า % แยกหวย
//	PUT    /downline/nodes/:id/commission  → ตั้ง % แยกหวย
//	GET    /downline/profits         → รายงานกำไรรวม
//	GET    /downline/profits/:nodeId → รายงานกำไรของ node
//	GET    /downline/report           → รายงานเคลียสายงาน (เว็บตัวเอง + ใต้สาย + สรุป)
package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

// =============================================================================
// GET /downline/tree — ดึง tree ทั้งหมด (hierarchical)
//
// Response: array ของ root nodes พร้อม children ซ้อนลงไป
// ใช้สำหรับ: แสดง tree view ในหน้า admin
// Query params:
//   - agent_id (optional, default=1)
//
// =============================================================================
func (h *Handler) GetDownlineTree(c *gin.Context) {
	agentID := int64(1)
	if v := c.Query("agent_id"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
			agentID = parsed
		}
	}
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope ตามสายงาน

	// ดึง nodes ของ agent (ถ้าเป็น node user → เฉพาะสายของตัวเอง)
	var nodes []model.AgentNode
	query := h.DB.Where("agent_id = ?", agentID)
	if scope.IsNode {
		// ⭐ node user: เห็นเฉพาะ nodes ในสาย (ตัวเอง + descendants)
		query = query.Where("id IN ?", scope.NodeIDs)
	}
	if err := query.Order("depth ASC, id ASC").
		Find(&nodes).Error; err != nil {
		fail(c, 500, "ดึงข้อมูลสายงานไม่สำเร็จ")
		return
	}

	// นับ member count สำหรับแต่ละ node
	type nodeCount struct {
		NodeID int64 `gorm:"column:agent_node_id"`
		Count  int64 `gorm:"column:cnt"`
	}
	var memberCounts []nodeCount
	h.DB.Raw(`
		SELECT agent_node_id, COUNT(*) as cnt
		FROM members
		WHERE agent_node_id IS NOT NULL AND agent_id = ?
		GROUP BY agent_node_id
	`, agentID).Scan(&memberCounts)

	memberCountMap := map[int64]int64{}
	for _, mc := range memberCounts {
		memberCountMap[mc.NodeID] = mc.Count
	}

	// นับ child count สำหรับแต่ละ node
	type childCountRow struct {
		ParentID int64 `gorm:"column:parent_id"`
		Count    int64 `gorm:"column:cnt"`
	}
	var childCounts []childCountRow
	h.DB.Raw(`
		SELECT parent_id, COUNT(*) as cnt
		FROM agent_nodes
		WHERE agent_id = ? AND parent_id IS NOT NULL
		GROUP BY parent_id
	`, agentID).Scan(&childCounts)

	childCountMap := map[int64]int64{}
	for _, cc := range childCounts {
		childCountMap[cc.ParentID] = cc.Count
	}

	// สร้าง map id → node (เพื่อ build tree)
	nodeMap := map[int64]*model.AgentNode{}
	for i := range nodes {
		nodes[i].MemberCount = memberCountMap[nodes[i].ID]
		nodes[i].ChildCount = childCountMap[nodes[i].ID]
		nodes[i].Children = []model.AgentNode{} // init เป็น empty array (ไม่ใช่ nil)
		nodeMap[nodes[i].ID] = &nodes[i]
	}

	// Build tree — หา roots (nodes ที่ parent ไม่อยู่ใน set)
	// ⭐ สำหรับ node user: root = ตัวเอง (parent อยู่นอก scope)
	var roots []model.AgentNode
	for i := range nodes {
		if nodes[i].ParentID == nil {
			roots = append(roots, nodes[i])
		} else if _, parentInSet := nodeMap[*nodes[i].ParentID]; !parentInSet {
			// parent ไม่อยู่ใน set → ถือว่าเป็น root ของ scope นี้
			roots = append(roots, nodes[i])
		}
	}

	// ⭐ ต้อง rebuild tree จาก roots เพราะ Children ข้างบนยังเป็น shallow copy
	// ใช้ recursive function เพื่อ deep build
	var buildTree func(nodeID int64) model.AgentNode
	buildTree = func(nodeID int64) model.AgentNode {
		n := *nodeMap[nodeID]
		n.Children = []model.AgentNode{}
		for i := range nodes {
			if nodes[i].ParentID != nil && *nodes[i].ParentID == nodeID {
				child := buildTree(nodes[i].ID)
				n.Children = append(n.Children, child)
			}
		}
		return n
	}

	result := []model.AgentNode{}
	for _, root := range roots {
		result = append(result, buildTree(root.ID))
	}

	ok(c, result)
}

// =============================================================================
// GET /downline/nodes — ดึง nodes แบบ flat (paginated)
//
// Query params:
//   - page, per_page (pagination)
//   - q (search name/username)
//   - role (filter by role)
//   - parent_id (filter by parent)
//   - status (filter: active/suspended)
//
// =============================================================================
func (h *Handler) ListDownlineNodes(c *gin.Context) {
	page, perPage := pageParams(c)
	agentID := int64(1)
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope

	var nodes []model.AgentNode
	var total int64

	query := h.DB.Model(&model.AgentNode{}).Where("agent_id = ?", agentID)
	if scope.IsNode {
		query = query.Where("id IN ?", scope.NodeIDs) // ⭐ node เห็นเฉพาะสายตัวเอง
	}

	// Filter: search
	if q := c.Query("q"); q != "" {
		query = query.Where("name LIKE ? OR username LIKE ?", "%"+q+"%", "%"+q+"%")
	}
	// Filter: role
	if role := c.Query("role"); role != "" {
		query = query.Where("role = ?", role)
	}
	// Filter: parent_id
	if pid := c.Query("parent_id"); pid != "" {
		if parsed, err := strconv.ParseInt(pid, 10, 64); err == nil {
			query = query.Where("parent_id = ?", parsed)
		}
	}
	// Filter: status
	if status := c.Query("status"); status != "" {
		query = query.Where("status = ?", status)
	}

	query.Count(&total)
	query.Order("depth ASC, id ASC").
		Offset((page - 1) * perPage).
		Limit(perPage).
		Find(&nodes)

	paginated(c, nodes, total, page, perPage)
}

// =============================================================================
// GET /downline/nodes/:id — ดึง node detail + children ชั้นเดียว
// =============================================================================
func (h *Handler) GetDownlineNode(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, 400, "invalid id")
		return
	}
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope
	// ⭐ node user: ดูได้เฉพาะ nodes ในสายตัวเอง
	if scope.IsNode {
		found := false
		for _, nid := range scope.NodeIDs {
			if nid == id {
				found = true
				break
			}
		}
		if !found {
			fail(c, 403, "ไม่มีสิทธิ์")
			return
		}
	}

	var node model.AgentNode
	if err := h.DB.First(&node, id).Error; err != nil {
		fail(c, 404, "ไม่พบ node")
		return
	}

	// ดึง children ชั้นเดียว
	var children []model.AgentNode
	h.DB.Where("parent_id = ?", id).Order("id ASC").Find(&children)
	node.Children = children

	// นับ members
	h.DB.Model(&model.Member{}).Where("agent_node_id = ?", id).Count(&node.MemberCount)

	// นับ children
	node.ChildCount = int64(len(children))

	// ดึง parent (ถ้ามี)
	if node.ParentID != nil {
		var parent model.AgentNode
		if err := h.DB.First(&parent, *node.ParentID).Error; err == nil {
			node.Parent = &parent
		}
	}

	ok(c, node)
}
