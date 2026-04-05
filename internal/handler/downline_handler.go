// Package handler — downline_handler.go
// จัดการ CRUD + รายงานกำไรสำหรับระบบปล่อยสาย (Agent Downline)
//
// ความสัมพันธ์:
//   - ใช้ model.AgentNode, AgentNodeCommissionSetting, AgentProfitTransaction
//   - share DB กับ member-api (#3) ตาราง agent_nodes, agent_node_commission_settings, agent_profit_transactions
//   - frontend: admin-web (#6) หน้า /downline
//
// Endpoints:
//   GET    /downline/tree            → ดึง tree ทั้งหมด
//   GET    /downline/nodes           → ดึง nodes (flat, paginated)
//   GET    /downline/nodes/:id       → ดึง node detail + children
//   POST   /downline/nodes           → สร้าง node ใหม่
//   PUT    /downline/nodes/:id       → แก้ไข node
//   DELETE /downline/nodes/:id       → ลบ node (ต้องไม่มีลูก)
//   GET    /downline/nodes/:id/commission  → ดูตั้งค่า % แยกหวย
//   PUT    /downline/nodes/:id/commission  → ตั้ง % แยกหวย
//   GET    /downline/profits         → รายงานกำไรรวม
//   GET    /downline/profits/:nodeId → รายงานกำไรของ node
package handler

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

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
		for _, nid := range scope.NodeIDs { if nid == id { found = true; break } }
		if !found { fail(c, 403, "ไม่มีสิทธิ์"); return }
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

// =============================================================================
// POST /downline/nodes — สร้าง node ใหม่ใต้ parent
//
// Request body:
//   - parent_id (required) — หัวสาย (หรือ null สำหรับ root)
//   - name (required)
//   - username (required)
//   - password (required)
//   - share_percent (required) — ต้อง < parent.share_percent
//   - role (optional) — ถ้าไม่ส่ง จะ auto จาก NextRole(parent.role)
//   - phone, line_id, note (optional)
//
// Business Rules:
//   1. share_percent < parent.share_percent
//   2. role ถูกต้องตามลำดับ
//   3. username ไม่ซ้ำในเดียวกัน agent
// =============================================================================
func (h *Handler) CreateDownlineNode(c *gin.Context) {
	var req struct {
		ParentID     *int64  `json:"parent_id"`                          // nil = root (admin)
		Name         string  `json:"name" binding:"required"`
		Username     string  `json:"username" binding:"required"`
		Password     string  `json:"password" binding:"required"`
		SharePercent float64 `json:"share_percent" binding:"required"`
		Role         string  `json:"role"`                                // optional: auto จาก parent
		Phone        string  `json:"phone"`
		LineID       string  `json:"line_id"`
		Note         string  `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "ข้อมูลไม่ถูกต้อง: "+err.Error())
		return
	}

	agentID := int64(1)

	// === Validate parent ===
	var parentNode *model.AgentNode
	parentPath := "/"
	parentDepth := -1 // root จะเป็น depth=0
	parentPercent := 100.0
	parentRole := ""

	if req.ParentID != nil {
		// มี parent → ดึง parent node
		var parent model.AgentNode
		if err := h.DB.Where("id = ? AND agent_id = ?", *req.ParentID, agentID).
			First(&parent).Error; err != nil {
			fail(c, 404, "ไม่พบหัวสาย (parent)")
			return
		}
		parentNode = &parent
		parentPath = parent.Path // path ของ parent มี ID ตัวเองอยู่แล้ว เช่น /1/2/4/6/9/
		parentDepth = parent.Depth
		parentPercent = parent.SharePercent
		parentRole = parent.Role
	}

	// === Validate share_percent: ลูกต้อง < พ่อ ===
	if req.SharePercent >= parentPercent {
		fail(c, 400, fmt.Sprintf("share_percent (%.2f) ต้องน้อยกว่าหัวสาย (%.2f)", req.SharePercent, parentPercent))
		return
	}
	if req.SharePercent <= 0 {
		fail(c, 400, "share_percent ต้องมากกว่า 0")
		return
	}

	// === Determine role ===
	role := req.Role
	if role == "" {
		if parentNode == nil {
			role = "admin" // root node
		} else {
			role = model.NextRole(parentRole)
		}
	}
	// Validate role ตามลำดับ
	if parentNode != nil {
		parentIdx := model.RoleHierarchy[parentRole]
		childIdx, validRole := model.RoleHierarchy[role]
		if !validRole {
			fail(c, 400, "role ไม่ถูกต้อง")
			return
		}
		// ⭐ agent_downline สามารถซ้อนได้ไม่จำกัด (child = agent_downline, parent = agent_downline → OK)
		if childIdx < parentIdx || (childIdx == parentIdx && role != "agent_downline") {
			fail(c, 400, fmt.Sprintf("role '%s' ต้องต่ำกว่า '%s'", role, parentRole))
			return
		}
	}

	// === Hash password ===
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		fail(c, 500, "hash password ไม่สำเร็จ")
		return
	}

	// === Build path ===
	depth := parentDepth + 1
	path := parentPath // จะ append ID หลัง create

	// === Create node ===
	node := model.AgentNode{
		AgentID:      agentID,
		ParentID:     req.ParentID,
		Role:         role,
		Name:         req.Name,
		Username:     req.Username,
		PasswordHash: string(hashedPassword),
		Depth:        depth,
		Path:         path, // temporary — อัพเดทหลัง create
		SharePercent: req.SharePercent,
		Phone:        req.Phone,
		LineID:       req.LineID,
		Note:         req.Note,
		Status:       "active",
	}

	if err := h.DB.Create(&node).Error; err != nil {
		// เช็ค duplicate username
		if strings.Contains(err.Error(), "uk_agent_node_username") || strings.Contains(err.Error(), "Duplicate") {
			fail(c, 400, "username ซ้ำ — กรุณาเปลี่ยน username")
			return
		}
		fail(c, 500, "สร้าง node ไม่สำเร็จ: "+err.Error())
		return
	}

	// === อัพเดท path ให้ถูกต้อง (ต้องรู้ ID ก่อน) ===
	if parentNode != nil {
		node.Path = parentPath + fmt.Sprintf("%d/", node.ID)
	} else {
		node.Path = fmt.Sprintf("/%d/", node.ID)
	}
	h.DB.Model(&node).Update("path", node.Path)

	ok(c, node)
}

// =============================================================================
// PUT /downline/nodes/:id — แก้ไข node (partial update)
//
// แก้ได้: name, share_percent, phone, line_id, note, status
// แก้ไม่ได้: role, parent_id, username (เพื่อป้องกันเสียหาย)
//
// Business Rules:
//   - share_percent ใหม่ต้อง < parent.share_percent
//   - share_percent ใหม่ต้อง > ลูกทุกคน.share_percent
// =============================================================================
func (h *Handler) UpdateDownlineNode(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, 400, "invalid id")
		return
	}

	var req struct {
		Name         *string  `json:"name"`
		SharePercent *float64 `json:"share_percent"`
		Phone        *string  `json:"phone"`
		LineID       *string  `json:"line_id"`
		Note         *string  `json:"note"`
		Status       *string  `json:"status"`
		Password     *string  `json:"password"` // optional: เปลี่ยน password
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// ดึง node ปัจจุบัน
	var node model.AgentNode
	if err := h.DB.First(&node, id).Error; err != nil {
		fail(c, 404, "ไม่พบ node")
		return
	}

	// === Validate share_percent ===
	if req.SharePercent != nil {
		newPercent := *req.SharePercent

		// ต้อง > 0
		if newPercent <= 0 {
			fail(c, 400, "share_percent ต้องมากกว่า 0")
			return
		}

		// ต้อง < parent
		if node.ParentID != nil {
			var parent model.AgentNode
			h.DB.First(&parent, *node.ParentID)
			if newPercent >= parent.SharePercent {
				fail(c, 400, fmt.Sprintf("share_percent (%.2f) ต้องน้อยกว่าหัวสาย (%.2f)", newPercent, parent.SharePercent))
				return
			}
		}

		// ต้อง > ลูกทุกคน
		var maxChildPercent float64
		h.DB.Model(&model.AgentNode{}).
			Where("parent_id = ?", id).
			Select("COALESCE(MAX(share_percent), 0)").
			Row().Scan(&maxChildPercent)
		if newPercent <= maxChildPercent {
			fail(c, 400, fmt.Sprintf("share_percent (%.2f) ต้องมากกว่าลูกสูงสุด (%.2f)", newPercent, maxChildPercent))
			return
		}
	}

	// === Build updates map ===
	updates := map[string]interface{}{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.SharePercent != nil {
		updates["share_percent"] = *req.SharePercent
	}
	if req.Phone != nil {
		updates["phone"] = *req.Phone
	}
	if req.LineID != nil {
		updates["line_id"] = *req.LineID
	}
	if req.Note != nil {
		updates["note"] = *req.Note
	}
	if req.Status != nil {
		if *req.Status != "active" && *req.Status != "suspended" {
			fail(c, 400, "status ต้องเป็น active หรือ suspended")
			return
		}
		updates["status"] = *req.Status
	}
	if req.Password != nil && *req.Password != "" {
		hashed, err := bcrypt.GenerateFromPassword([]byte(*req.Password), bcrypt.DefaultCost)
		if err != nil {
			fail(c, 500, "hash password ไม่สำเร็จ")
			return
		}
		updates["password_hash"] = string(hashed)
	}

	if len(updates) == 0 {
		fail(c, 400, "ไม่มีข้อมูลที่ต้องอัพเดท")
		return
	}

	updates["updated_at"] = time.Now()
	h.DB.Model(&model.AgentNode{}).Where("id = ?", id).Updates(updates)

	// ดึง node ล่าสุดส่งกลับ
	h.DB.First(&node, id)
	ok(c, node)
}

// =============================================================================
// DELETE /downline/nodes/:id — ลบ node
//
// Business Rules:
//   - ต้องไม่มี children
//   - ต้องไม่มี members (agent_node_id ชี้มาที่ node นี้)
//   - ห้ามลบ root node (admin)
// =============================================================================
func (h *Handler) DeleteDownlineNode(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, 400, "invalid id")
		return
	}
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope

	var node model.AgentNode
	if err := h.DB.First(&node, id).Error; err != nil {
		fail(c, 404, "ไม่พบ node")
		return
	}

	// ⭐ node user: ลบได้เฉพาะ nodes ในสายตัวเอง
	if scope.IsNode {
		found := false
		for _, nid := range scope.NodeIDs {
			if nid == id { found = true; break }
		}
		if !found {
			fail(c, 403, "ไม่มีสิทธิ์ลบ node นี้")
			return
		}
	}

	// ห้ามลบ root
	if node.ParentID == nil {
		fail(c, 400, "ไม่สามารถลบ root node (admin) ได้")
		return
	}

	// เช็ค children
	var childCount int64
	h.DB.Model(&model.AgentNode{}).Where("parent_id = ?", id).Count(&childCount)
	if childCount > 0 {
		fail(c, 400, fmt.Sprintf("ไม่สามารถลบได้ — มีลูกสาย %d คน (ต้องลบลูกก่อน)", childCount))
		return
	}

	// เช็ค members
	var memberCount int64
	h.DB.Model(&model.Member{}).Where("agent_node_id = ?", id).Count(&memberCount)
	if memberCount > 0 {
		fail(c, 400, fmt.Sprintf("ไม่สามารถลบได้ — มีสมาชิก %d คน (ต้องย้ายสมาชิกก่อน)", memberCount))
		return
	}

	// ลบ commission settings ก่อน (cascade)
	h.DB.Where("agent_node_id = ?", id).Delete(&model.AgentNodeCommissionSetting{})

	// ลบ node
	h.DB.Delete(&model.AgentNode{}, id)

	ok(c, gin.H{"deleted": true, "id": id})
}

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
