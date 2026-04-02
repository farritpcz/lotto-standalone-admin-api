// Package middleware — CORS + security headers
package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORS สร้าง CORS middleware ที่ whitelist เฉพาะ domain ที่กำหนด
//
// development: อนุญาต localhost ทุก port
// production: อนุญาตเฉพาะ domain ที่ส่งเข้ามา
func CORS(allowedOrigins []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")

		// เช็คว่า origin อยู่ใน whitelist หรือไม่
		allowed := false
		for _, ao := range allowedOrigins {
			if ao == origin {
				allowed = true
				break
			}
			// development: อนุญาต localhost ทุก port
			if strings.HasPrefix(ao, "http://localhost") && strings.HasPrefix(origin, "http://localhost") {
				allowed = true
				break
			}
		}

		if allowed && origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
		}

		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization, X-Request-ID, X-Idempotency-Key")
		c.Header("Access-Control-Max-Age", "86400")

		// Security headers
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
