// Package handler — auth admin handlers.
// Split from stubs.go on 2026-04-20.
package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

// =============================================================================
// Auth — Admin Login
// =============================================================================

func (h *Handler) AdminLogin(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// ⭐ ลอง admin ก่อน → ถ้าไม่เจอ → ลอง agent_nodes (สายงาน)
	var admin model.Admin
	if err := h.DB.Where("username = ?", req.Username).First(&admin).Error; err == nil {
		// === พบใน admins table ===
		if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.Password)) != nil {
			fail(c, 401, "invalid credentials")
			return
		}
		if admin.Status != "active" {
			fail(c, 403, "account suspended")
			return
		}

		now := time.Now()
		ip := c.ClientIP()
		h.DB.Model(&admin).Updates(map[string]interface{}{"last_login_at": &now, "last_login_ip": ip})
		h.DB.Create(&model.AdminLoginHistory{
			AdminID: admin.ID, IP: ip, UserAgent: c.GetHeader("User-Agent"), Success: true, CreatedAt: now,
		})

		// ⭐ เช็คว่าพนักงานนี้สร้างจากเว็บไหน (agent_node_id)
		// ถ้ามี agent_node_id → login เป็น node user (เห็นเฉพาะข้อมูลเว็บนั้น)
		// ถ้าไม่มี (NULL) → login เป็น admin ปกติ (เห็นทุกอย่าง)
		if admin.AgentNodeID != nil {
			// ⭐ พนักงานของเว็บ → สร้าง token ด้วย role จริง (operator/viewer) + AdminID ยังเป็น admin.ID
			// แต่ set flag เป็น "node" ใน user_type เพื่อให้ NodeScope รู้ว่าต้อง scope ข้อมูล
			nodeID := *admin.AgentNodeID
			// ใช้ role="node" เพื่อให้ NodeScope ทำงาน แต่เก็บ role จริงไว้ใน context ด้วย
			token, err := middleware.GenerateAdminToken(admin.ID, admin.Username, "node", h.AdminJWTSecret, h.AdminJWTExpiryHours)
			if err != nil {
				fail(c, 500, "failed to generate token")
				return
			}
			middleware.SetAdminTokenCookie(c, token, h.AdminJWTExpiryHours*3600, h.cookieConfig())
			middleware.SetCSRFCookie(c, h.cookieConfig())
			ok(c, gin.H{
				"admin": admin, "token": token, "permissions": admin.Permissions,
				"user_type": "node", "node_id": nodeID,
			})
			return
		}

		// ⭐ admin ระดับระบบ (ไม่มี agent_node_id) → login ปกติ
		token, err := middleware.GenerateAdminToken(admin.ID, admin.Username, admin.Role, h.AdminJWTSecret, h.AdminJWTExpiryHours)
		if err != nil {
			fail(c, 500, "failed to generate token")
			return
		}

		middleware.SetAdminTokenCookie(c, token, h.AdminJWTExpiryHours*3600, h.cookieConfig())
		middleware.SetCSRFCookie(c, h.cookieConfig())
		ok(c, gin.H{"admin": admin, "token": token, "permissions": admin.Permissions, "user_type": "admin"})
		return
	}

	// === ไม่เจอใน admins → ลอง agent_nodes (สายงาน) ===
	var node model.AgentNode
	if err := h.DB.Where("username = ?", req.Username).First(&node).Error; err != nil {
		fail(c, 401, "invalid credentials")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(node.PasswordHash), []byte(req.Password)) != nil {
		fail(c, 401, "invalid credentials")
		return
	}
	if node.Status != "active" {
		fail(c, 403, "บัญชีถูกระงับ — กรุณาติดต่อหัวสาย")
		return
	}

	// สร้าง node_token + admin_token (role="node") เพื่อเข้า admin panel ได้
	nodeToken, _ := middleware.GenerateNodeToken(node.ID, node.RootNodeID(), node.Username, node.Role, h.AdminJWTSecret, h.AdminJWTExpiryHours)
	adminToken, _ := middleware.GenerateAdminToken(node.ID, node.Username, "node", h.AdminJWTSecret, h.AdminJWTExpiryHours)

	maxAge := h.AdminJWTExpiryHours * 3600
	middleware.SetNodeTokenCookie(c, nodeToken, maxAge, h.cookieConfig())
	middleware.SetAdminTokenCookie(c, adminToken, maxAge, h.cookieConfig())
	middleware.SetCSRFCookie(c, h.cookieConfig())

	ok(c, gin.H{
		"admin": gin.H{
			"id": node.ID, "username": node.Username, "name": node.Name,
			"role": "node", "permissions": "", "status": node.Status,
		},
		"token":         adminToken,
		"permissions":   "",
		"user_type":     "node",
		"node_id":       node.ID,
		"node_role":     node.Role,
		"share_percent": node.SharePercent,
	})
}

// AdminLogout ออกจากระบบ — ลบ httpOnly cookie
//
// POST /api/v1/auth/logout
func (h *Handler) AdminLogout(c *gin.Context) {
	middleware.ClearAdminTokenCookie(c, h.cookieConfig())
	middleware.ClearCSRFCookie(c, h.cookieConfig())
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "logged out"})
}

// cookieConfig ดึง CookieConfig จาก Handler fields
func (h *Handler) cookieConfig() middleware.CookieConfig {
	return middleware.CookieConfig{
		Domain: h.CookieDomain,
		Secure: h.CookieSecure,
	}
}
