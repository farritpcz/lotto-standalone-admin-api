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
//  1. อ่าน Authorization header: "Bearer <token>"
//  2. Parse + verify token ด้วย admin secret
//  3. ถ้า valid → เก็บ admin_id, admin_username, admin_role ใน context
//  4. ถ้า invalid → return 401
func AdminJWTAuth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error":   "missing authorization header",
			})
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error":   "invalid authorization format, expected: Bearer <token>",
			})
			c.Abort()
			return
		}

		claims := &AdminClaims{}
		token, err := jwt.ParseWithClaims(parts[1], claims, func(token *jwt.Token) (interface{}, error) {
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
		c.Set("admin_id", claims.AdminID)
		c.Set("admin_username", claims.Username)
		c.Set("admin_role", claims.Role)

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

// GetAdminID ดึง admin_id จาก context (helper)
func GetAdminID(c *gin.Context) int64 {
	if id, exists := c.Get("admin_id"); exists {
		if v, ok := id.(int64); ok {
			return v
		}
	}
	return 0
}
