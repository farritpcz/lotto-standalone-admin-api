// Package handler — staff admin handlers.
// Split from stubs.go on 2026-04-20.
package handler

import (
	"encoding/json"
	"strconv"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

// =============================================================================
// Staff (Admin Users) — CRUD + permissions
// =============================================================================

// ListStaff รายการ admin ทั้งหมด
func (h *Handler) ListStaff(c *gin.Context) {
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope — เว็บใครเว็บมัน
	var admins []model.Admin
	query := h.DB.Where("status != ?", "deleted")
	if scope.IsNode {
		query = query.Where("agent_node_id = ?", scope.NodeID) // ⭐ เห็นเฉพาะพนักงานเว็บตัวเอง
	} else {
		query = query.Where("agent_node_id = ?", scope.RootNodeID) // admin เห็นเฉพาะพนักงานระดับระบบ
	}
	query.Order("created_at DESC").Find(&admins)
	ok(c, admins)
}

// CreateStaff เพิ่ม admin ใหม่ — ⭐ สร้างภายใต้เว็บของผู้สร้าง
func (h *Handler) CreateStaff(c *gin.Context) {
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope
	var req struct {
		Username    string `json:"username" binding:"required,min=3,max=50"`
		Password    string `json:"password" binding:"required,min=6,max=100"`
		Name        string `json:"name" binding:"required,max=100"`
		Role        string `json:"role"`
		Permissions string `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	if req.Role == "" {
		req.Role = "admin"
	}

	var count int64
	h.DB.Model(&model.Admin{}).Where("username = ?", req.Username).Count(&count)
	if count > 0 {
		fail(c, 400, "username นี้ถูกใช้แล้ว")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		fail(c, 500, "failed to hash password")
		return
	}

	admin := model.Admin{
		Username: req.Username, PasswordHash: string(hash),
		Name: req.Name, Role: req.Role, Permissions: req.Permissions, Status: "active",
		AgentNodeID: scope.SettingNodeID(), // ⭐ สร้างภายใต้เว็บของผู้สร้าง
	}
	if err := h.DB.Create(&admin).Error; err != nil {
		fail(c, 500, "failed to create admin")
		return
	}
	ok(c, admin)
}

// UpdateStaff แก้ไข admin
// ⭐ scope: ป้องกัน node แก้พนักงานเว็บอื่น
func (h *Handler) UpdateStaff(c *gin.Context) {
	scope := mw.GetNodeScope(c, h.DB)
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	var admin model.Admin
	// ⭐ scope: ดึงเฉพาะพนักงานในเว็บเดียวกัน
	query := h.DB.Where("id = ?", id)
	if scope.IsNode {
		query = query.Where("agent_node_id = ?", scope.NodeID)
	} else {
		query = query.Where("agent_node_id = ?", scope.RootNodeID)
	}
	if err := query.First(&admin).Error; err != nil {
		fail(c, 404, "admin not found")
		return
	}

	var req struct {
		Name        string `json:"name"`
		Role        string `json:"role"`
		Permissions string `json:"permissions"`
		Password    string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	if req.Name != "" {
		admin.Name = req.Name
	}
	// ⭐ Validate role — ห้ามตั้ง role เกินสิทธิ์ตัวเอง
	if req.Role != "" {
		allowedRoles := map[string]bool{"operator": true, "viewer": true}
		// owner เปลี่ยนได้ทุก role, admin เปลี่ยนได้เฉพาะ operator/viewer
		callerRole, _ := c.Get("admin_role")
		if callerRole == "owner" {
			allowedRoles["admin"] = true
			allowedRoles["owner"] = true
		}
		if !allowedRoles[req.Role] {
			fail(c, 403, "ไม่มีสิทธิ์ตั้ง role \""+req.Role+"\"")
			return
		}
		admin.Role = req.Role
	}
	if req.Permissions != "" {
		// ⭐ Validate permissions format — ต้องเป็น JSON array of strings
		var perms []string
		if err := json.Unmarshal([]byte(req.Permissions), &perms); err != nil {
			fail(c, 400, "permissions ต้องเป็น JSON array เช่น [\"members.view\",\"finance.deposits\"]")
			return
		}
		admin.Permissions = req.Permissions
	}
	if req.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			fail(c, 500, "failed to hash password")
			return
		}
		admin.PasswordHash = string(hash)
	}
	h.DB.Save(&admin)
	ok(c, admin)
}

// UpdateStaffStatus เปลี่ยนสถานะ admin
// ⭐ scope: ป้องกัน node เปลี่ยนสถานะพนักงานเว็บอื่น
func (h *Handler) UpdateStaffStatus(c *gin.Context) {
	scope := mw.GetNodeScope(c, h.DB)
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	query := h.DB.Model(&model.Admin{}).Where("id = ?", id)
	if scope.IsNode {
		query = query.Where("agent_node_id = ?", scope.NodeID)
	} else {
		query = query.Where("agent_node_id = ?", scope.RootNodeID)
	}
	result := query.Update("status", req.Status)
	if result.RowsAffected == 0 {
		fail(c, 404, "admin not found หรือไม่มีสิทธิ์")
		return
	}
	ok(c, gin.H{"id": id, "status": req.Status})
}

// DeleteStaff ลบ admin (soft delete)
// ⭐ scope: ป้องกัน node ลบพนักงานเว็บอื่น
func (h *Handler) DeleteStaff(c *gin.Context) {
	scope := mw.GetNodeScope(c, h.DB)
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	adminID := middleware.GetAdminID(c)
	if id == adminID {
		fail(c, 400, "ไม่สามารถลบตัวเองได้")
		return
	}

	query := h.DB.Model(&model.Admin{}).Where("id = ?", id)
	if scope.IsNode {
		query = query.Where("agent_node_id = ?", scope.NodeID)
	} else {
		query = query.Where("agent_node_id = ?", scope.RootNodeID)
	}
	result := query.Update("status", "deleted")
	if result.RowsAffected == 0 {
		fail(c, 404, "admin not found หรือไม่มีสิทธิ์")
		return
	}
	ok(c, gin.H{"id": id, "status": "deleted"})
}

// GetStaffLoginHistory ดูประวัติ login ของพนักงาน
// GET /api/v1/staff/:id/login-history
// ⭐ scope: ดูได้เฉพาะพนักงานในเว็บตัวเอง
func (h *Handler) GetStaffLoginHistory(c *gin.Context) {
	scope := mw.GetNodeScope(c, h.DB)
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	// เช็คว่า staff คนนี้อยู่ในเว็บเดียวกัน
	var admin model.Admin
	query := h.DB.Where("id = ?", id)
	if scope.IsNode {
		query = query.Where("agent_node_id = ?", scope.NodeID)
	} else {
		query = query.Where("agent_node_id = ?", scope.RootNodeID)
	}
	if err := query.First(&admin).Error; err != nil {
		fail(c, 404, "ไม่พบพนักงาน หรือไม่มีสิทธิ์ดู")
		return
	}

	var history []model.AdminLoginHistory
	h.DB.Where("admin_id = ?", id).Order("created_at DESC").Limit(50).Find(&history)
	ok(c, history)
}

// GetStaffActivity ดู activity log ของพนักงานคนเดียว
// GET /api/v1/staff/:id/activity
// ⭐ scope: ดูได้เฉพาะพนักงานในเว็บตัวเอง
func (h *Handler) GetStaffActivity(c *gin.Context) {
	scope := mw.GetNodeScope(c, h.DB)
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	// เช็คว่า staff คนนี้อยู่ในเว็บเดียวกัน
	var admin model.Admin
	query := h.DB.Where("id = ?", id)
	if scope.IsNode {
		query = query.Where("agent_node_id = ?", scope.NodeID)
	} else {
		query = query.Where("agent_node_id = ?", scope.RootNodeID)
	}
	if err := query.First(&admin).Error; err != nil {
		fail(c, 404, "ไม่พบพนักงาน หรือไม่มีสิทธิ์ดู")
		return
	}

	var logs []model.ActivityLog
	h.DB.Where("admin_id = ?", id).Order("created_at DESC").Limit(50).Find(&logs)
	ok(c, logs)
}
