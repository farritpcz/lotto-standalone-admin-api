// Package middleware — csrf.go
// CSRF protection ด้วย Double Submit Cookie pattern
// (เหมือน member-api แต่ cookie name = admin_csrf_token)
package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/gin-gonic/gin"
)

// CSRFProtect middleware ตรวจสอบ CSRF token สำหรับ state-changing requests
// ⭐ env=development → skip CSRF (เปิดเฉพาะ production)
func CSRFProtect(env ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if len(env) > 0 && env[0] != "production" {
			c.Next()
			return
		}
		method := c.Request.Method
		if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
			c.Next()
			return
		}

		cookieToken, err := c.Cookie("admin_csrf_token")
		if err != nil || cookieToken == "" {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"error":   "missing CSRF token cookie",
			})
			c.Abort()
			return
		}

		headerToken := c.GetHeader("X-CSRF-Token")
		if headerToken == "" {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"error":   "missing X-CSRF-Token header",
			})
			c.Abort()
			return
		}

		if cookieToken != headerToken {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"error":   "CSRF token mismatch",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// SetCSRFCookie สร้าง + set admin CSRF token cookie
func SetCSRFCookie(c *gin.Context, cfg CookieConfig) {
	b := make([]byte, 32)
	rand.Read(b)
	token := hex.EncodeToString(b)

	http.SetCookie(c.Writer, &http.Cookie{
		Name:     "admin_csrf_token",
		Value:    token,
		Path:     "/",
		Domain:   cfg.Domain,
		MaxAge:   86400, // 1 วัน (admin session สั้นกว่า)
		HttpOnly: false, // ⭐ Frontend ต้องอ่านได้
		Secure:   cfg.Secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearCSRFCookie ลบ admin CSRF cookie
func ClearCSRFCookie(c *gin.Context, cfg CookieConfig) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     "admin_csrf_token",
		Value:    "",
		Path:     "/",
		Domain:   cfg.Domain,
		MaxAge:   -1,
		HttpOnly: false,
		Secure:   cfg.Secure,
		SameSite: http.SameSiteLaxMode,
	})
}
