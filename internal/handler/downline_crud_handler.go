// Package handler — downline_crud_handler.go
// CRUD mutations สำหรับระบบปล่อยสาย (Agent Downline): Create / Update / Delete
//
// Read-only operations (tree / list / get detail) อยู่ใน downline_handler.go
// Report operations (profits / reconciliation) อยู่ใน downline_report_handler.go
package handler

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

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
//  1. share_percent < parent.share_percent
//  2. role ถูกต้องตามลำดับ
//  3. username ไม่ซ้ำในเดียวกัน agent
//
// =============================================================================
func (h *Handler) CreateDownlineNode(c *gin.Context) {
	var req struct {
		ParentID     *int64  `json:"parent_id"` // nil = root (admin)
		Name         string  `json:"name" binding:"required"`
		Username     string  `json:"username" binding:"required"`
		Password     string  `json:"password" binding:"required"`
		SharePercent float64 `json:"share_percent" binding:"required"`
		Role         string  `json:"role"` // optional: auto จาก parent
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
//
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
//
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
			if nid == id {
				found = true
				break
			}
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
