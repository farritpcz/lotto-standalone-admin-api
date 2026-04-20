// Package handler — settings admin handlers.
// Split from stubs.go on 2026-04-20.
package handler

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

func (h *Handler) GetSettings(c *gin.Context) {
	// ⭐ ดึง scope — node เห็นเฉพาะ settings ที่เกี่ยวข้องกับตัวเอง
	// settings เป็น key-value table → node ใช้ key prefix "node_{nodeID}_" สำหรับ override
	// admin เห็นทุก settings
	scope := mw.GetNodeScope(c, h.DB)

	var settings []model.Setting
	if scope.IsNode {
		// ⭐ node เห็น: settings ทั่วไป + settings เฉพาะ node ของตัวเอง
		prefix := "node_" + strconv.FormatInt(scope.NodeID, 10) + "_"
		h.DB.Where("`key` NOT LIKE 'node_%' OR `key` LIKE ?", prefix+"%").Find(&settings)
	} else {
		h.DB.Find(&settings)
	}
	ok(c, settings)
}

func (h *Handler) UpdateSettings(c *gin.Context) {
	// ⭐ ดึง scope — node แก้ได้เฉพาะ settings ของตัวเอง (prefix "node_{nodeID}_")
	scope := mw.GetNodeScope(c, h.DB)

	var req map[string]string
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	for key, value := range req {
		// ⭐ ถ้าเป็น node → บังคับ prefix key เพื่อไม่ให้แก้ settings ระบบ
		actualKey := key
		if scope.IsNode {
			prefix := "node_" + strconv.FormatInt(scope.NodeID, 10) + "_"
			// ถ้า key ไม่ได้ขึ้นต้นด้วย prefix ของตัวเอง → เพิ่ม prefix
			if !strings.HasPrefix(key, prefix) {
				actualKey = prefix + key
			}
		}

		// ⭐ Upsert: ถ้ามี key → update, ถ้ายังไม่มี → insert
		var existing model.Setting
		if err := h.DB.Where("`key` = ?", actualKey).First(&existing).Error; err != nil {
			// ไม่มี → สร้างใหม่
			h.DB.Create(&model.Setting{Key: actualKey, Value: value})
		} else {
			// มีอยู่แล้ว → update value
			h.DB.Model(&existing).Update("value", value)
		}
	}
	ok(c, gin.H{"updated": len(req)})
}
