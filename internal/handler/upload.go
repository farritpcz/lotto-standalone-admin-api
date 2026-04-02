// Package handler — upload.go
// อัพโหลดรูปภาพ → Cloudflare R2 (primary) หรือ local disk (fallback)
//
// POST /api/v1/upload — multipart/form-data
//
// ⚠️ SECURITY:
// - จำกัดขนาด 5MB
// - รับเฉพาะ jpg, jpeg, png, gif, svg, webp
// - rename UUID ป้องกัน path traversal
package handler

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/farritpcz/lotto-standalone-admin-api/internal/storage"
)

const (
	uploadDir     = "./uploads"
	maxUploadSize = 5 << 20 // 5MB
)

var allowedExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true,
	".gif": true, ".svg": true, ".webp": true,
}

// UploadFile อัพโหลดรูปภาพ
// POST /api/v1/upload
// Field: file (required), folder (optional: "lottery", "banner", "avatar")
//
// ถ้า R2 configured → อัพไป R2 → return R2 public URL
// ถ้า R2 ไม่มี → เก็บ local → return /uploads/... URL
func (h *Handler) UploadFile(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadSize)

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		fail(c, 400, "กรุณาเลือกไฟล์ (max 5MB)")
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !allowedExts[ext] {
		fail(c, 400, "รองรับเฉพาะ jpg, png, gif, svg, webp")
		return
	}

	folder := c.DefaultPostForm("folder", "general")
	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// ── ลอง R2 ก่อน ──
	if r2, ok := h.R2.(*storage.R2Client); ok && r2 != nil && r2.IsConfigured() {
		publicURL, err := r2.Upload(folder, header.Filename, contentType, file)
		if err != nil {
			fail(c, 500, "R2 upload failed: "+err.Error())
			return
		}

		ok2(c, gin.H{
			"url":      publicURL,
			"storage":  "r2",
			"filename": filepath.Base(publicURL),
			"folder":   folder,
			"size":     header.Size,
			"type":     contentType,
		})
		return
	}

	// ── Fallback: Local disk ──
	dir := filepath.Join(uploadDir, folder)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fail(c, 500, "สร้าง directory ไม่สำเร็จ")
		return
	}

	newName := fmt.Sprintf("%s_%d%s", uuid.New().String()[:12], time.Now().Unix(), ext)
	savePath := filepath.Join(dir, newName)

	if err := c.SaveUploadedFile(header, savePath); err != nil {
		fail(c, 500, "บันทึกไฟล์ไม่สำเร็จ")
		return
	}

	fileURL := fmt.Sprintf("/uploads/%s/%s", folder, newName)

	ok2(c, gin.H{
		"url":      fileURL,
		"storage":  "local",
		"filename": newName,
		"folder":   folder,
		"size":     header.Size,
		"type":     contentType,
	})
}

// ok2 helper — เหมือน ok แต่ใช้ใน upload (ป้องกัน conflict กับ ok ใน stubs.go)
func ok2(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}
