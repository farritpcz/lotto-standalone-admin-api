// Package handler — upload.go
//
// Upload endpoint สำหรับ admin-api
//
// ⚠️ [Security] — admin upload ก็ต้องปลอดภัยเหมือน member:
//  1. Auth (admin JWT) — ต้องล็อกอินเป็น admin ก่อน
//  2. Folder whitelist — admin ใช้ได้: lottery, banner, logo, favicon, promo, bank, contact, general
//  3. Magic bytes validation (ไม่เชื่อ Content-Type)
//  4. Max size ต่าง folder (banner/promo 2MB, logo 500KB, etc.)
//  5. Max dimensions (ป้องกัน decompression bomb)
//  6. Re-encode → strip EXIF + metadata + payload แฝง
//  7. UUID filename (ไม่เก็บชื่อ user input)
//
// ⚠️ [Security] SVG ถูกถอดออก — เสี่ยง stored XSS (SVG รัน JavaScript ได้)
//
// POST /api/v1/upload — multipart/form-data
//   - file:   ไฟล์รูป (required)
//   - folder: whitelist เท่านั้น
package handler

import (
	"bytes"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/farritpcz/lotto-standalone-admin-api/internal/storage"
)

// adminAllowedFolders — folder ที่ admin ใช้ได้ (กว้างกว่า member)
// ⚠️ [Security] admin ไม่ให้ upload ไป folder "slip" (เฉพาะ member)
var adminAllowedFolders = map[string]bool{
	"lottery": true,
	"banner":  true,
	"logo":    true,
	"favicon": true,
	"promo":   true, // ⭐ โปรโมชั่น
	"bank":    true, // ⭐ QR + icon บัญชีธนาคาร
	"contact": true, // ⭐ QR ช่องทางติดต่อ
	"avatar":  true, // admin avatar
	"general": true,
}

// UploadFile — POST /api/v1/upload (admin only)
func (h *Handler) UploadFile(c *gin.Context) {
	// ⭐ R2 พร้อมใช้?
	r2, ok := h.R2.(*storage.R2Client)
	if !ok || r2 == nil || !r2.IsConfigured() {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"error":   "upload service unavailable (R2 not configured)",
		})
		return
	}

	// =================================================================
	// [1] Sanitize folder
	// =================================================================
	folder := c.PostForm("folder")
	if folder == "" {
		folder = c.DefaultPostForm("folder", "general")
	}
	folder, err := storage.SanitizeFolder(folder)
	if err != nil {
		fail(c, 400, err.Error())
		return
	}
	// ⚠️ [Security] whitelist admin folders
	if !adminAllowedFolders[folder] {
		fail(c, 403, "folder '"+folder+"' not allowed for admin")
		return
	}

	// =================================================================
	// [2] รับไฟล์
	// =================================================================
	fileHeader, err := c.FormFile("file")
	if err != nil {
		fail(c, 400, "กรุณาเลือกไฟล์")
		return
	}

	sizeLimit := storage.SizeLimitForFolder(folder)
	if fileHeader.Size > sizeLimit {
		fail(c, 413, "ไฟล์ใหญ่เกินไป")
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		fail(c, 500, "ไม่สามารถเปิดไฟล์ได้")
		return
	}
	defer file.Close()

	// =================================================================
	// [3] Validate + re-encode (magic bytes + strip EXIF + dimensions)
	// =================================================================
	safeData, contentType, ext, err := storage.ValidateAndReEncode(file, folder)
	if err != nil {
		fail(c, 415, "ไฟล์ไม่ถูกต้อง: "+err.Error())
		return
	}

	// =================================================================
	// [4] Upload ไป R2
	//
	// ⭐ Banner folder: สร้าง 3 ขนาด (sm 640, md 1280, lg 1920) สำหรับ srcset
	//    ส่วน folder อื่น: upload ขนาดเดียว (เดิม)
	// =================================================================
	if folder == "banner" {
		// ดึง kind กลับมา (validator decoded แล้ว แต่ไม่ส่ง kind กลับ → detect อีกรอบ)
		kind, kErr := storage.DetectImageKind(safeData)
		if kErr != nil {
			fail(c, 500, "re-detect kind failed: "+kErr.Error())
			return
		}
		variants, vContentType, vExt, genErr := storage.GenerateBannerVariants(safeData, kind)
		if genErr != nil {
			fail(c, 500, "generate variants failed: "+genErr.Error())
			return
		}
		urls, upErr := r2.UploadVariants(folder, vContentType, vExt, variants)
		if upErr != nil {
			fail(c, 500, "R2 upload failed: "+upErr.Error())
			return
		}
		// ⭐ ใช้ _lg เป็น URL หลัก (backward compat) — frontend ถอด suffix เพื่อ build srcset
		mainURL := urls["lg"]
		if mainURL == "" {
			mainURL = urls["md"]
		}
		ok2(c, gin.H{
			"url":      mainURL,
			"variants": urls, // {sm, md, lg}
			"storage":  "r2",
			"folder":   folder,
			"type":     vContentType,
		})
		return
	}

	// ───── Non-banner: upload ขนาดเดียว (เดิม) ─────
	publicURL, err := r2.Upload(folder, "upload"+ext, contentType, bytes.NewReader(safeData))
	if err != nil {
		fail(c, 500, "R2 upload failed: "+err.Error())
		return
	}

	ok2(c, gin.H{
		"url":     publicURL,
		"storage": "r2",
		"folder":  folder,
		"size":    len(safeData),
		"type":    contentType,
	})
}

// ok2 helper — ส่ง success response (แยกจาก ok ใน stubs.go เพื่อไม่ชนกัน)
func ok2(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}
