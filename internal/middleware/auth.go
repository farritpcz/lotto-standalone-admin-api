// Package middleware — admin JWT authentication
//
// ตรวจสอบ JWT token สำหรับ admin routes
// ใช้ HMAC-SHA256 signing method
package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// AdminClaims ข้อมูลที่เก็บใน admin JWT token
type AdminClaims struct {
	AdminID  int64  `json:"admin_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// AdminJWTAuth middleware ตรวจสอบ admin JWT token
//
// Flow:
//  1. อ่าน token จาก httpOnly cookie (admin_token) ก่อน
//  2. ถ้าไม่มี cookie → fallback อ่าน Authorization header: "Bearer <token>"
//  3. Parse + verify token ด้วย admin secret
//  4. ถ้า valid → เก็บ admin_id, admin_username, admin_role ใน context
//  5. ถ้า invalid → return 401
//
// ⭐ รองรับทั้ง cookie (admin-web) และ header (API client)
func AdminJWTAuth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// ⭐ ลำดับ: 1) httpOnly cookie → 2) Authorization header
		tokenString := extractAdminToken(c)
		if tokenString == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error":   "missing authentication token",
			})
			c.Abort()
			return
		}

		claims := &AdminClaims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(secret), nil
		})

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error":   "invalid or expired token",
			})
			c.Abort()
			return
		}

		// เก็บ admin info ใน context
		// ⭐ role="node" หมายถึง login จาก agent_nodes (สายงาน)
		// AdminID จะเป็น node.ID, Role = "node"
		c.Set("admin_id", claims.AdminID)
		c.Set("admin_username", claims.Username)
		c.Set("admin_role", claims.Role)
		// ⭐ flag สำหรับเช็คว่าเป็น node user
		c.Set("is_node_user", claims.Role == "node")

		c.Next()
	}
}

// GenerateAdminToken สร้าง JWT token สำหรับ admin
func GenerateAdminToken(adminID int64, username, role, secret string, expiryHours int) (string, error) {
	claims := &AdminClaims{
		AdminID:  adminID,
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

// extractAdminToken ดึง JWT token จาก cookie หรือ Authorization header
func extractAdminToken(c *gin.Context) string {
	// 1) httpOnly cookie (จาก admin-web)
	if cookie, err := c.Cookie("admin_token"); err == nil && cookie != "" {
		return cookie
	}
	// 2) Authorization header: "Bearer <token>" (fallback)
	authHeader := c.GetHeader("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
			return parts[1]
		}
	}
	return ""
}

// GetAdminID ดึง admin_id จาก context (helper)
// AIDEV-NOTE: รองรับทั้ง int64 (ตอน SetAdminContext) และ float64 (จาก JWT claim ตรงๆ)
// jwt lib decode ตัวเลขเป็น float64 เสมอ — handler ที่อ่าน claim ดิบต้องแปลง
func GetAdminID(c *gin.Context) int64 {
	v, exists := c.Get("admin_id")
	if !exists {
		return 0
	}
	if id, ok := v.(int64); ok {
		return id
	}
	if idF, ok := v.(float64); ok {
		return int64(idF)
	}
	return 0
}

// GetAdminIDPtr — คืน *int64 แทน int64 สำหรับ column ที่เก็บ NULL ได้
// (nil ถ้าไม่มี admin_id หรือค่า = 0)
func GetAdminIDPtr(c *gin.Context) *int64 {
	id := GetAdminID(c)
	if id <= 0 {
		return nil
	}
	return &id
}
