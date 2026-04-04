// Package middleware — audit log
//
// บันทึกทุก admin action ลง activity_logs table
// เก็บ: admin_id, method, path, request body, response status, IP, timestamp
package middleware

import (
	"bytes"
	"io"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// AuditLog middleware บันทึกทุก mutating request (POST/PUT/DELETE) ลง DB
//
// GET requests ไม่บันทึก (ไม่เปลี่ยนแปลงข้อมูล)
// บันทึก: admin_id, method, path, request body (ตัดที่ 2KB), status code, IP
func AuditLog(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		method := c.Request.Method

		// ข้าม GET, OPTIONS, HEAD (ไม่เปลี่ยนข้อมูล)
		if method == "GET" || method == "OPTIONS" || method == "HEAD" {
			c.Next()
			return
		}

		// อ่าน request body ทั้งหมด แล้วคืนกลับให้ handler ใช้ต่อ
		// ⚠️ เก็บ log แค่ 2KB แต่คืน body เต็มให้ handler
		var bodyStr string
		if c.Request.Body != nil {
			bodyBytes, _ := io.ReadAll(c.Request.Body)
			// คืน body เต็มกลับให้ handler ใช้ต่อ (สำคัญ! ห้ามตัด)
			c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			// เก็บ log แค่ 2KB
			if len(bodyBytes) > 2048 {
				bodyStr = string(bodyBytes[:2048]) + "...(truncated)"
			} else {
				bodyStr = string(bodyBytes)
			}
		}

		// ดำเนินการ handler
		c.Next()

		// บันทึกหลัง handler ทำงานเสร็จ (รู้ status code แล้ว)
		adminID := GetAdminID(c)
		statusCode := c.Writer.Status()
		path := c.Request.URL.Path
		ip := c.ClientIP()

		// INSERT ลง activity_logs (fire-and-forget — ไม่ block response)
		go func() {
			db.Exec(
				`INSERT INTO activity_logs (admin_id, method, path, request_body, status_code, ip_address, created_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?)`,
				adminID, method, path, bodyStr, statusCode, ip, time.Now(),
			)
		}()
	}
}
