// Package middleware — cookie.go
// Helper functions สำหรับจัดการ httpOnly cookies (admin JWT token)
//
// ทำไมใช้ httpOnly cookie แทน localStorage?
// - localStorage: JavaScript อ่านได้ → XSS attack สามารถขโมย token
// - httpOnly cookie: JavaScript อ่านไม่ได้ → ปลอดภัยจาก XSS
// - SameSite=Lax: ป้องกัน CSRF (ไม่ส่ง cookie ใน cross-site POST)
package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// CookieConfig เก็บ config สำหรับ cookie
type CookieConfig struct {
	Domain string // domain เช่น ".example.com" (ว่าง = current domain)
	Secure bool   // true = HTTPS only
}

// SetAdminTokenCookie ตั้ง httpOnly cookie สำหรับ admin access token
func SetAdminTokenCookie(c *gin.Context, token string, maxAge int, cfg CookieConfig) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     "admin_token",
		Value:    token,
		Path:     "/",
		Domain:   cfg.Domain,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearAdminTokenCookie ลบ admin cookie (ตอน logout)
func ClearAdminTokenCookie(c *gin.Context, cfg CookieConfig) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     "admin_token",
		Value:    "",
		Path:     "/",
		Domain:   cfg.Domain,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: http.SameSiteLaxMode,
	})
}
