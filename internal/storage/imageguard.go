// Package storage — imageguard.go
//
// ⚠️ [Security] Image upload validation & hardening
//
// หน้าที่:
//  1. ตรวจ magic bytes (ไม่เชื่อ Content-Type / filename extension)
//  2. จำกัดขนาดไฟล์ (ต่อ folder)
//  3. จำกัดขนาดภาพ (width × height) ป้องกัน decompression bomb
//  4. Re-encode ภาพ → strip EXIF, metadata, payload แฝง
//  5. Sanitize folder name (whitelist)
//
// ทำไมต้องทำทั้งหมดนี้?
//   - Image uploads เป็นช่องทางโจมตีเบอร์หนึ่งของเว็บ (stored XSS, RCE, SSRF)
//   - Magic bytes ปลอมได้ถ้าตรวจแค่ extension
//   - EXIF ของสลิปลูกค้ามี GPS + device info (PDPA violation)
//   - Polyglot files (รูป + JS/PHP ในไฟล์เดียว) → re-encode ทำลายได้หมด
//
// ✋ ห้าม SVG! — SVG รัน JavaScript ได้ = stored XSS
package storage

import (
	"bytes"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"strings"

	"golang.org/x/image/webp" // WebP decoder (encoder ไม่ใช่ stdlib → re-encode เป็น PNG/JPEG แทน)
)

// ImageKind — ประเภทภาพที่รองรับ (ใช้กำหนด re-encode target)
type ImageKind string

const (
	KindJPEG ImageKind = "jpeg"
	KindPNG  ImageKind = "png"
	KindGIF  ImageKind = "gif"
	KindWebP ImageKind = "webp"
)

// AllowedFolder — whitelist ของ folder ที่อนุญาตให้ upload
// ⚠️ [Security] ห้ามรับ folder param จาก user ตรงๆ (path traversal)
var AllowedFolders = map[string]bool{
	// admin folders
	"lottery": true,
	"banner":  true,
	"logo":    true,
	"favicon": true,
	"promo":   true, // ⭐ โปรโมชั่น
	"bank":    true, // ⭐ QR + icon บัญชีธนาคาร
	"contact": true, // ⭐ QR ช่องทางติดต่อ
	"general": true,
	// member folders
	"slip":   true, // ⭐ สลิปฝากเงิน (retention 30 วัน)
	"avatar": true, // ⭐ รูปโปรไฟล์สมาชิก
}

// SanitizeFolder — ตรวจว่า folder ถูกต้อง + อยู่ใน whitelist
// คืน folder ที่ปลอดภัย หรือ error ถ้าไม่ผ่าน
func SanitizeFolder(folder string) (string, error) {
	folder = strings.ToLower(strings.TrimSpace(folder))
	if folder == "" {
		return "general", nil
	}
	// ห้ามมี path separator หรือ special chars
	if strings.ContainsAny(folder, "/\\.:;*?\"<>| \t\n\r") {
		return "", fmt.Errorf("invalid folder name")
	}
	if !AllowedFolders[folder] {
		return "", fmt.Errorf("folder '%s' not allowed", folder)
	}
	return folder, nil
}

// DetectImageKind — ตรวจ magic bytes (ไม่เชื่อ Content-Type header)
//
// Magic bytes references:
//
//	JPEG: FF D8 FF
//	PNG:  89 50 4E 47 0D 0A 1A 0A
//	GIF:  47 49 46 38 (37|39) 61
//	WebP: 52 49 46 46 ? ? ? ? 57 45 42 50
//
// ✋ คืน error ถ้าเป็น SVG / BMP / TIFF / format อื่น (ถือว่าเป็นภัย)
func DetectImageKind(data []byte) (ImageKind, error) {
	if len(data) < 12 {
		return "", fmt.Errorf("file too small to be an image")
	}

	// JPEG: FF D8 FF
	if data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return KindJPEG, nil
	}
	// PNG: 89 50 4E 47 0D 0A 1A 0A
	if bytes.HasPrefix(data, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}) {
		return KindPNG, nil
	}
	// GIF: GIF87a หรือ GIF89a
	if bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a")) {
		return KindGIF, nil
	}
	// WebP: RIFF....WEBP
	if bytes.HasPrefix(data, []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")) {
		return KindWebP, nil
	}

	// ⚠️ [Security] ปฏิเสธทุก format ที่เหลือ รวมถึง SVG (<?xml... หรือ <svg...)
	return "", fmt.Errorf("unsupported image format (only JPEG/PNG/GIF/WebP allowed)")
}

// SizeLimitForFolder — ขนาดสูงสุดต่อ folder (bytes)
// ⭐ แยกลิมิตตาม use case → slip/avatar ต้องเล็ก, banner/promo ต้องใหญ่ขึ้น
func SizeLimitForFolder(folder string) int64 {
	switch folder {
	case "avatar", "logo", "favicon":
		return 500 * 1024 // 500 KB
	case "slip":
		return 1 * 1024 * 1024 // 1 MB
	case "bank", "contact":
		return 1 * 1024 * 1024 // 1 MB (QR code รูปเล็ก)
	case "banner", "promo", "lottery":
		return 2 * 1024 * 1024 // 2 MB
	default:
		return 1 * 1024 * 1024 // 1 MB (general)
	}
}

// MaxImageDimensions — limit ขนาด width × height (ป้องกัน decompression bomb)
// decompression bomb = ไฟล์เล็กๆ แต่ decompress แล้ว RAM ระเบิด
const (
	MaxImageWidth  = 4096
	MaxImageHeight = 4096
	MaxImagePixels = 16000000 // 16MP (ครอบ 4000×4000 พอดี)
)

// ReEncode — decode + encode ภาพใหม่ → strip EXIF/metadata/payload
//
// ⭐ ทำไมต้อง re-encode?
//  1. EXIF ของสลิปลูกค้ามี GPS, device model (PDPA violation)
//  2. Polyglot file (JPEG + PHP ในไฟล์เดียว) → decode/encode ใหม่ → PHP หาย
//  3. Normalize format → ทุกไฟล์เก็บด้วย encoding มาตรฐาน
//
// returns: (re-encoded bytes, final content-type, ext)
func ReEncode(data []byte, kind ImageKind) ([]byte, string, string, error) {
	reader := bytes.NewReader(data)

	var img image.Image
	var err error
	switch kind {
	case KindJPEG:
		img, err = jpeg.Decode(reader)
	case KindPNG:
		img, err = png.Decode(reader)
	case KindGIF:
		img, err = gif.Decode(reader)
	case KindWebP:
		img, err = webp.Decode(reader)
	default:
		return nil, "", "", fmt.Errorf("unsupported kind: %s", kind)
	}
	if err != nil {
		return nil, "", "", fmt.Errorf("decode failed: %w", err)
	}

	// ⚠️ [Security] ตรวจขนาดภาพหลัง decode (ป้องกัน decompression bomb)
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w > MaxImageWidth || h > MaxImageHeight {
		return nil, "", "", fmt.Errorf("image too large: %dx%d (max %dx%d)", w, h, MaxImageWidth, MaxImageHeight)
	}
	if int64(w)*int64(h) > MaxImagePixels {
		return nil, "", "", fmt.Errorf("image area too large: %d pixels (max %d)", w*h, MaxImagePixels)
	}

	// ⭐ Re-encode → เลือก format ปลายทาง
	// JPEG → JPEG (quality 85 — คุณภาพดี + ไฟล์เล็ก)
	// PNG → PNG (stdlib lossless)
	// GIF → PNG (ป้องกัน animated payload + ไม่ค่อยจำเป็นต้อง animate)
	// WebP → PNG (encoder ของ WebP ไม่อยู่ใน stdlib)
	var buf bytes.Buffer
	var contentType, ext string
	switch kind {
	case KindJPEG:
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
			return nil, "", "", fmt.Errorf("JPEG encode failed: %w", err)
		}
		contentType, ext = "image/jpeg", ".jpg"
	default: // PNG, GIF, WebP → แปลงเป็น PNG
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", "", fmt.Errorf("PNG encode failed: %w", err)
		}
		contentType, ext = "image/png", ".png"
	}

	return buf.Bytes(), contentType, ext, nil
}

// ValidateAndReEncode — all-in-one: ตรวจ + re-encode ภาพให้พร้อม upload
//
// steps:
//  1. check size limit (ตาม folder)
//  2. detect magic bytes (reject SVG/BMP/etc)
//  3. re-encode → strip EXIF/payload
//  4. return safe bytes + content-type + ext
//
// caller ต้องทำ:
//   - sanitize folder (via SanitizeFolder)
//   - rate limit (middleware)
//   - auth check (middleware)
func ValidateAndReEncode(reader io.Reader, folder string) (safeData []byte, contentType string, ext string, err error) {
	sizeLimit := SizeLimitForFolder(folder)
	// ⚠️ [Security] LimitReader + เผื่อ 1 byte → ถ้าอ่านได้เกินลิมิต = ปฏิเสธ
	limited := io.LimitReader(reader, sizeLimit+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, "", "", fmt.Errorf("read failed: %w", err)
	}
	if int64(len(data)) > sizeLimit {
		return nil, "", "", fmt.Errorf("file too large (max %d bytes)", sizeLimit)
	}

	kind, err := DetectImageKind(data)
	if err != nil {
		return nil, "", "", err
	}

	return ReEncode(data, kind)
}
