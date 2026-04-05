// Package middleware — node_auth.go
// JWT authentication สำหรับ Agent Node (ระบบปล่อยสาย)
//
// แยกจาก admin auth:
//   - admin ใช้ cookie "admin_token" + AdminClaims
//   - node  ใช้ cookie "node_token"  + NodeClaims
//   - ใช้ JWT secret เดียวกัน แต่ claims ต่างกัน
//
// Flow:
//  1. Node login → POST /node/auth/login → ได้ JWT ใน cookie "node_token"
//  2. ทุก request → NodeJWTAuth middleware อ่าน "node_token" cookie
//  3. Valid → เก็บ node_id, node_agent_id, node_username, node_role ใน context
//  4. Handler ใช้ GetNodeID(c) ดึง node_id มาเช็คสิทธิ์
package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// NodeClaims ข้อมูลที่เก็บใน node JWT token
// แยกจาก AdminClaims เพื่อไม่ให้ปนกัน
type NodeClaims struct {
	NodeID   int64  `json:"node_id"`   // agent_nodes.id
	AgentID  int64  `json:"agent_id"`  // agents.id (multi-tenant)
	Username string `json:"username"`  // agent_nodes.username
	Role     string `json:"role"`      // admin/share_holder/senior/master/agent/agent_downline
	jwt.RegisteredClaims
}

// NodeJWTAuth middleware ตรวจสอบ node JWT token
//
// ลำดับอ่าน token:
//  1. httpOnly cookie "node_token" (จาก web portal)
//  2. Authorization header "Bearer <token>" (fallback สำหรับ API)
//
// เมื่อ valid → set context:
//  - "node_id"       → int64
//  - "node_agent_id" → int64
//  - "node_username" → string
//  - "node_role"     → string
func NodeJWTAuth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// ดึง token จาก cookie หรือ header
		tokenString := extractNodeToken(c)
		if tokenString == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error":   "กรุณาเข้าสู่ระบบก่อน",
			})
			c.Abort()
			return
		}

		// Parse + verify JWT
		claims := &NodeClaims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(secret), nil
		})

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error":   "token ไม่ถูกต้องหรือหมดอายุ",
			})
			c.Abort()
			return
		}

		// เก็บ node info ใน context
		c.Set("node_id", claims.NodeID)
		c.Set("node_agent_id", claims.AgentID)
		c.Set("node_username", claims.Username)
		c.Set("node_role", claims.Role)

		c.Next()
	}
}

// GenerateNodeToken สร้าง JWT token สำหรับ agent node
func GenerateNodeToken(nodeID, agentID int64, username, role, secret string, expiryHours int) (string, error) {
	claims := &NodeClaims{
		NodeID:   nodeID,
		AgentID:  agentID,
		Username: username,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Duration(expiryHours) * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "lotto-standalone-admin-api",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// extractNodeToken ดึง JWT token จาก cookie หรือ header
func extractNodeToken(c *gin.Context) string {
	// 1) httpOnly cookie "node_token"
	if cookie, err := c.Cookie("node_token"); err == nil && cookie != "" {
		return cookie
	}
	// 2) Authorization header (fallback)
	authHeader := c.GetHeader("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
			return parts[1]
		}
	}
	return ""
}

// GetNodeID ดึง node_id จาก context (helper สำหรับ handler)
func GetNodeID(c *gin.Context) int64 {
	if id, exists := c.Get("node_id"); exists {
		if v, ok := id.(int64); ok {
			return v
		}
	}
	return 0
}

// GetNodeAgentID ดึง agent_id ของ node จาก context
func GetNodeAgentID(c *gin.Context) int64 {
	if id, exists := c.Get("node_agent_id"); exists {
		if v, ok := id.(int64); ok {
			return v
		}
	}
	return 0
}

// SetNodeTokenCookie ตั้ง httpOnly cookie สำหรับ node token
func SetNodeTokenCookie(c *gin.Context, token string, maxAge int, cfg CookieConfig) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     "node_token",
		Value:    token,
		Path:     "/",
		Domain:   cfg.Domain,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearNodeTokenCookie ลบ node cookie (ตอน logout)
func ClearNodeTokenCookie(c *gin.Context, cfg CookieConfig) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     "node_token",
		Value:    "",
		Path:     "/",
		Domain:   cfg.Domain,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: http.SameSiteLaxMode,
	})
}
