// Package handler — node_portal_children_handler.go
// Node Portal: CRUD ลูกตรง (parent_id = me) — ลิสต์ / สร้าง / แก้ / ลบ
//
// ⭐ กฎสำคัญ: แก้/ลบได้เฉพาะลูกตรง (parent_id = me) เท่านั้น — หลาน/เหลน = read-only
// รับช่วงจาก node_portal_handler.go (auth) — ดูไฟล์นั้นสำหรับ package comment หลัก
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
// GET /node/children — ดูลูกตรง (parent_id = me)
// =============================================================================
func (h *Handler) NodeListChildren(c *gin.Context) {
	nodeID := mw.GetNodeID(c)

	var children []model.AgentNode
	h.DB.Where("parent_id = ?", nodeID).Order("id ASC").Find(&children)

	// นับ members ของแต่ละลูก
	for i := range children {
		h.DB.Model(&model.Member{}).Where("agent_node_id = ?", children[i].ID).Count(&children[i].MemberCount)
		h.DB.Model(&model.AgentNode{}).Where("parent_id = ?", children[i].ID).Count(&children[i].ChildCount)
	}

	ok(c, children)
}

// =============================================================================
// POST /node/children — สร้างลูกตรงใหม่
//
// เหมือน CreateDownlineNode แต่ parent_id = ตัวเอง (บังคับ)
// =============================================================================
func (h *Handler) NodeCreateChild(c *gin.Context) {
	nodeID := mw.GetNodeID(c)

	var req struct {
		Name         string  `json:"name" binding:"required"`
		Username     string  `json:"username" binding:"required"`
		Password     string  `json:"password" binding:"required"`
		SharePercent float64 `json:"share_percent" binding:"required"`
		Phone        string  `json:"phone"`
		LineID       string  `json:"line_id"`
		Note         string  `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "ข้อมูลไม่ถูกต้อง: "+err.Error())
		return
	}

	// ดึง parent (ตัวเอง)
	var me model.AgentNode
	if err := h.DB.First(&me, nodeID).Error; err != nil {
		fail(c, 404, "ไม่พบข้อมูลตัวเอง")
		return
	}

	// Validate share_percent < ตัวเอง
	if req.SharePercent >= me.SharePercent {
		fail(c, 400, fmt.Sprintf("share_percent (%.2f) ต้องน้อยกว่าของคุณ (%.2f)", req.SharePercent, me.SharePercent))
		return
	}
	if req.SharePercent <= 0 {
		fail(c, 400, "share_percent ต้องมากกว่า 0")
		return
	}

	// Hash password
	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		fail(c, 500, "hash password ไม่สำเร็จ")
		return
	}

	// กำหนด role ถัดไป
	role := model.NextRole(me.Role)

	// สร้าง node
	child := model.AgentNode{
		AgentID:      me.AgentID,
		ParentID:     &nodeID,
		Role:         role,
		Name:         req.Name,
		Username:     req.Username,
		PasswordHash: string(hashed),
		Depth:        me.Depth + 1,
		Path:         me.Path, // temporary
		SharePercent: req.SharePercent,
		Phone:        req.Phone,
		LineID:       req.LineID,
		Note:         req.Note,
		Status:       "active",
	}

	if err := h.DB.Create(&child).Error; err != nil {
		if strings.Contains(err.Error(), "Duplicate") || strings.Contains(err.Error(), "uk_agent_node_username") {
			fail(c, 400, "username ซ้ำ — กรุณาเปลี่ยน")
			return
		}
		fail(c, 500, "สร้างลูกสายไม่สำเร็จ: "+err.Error())
		return
	}

	// อัพเดท path
	child.Path = me.Path + fmt.Sprintf("%d/", child.ID)
	h.DB.Model(&child).Update("path", child.Path)

	ok(c, child)
}

// =============================================================================
// PUT /node/children/:id — แก้ไขลูกตรง
//
// ⭐ กฎสำคัญ: ต้องเป็นลูกตรงเท่านั้น (parent_id = me)
// ถ้าเป็นหลาน/เหลน → 403 Forbidden
// =============================================================================
func (h *Handler) NodeUpdateChild(c *gin.Context) {
	nodeID := mw.GetNodeID(c)
	childID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, 400, "invalid id")
		return
	}

	// ดึง child node
	var child model.AgentNode
	if err := h.DB.First(&child, childID).Error; err != nil {
		fail(c, 404, "ไม่พบ node")
		return
	}

	// ⭐ เช็คว่าเป็นลูกตรง (parent_id = ตัวเอง)
	if child.ParentID == nil || *child.ParentID != nodeID {
		fail(c, 403, "สามารถแก้ไขได้เฉพาะลูกตรงของคุณเท่านั้น")
		return
	}

	var req struct {
		Name         *string  `json:"name"`
		SharePercent *float64 `json:"share_percent"`
		Phone        *string  `json:"phone"`
		LineID       *string  `json:"line_id"`
		Note         *string  `json:"note"`
		Status       *string  `json:"status"`
		Password     *string  `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// ดึง me เพื่อ validate share_percent
	var me model.AgentNode
	h.DB.First(&me, nodeID)

	// Validate share_percent
	if req.SharePercent != nil {
		if *req.SharePercent >= me.SharePercent {
			fail(c, 400, fmt.Sprintf("share_percent (%.2f) ต้องน้อยกว่าของคุณ (%.2f)", *req.SharePercent, me.SharePercent))
			return
		}
		if *req.SharePercent <= 0 {
			fail(c, 400, "share_percent ต้องมากกว่า 0")
			return
		}
		// ต้อง > ลูกของ child ทุกคน
		var maxGrandchildPercent float64
		h.DB.Model(&model.AgentNode{}).Where("parent_id = ?", childID).
			Select("COALESCE(MAX(share_percent), 0)").Row().Scan(&maxGrandchildPercent)
		if *req.SharePercent <= maxGrandchildPercent {
			fail(c, 400, fmt.Sprintf("share_percent (%.2f) ต้องมากกว่าลูกของเขา (%.2f)", *req.SharePercent, maxGrandchildPercent))
			return
		}
	}

	// Build updates
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

	h.DB.Model(&model.AgentNode{}).Where("id = ?", childID).Updates(updates)
	h.DB.First(&child, childID)
	ok(c, child)
}

// =============================================================================
// DELETE /node/children/:id — ลบลูกตรง
//
// ⭐ ต้องเป็นลูกตรง + ไม่มี children + ไม่มี members
// =============================================================================
func (h *Handler) NodeDeleteChild(c *gin.Context) {
	nodeID := mw.GetNodeID(c)
	childID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, 400, "invalid id")
		return
	}

	var child model.AgentNode
	if err := h.DB.First(&child, childID).Error; err != nil {
		fail(c, 404, "ไม่พบ node")
		return
	}

	// ⭐ เช็คลูกตรง
	if child.ParentID == nil || *child.ParentID != nodeID {
		fail(c, 403, "สามารถลบได้เฉพาะลูกตรงของคุณเท่านั้น")
		return
	}

	// เช็ค children ของ child
	var grandchildCount int64
	h.DB.Model(&model.AgentNode{}).Where("parent_id = ?", childID).Count(&grandchildCount)
	if grandchildCount > 0 {
		fail(c, 400, fmt.Sprintf("ไม่สามารถลบได้ — มีลูกสาย %d คน", grandchildCount))
		return
	}

	// เช็ค members
	var memberCount int64
	h.DB.Model(&model.Member{}).Where("agent_node_id = ?", childID).Count(&memberCount)
	if memberCount > 0 {
		fail(c, 400, fmt.Sprintf("ไม่สามารถลบได้ — มีสมาชิก %d คน", memberCount))
		return
	}

	h.DB.Where("agent_node_id = ?", childID).Delete(&model.AgentNodeCommissionSetting{})
	h.DB.Delete(&model.AgentNode{}, childID)

	ok(c, gin.H{"deleted": true, "id": childID})
}
