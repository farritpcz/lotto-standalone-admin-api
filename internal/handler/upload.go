// Package handler — upload.go
// อัพโหลดรูปภาพ + serve static files
//
// POST /api/v1/upload — รับไฟล์ multipart/form-data → บันทึก → return URL
// GET  /uploads/:filename — serve static file
//
// ⚠️ SECURITY:
// - จำกัดขนาด 5MB
// - รับเฉพาะ jpg, jpeg, png, gif, svg, webp
// - rename ไฟล์เป็น UUID ป้องกัน path traversal
// - เก็บใน ./uploads/ directory
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
)

const (
	uploadDir     = "./uploads"
	maxUploadSize = 5 << 20 // 5MB
)

var allowedExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true,
	".gif": true, ".svg": true, ".webp": true,
}

// UploadFile อัพโหลดไฟล์รูปภาพ
// POST /api/v1/upload
// Content-Type: multipart/form-data
// Field: file (required), folder (optional: "lottery", "banner", "avatar")
func (h *Handler) UploadFile(c *gin.Context) {
	// จำกัดขนาด
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadSize)

	file, err := c.FormFile("file")
	if err != nil {
		fail(c, 400, "กรุณาเลือกไฟล์ (max 5MB)")
		return
	}

	// เช็คนามสกุล
	ext := strings.ToLower(filepath.Ext(file.Filename))
	if !allowedExts[ext] {
		fail(c, 400, "รองรับเฉพาะ jpg, png, gif, svg, webp")
		return
	}

	// สร้าง folder ถ้าไม่มี
	folder := c.DefaultPostForm("folder", "general")
	dir := filepath.Join(uploadDir, folder)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fail(c, 500, "สร้าง directory ไม่สำเร็จ")
		return
	}

	// rename เป็น UUID ป้องกัน path traversal
	newName := fmt.Sprintf("%s_%d%s", uuid.New().String()[:12], time.Now().Unix(), ext)
	savePath := filepath.Join(dir, newName)

	if err := c.SaveUploadedFile(file, savePath); err != nil {
		fail(c, 500, "บันทึกไฟล์ไม่สำเร็จ")
		return
	}

	// URL สำหรับเข้าถึงไฟล์
	fileURL := fmt.Sprintf("/uploads/%s/%s", folder, newName)

	ok(c, gin.H{
		"url":      fileURL,
		"filename": newName,
		"folder":   folder,
		"size":     file.Size,
		"type":     file.Header.Get("Content-Type"),
	})
}
