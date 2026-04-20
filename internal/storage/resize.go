// Package storage — resize.go
//
// สร้าง image variants หลายขนาด (sm/md/lg) สำหรับ responsive srcset
//
// ทำไมต้องทำ?
//   - Mobile ไม่ต้องโหลด banner 1920px (เปลือง bandwidth + LCP แย่)
//   - Desktop retina ต้องการรูปคมๆ → 1920px
//   - ให้ frontend ใช้ srcset + sizes → browser เลือกขนาดเหมาะสม
//
// Pipeline:
//   1. Decode ภาพต้นฉบับ (JPEG/PNG/GIF/WebP) — ทำไปแล้วใน ReEncode
//   2. ตรวจความกว้างต้นฉบับ (w) → เลือก target widths ที่น้อยกว่า/เท่ากับ
//   3. Resize แต่ละขนาด → encode → return []byte
//   4. Caller (upload.go) upload แต่ละ variant ไป R2 ด้วย naming _sm/_md/_lg
//
// คุณภาพ resize: ใช้ CatmullRom (ดีสุดใน draw package — slower but sharp)
package storage

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/draw"
)

// ImageVariant — 1 ขนาดของภาพ (sm/md/lg)
type ImageVariant struct {
	Name  string // "sm", "md", "lg"
	Width int    // target width (height คำนวณตามอัตราส่วน)
	Data  []byte // encoded bytes
}

// BannerVariants — target widths สำหรับ banner
//
// เลือก 640 (mobile), 1280 (tablet/desktop normal), 1920 (retina/fullHD)
var BannerVariants = []struct {
	Name  string
	Width int
}{
	{"sm", 640},
	{"md", 1280},
	{"lg", 1920},
}

// GenerateBannerVariants — สร้าง 3 ขนาดจากภาพต้นฉบับ
//
// inputData: bytes ของภาพหลัง ReEncode (JPEG หรือ PNG)
// kind: ประเภทภาพ (ใช้เลือก encoder)
//
// returns: array ของ ImageVariant (sm, md, lg) + ext (.jpg หรือ .png) + contentType
// ถ้าภาพเล็กกว่า target width จะ skip ขนาดนั้น (ไม่ upscale)
func GenerateBannerVariants(inputData []byte, kind ImageKind) ([]ImageVariant, string, string, error) {
	// Decode ภาพต้นฉบับ
	src, _, err := image.Decode(bytes.NewReader(inputData))
	if err != nil {
		return nil, "", "", fmt.Errorf("decode for resize failed: %w", err)
	}

	bounds := src.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()
	if srcW == 0 || srcH == 0 {
		return nil, "", "", fmt.Errorf("invalid image dimensions")
	}

	// เลือก encoder — JPEG input → JPEG output, อย่างอื่น → PNG
	var ext, contentType string
	isJPEG := kind == KindJPEG
	if isJPEG {
		ext, contentType = ".jpg", "image/jpeg"
	} else {
		ext, contentType = ".png", "image/png"
	}

	variants := make([]ImageVariant, 0, len(BannerVariants))

	for _, target := range BannerVariants {
		targetW := target.Width
		// ถ้าภาพต้นฉบับเล็กกว่า target → ใช้ขนาดต้นฉบับเลย (อย่า upscale)
		if srcW < targetW {
			targetW = srcW
		}
		// คำนวณ height ตามอัตราส่วน
		targetH := srcH * targetW / srcW

		// Resize ด้วย CatmullRom (คุณภาพดี)
		dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, draw.Over, nil)

		// Encode
		var buf bytes.Buffer
		if isJPEG {
			if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
				return nil, "", "", fmt.Errorf("variant %s JPEG encode failed: %w", target.Name, err)
			}
		} else {
			if err := png.Encode(&buf, dst); err != nil {
				return nil, "", "", fmt.Errorf("variant %s PNG encode failed: %w", target.Name, err)
			}
		}

		variants = append(variants, ImageVariant{
			Name:  target.Name,
			Width: targetW,
			Data:  buf.Bytes(),
		})
	}

	return variants, contentType, ext, nil
}
