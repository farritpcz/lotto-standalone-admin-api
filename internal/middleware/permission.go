// Package middleware — permission check
//
// เช็คว่า admin มีสิทธิ์ทำ action นั้นหรือไม่
// owner + admin = ทำได้ทุกอย่าง
// operator = ทำได้เฉพาะที่อยู่ใน permissions JSON
// viewer = ดูได้อย่างเดียว (GET เท่านั้น)
package middleware

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RequirePermission middleware เช็คสิทธิ์ก่อนเข้า handler
//
// Usage: protected.PUT("/members/:id/balance", mw.RequirePermission(db, "members.adjust_balance"), h.AdjustMemberBalance)
//
// Logic:
//   - owner/admin → ผ่านเสมอ
//   - viewer → GET เท่านั้น (ถ้า permission ต้องการ action อื่น → 403)
//   - operator → เช็ค permissions JSON array
func RequirePermission(db *gorm.DB, permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		adminID := GetAdminID(c)
		if adminID == 0 {
			c.JSON(http.StatusUnauthorized, gin.H{"success": false, "error": "unauthorized"})
			c.Abort()
			return
		}

		// ดึง role + permissions จาก DB (cache ใน context ถ้ามี)
		role, _ := c.Get("admin_role")
		roleStr, _ := role.(string)

		// owner + admin = bypass ทั้งหมด
		if roleStr == "owner" || roleStr == "admin" {
			c.Next()
			return
		}

		// viewer = GET เท่านั้น
		if roleStr == "viewer" {
			if c.Request.Method != "GET" {
				c.JSON(http.StatusForbidden, gin.H{"success": false, "error": "ไม่มีสิทธิ์ (viewer ดูได้อย่างเดียว)"})
				c.Abort()
				return
			}
			// viewer ดู GET ได้ แต่ถ้า permission ระบุ .approve, .manage → 403
			if strings.HasSuffix(permission, ".approve") || strings.HasSuffix(permission, ".manage") ||
				strings.HasSuffix(permission, ".submit") || strings.HasSuffix(permission, ".adjust_balance") {
				c.JSON(http.StatusForbidden, gin.H{"success": false, "error": "ไม่มีสิทธิ์"})
				c.Abort()
				return
			}
			c.Next()
			return
		}

		// operator = เช็ค permissions JSON
		var permissionsStr string
		db.Table("admins").Select("permissions").Where("id = ?", adminID).Row().Scan(&permissionsStr)

		if permissionsStr == "" {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "error": "ไม่มีสิทธิ์ (ยังไม่ได้ตั้งค่า permissions)"})
			c.Abort()
			return
		}

		var perms []string
		if err := json.Unmarshal([]byte(permissionsStr), &perms); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "error": "permissions format ผิด"})
			c.Abort()
			return
		}

		// เช็คว่ามี permission ที่ต้องการ
		// รองรับ wildcard: "members.*" ครอบทุก members.xxx
		hasPermission := false
		for _, p := range perms {
			if p == permission {
				hasPermission = true
				break
			}
			// wildcard: "members.*" matches "members.view", "members.edit", etc.
			if strings.HasSuffix(p, ".*") {
				prefix := strings.TrimSuffix(p, ".*")
				if strings.HasPrefix(permission, prefix+".") {
					hasPermission = true
					break
				}
			}
		}

		if !hasPermission {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "error": "ไม่มีสิทธิ์ (" + permission + ")"})
			c.Abort()
			return
		}

		c.Next()
	}
}
