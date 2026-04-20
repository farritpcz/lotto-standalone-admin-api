// Package handler — node_portal_handler.go
// Portal สำหรับ Agent Node — Auth (login/logout)
//
// ความสัมพันธ์:
//   - ใช้ middleware.NodeJWTAuth() ตรวจสอบ JWT จาก "node_token" cookie
//   - ใช้ model.AgentNode, AgentProfitTransaction จาก models.go
//   - frontend: admin-web /node/login + /node/portal
//
// กฎสิทธิ์ (ใช้ใน tree/children/profits files):
//  1. เห็นทั้งสาย: ancestors (สายบน) + ตัวเอง + descendants (สายล่าง)
//  2. แก้ไขได้เฉพาะลูกตรง (parent_id = ตัวเอง)
//  3. หลาน/เหลน = read-only (แก้ไขไม่ได้)
//
// Endpoints (split ข้ามหลายไฟล์ — ทุกไฟล์อยู่ package handler):
//
//	POST /node/auth/login       → node_portal_handler.go (ไฟล์นี้)
//	POST /node/auth/logout      → node_portal_handler.go (ไฟล์นี้)
//	GET  /node/me               → node_portal_tree_handler.go
//	GET  /node/tree             → node_portal_tree_handler.go
//	GET  /node/children         → node_portal_children_handler.go
//	POST /node/children         → node_portal_children_handler.go
//	PUT  /node/children/:id     → node_portal_children_handler.go
//	DELETE /node/children/:id   → node_portal_children_handler.go
//	GET  /node/profits          → node_portal_profits_handler.go
package handler

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

// =============================================================================
// POST /node/auth/login — Node Login
//
// ดึง agent_nodes by username → เช็ค bcrypt password → สร้าง JWT
// ตั้ง httpOnly cookie "node_token"
// =============================================================================
func (h *Handler) NodeLogin(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "กรุณากรอก username และ password")
		return
	}

	// ดึง node จาก DB
	var node model.AgentNode
	if err := h.DB.Where("username = ? AND agent_id = 1", req.Username).First(&node).Error; err != nil {
		fail(c, 401, "ชื่อผู้ใช้หรือรหัสผ่านไม่ถูกต้อง")
		return
	}

	// เช็ค password (bcrypt)
	if err := bcrypt.CompareHashAndPassword([]byte(node.PasswordHash), []byte(req.Password)); err != nil {
		fail(c, 401, "ชื่อผู้ใช้หรือรหัสผ่านไม่ถูกต้อง")
		return
	}

	// เช็คสถานะ
	if node.Status != "active" {
		fail(c, 403, "บัญชีถูกระงับ — กรุณาติดต่อหัวสาย")
		return
	}

	// สร้าง JWT token
	token, err := mw.GenerateNodeToken(
		node.ID, node.AgentID, node.Username, node.Role,
		h.AdminJWTSecret, h.AdminJWTExpiryHours,
	)
	if err != nil {
		fail(c, 500, "สร้าง token ไม่สำเร็จ")
		return
	}

	// ตั้ง httpOnly cookie
	maxAge := h.AdminJWTExpiryHours * 3600
	mw.SetNodeTokenCookie(c, token, maxAge, mw.CookieConfig{
		Domain: h.CookieDomain,
		Secure: h.CookieSecure,
	})

	// ตั้ง CSRF cookie สำหรับ node (ใช้ชื่อแยก)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     "node_csrf_token",
		Value:    fmt.Sprintf("%d", time.Now().UnixNano()),
		Path:     "/",
		Domain:   h.CookieDomain,
		MaxAge:   86400,
		HttpOnly: false, // ⭐ frontend ต้องอ่านได้
		Secure:   h.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})

	ok(c, gin.H{
		"node": gin.H{
			"id":            node.ID,
			"name":          node.Name,
			"username":      node.Username,
			"role":          node.Role,
			"share_percent": node.SharePercent,
		},
		"token": token,
	})
}

// =============================================================================
// POST /node/auth/logout — ลบ node cookie
// =============================================================================
func (h *Handler) NodeLogout(c *gin.Context) {
	mw.ClearNodeTokenCookie(c, mw.CookieConfig{
		Domain: h.CookieDomain,
		Secure: h.CookieSecure,
	})
	// ลบ CSRF cookie ด้วย
	http.SetCookie(c.Writer, &http.Cookie{
		Name: "node_csrf_token", Value: "", Path: "/",
		Domain: h.CookieDomain, MaxAge: -1,
		Secure: h.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
	ok(c, gin.H{"message": "ออกจากระบบสำเร็จ"})
}
