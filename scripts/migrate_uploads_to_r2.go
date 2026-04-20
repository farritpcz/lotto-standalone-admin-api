//go:build ignore
// +build ignore

// migrate_uploads_to_r2.go — ย้ายรูปเก่าจาก local /uploads → R2
//
// Usage:
//
//	go run scripts/migrate_uploads_to_r2.go --dry-run        # ดูว่าจะทำอะไร (ไม่เปลี่ยน DB/R2)
//	go run scripts/migrate_uploads_to_r2.go                  # รันจริง
//
// Flow:
//  1. Connect DB + R2
//  2. สำหรับแต่ละ column ที่เก็บ image URL:
//     - SELECT records ที่ url ขึ้นต้นด้วย "/uploads/"
//     - อ่านไฟล์จาก disk (relative to admin-api/uploads/)
//     - ถ้าเป็น SVG: ⚠️ ตั้ง url = NULL (frontend fallback handles) + log warning
//     - ถ้าเป็น JPEG/PNG/GIF/WebP: upload ไป R2 → UPDATE url
//  3. รายงานผล
//
// ⚠️ [Security] SVG ไฟล์เก่าจะถูก NULL ทิ้ง (ไม่ upload) เพราะ:
//   - SVG รัน JavaScript ได้ → stored XSS risk
//   - ใหม่ทั้งระบบห้าม SVG แล้ว (imageguard.go)
//   - Frontend มี fallback SVG pattern + gradient อยู่แล้ว
//
// Prerequisites:
//   - env vars: DB_PASSWORD, R2_ACCOUNT_ID, R2_ACCESS_KEY, R2_SECRET_KEY, R2_PUBLIC_URL
//   - local /uploads folder ยังอยู่ (ของ admin-api)
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// uploadTarget — แต่ละ column ที่เก็บ URL รูปภาพ
// columns เหล่านี้ปัจจุบันอาจเก็บ "/uploads/xxx" — ต้องแปลงเป็น R2 URL
type uploadTarget struct {
	Table  string
	Column string
	Folder string // folder ปลายทางบน R2 เช่น "lottery", "banner"
}

var targets = []uploadTarget{
	{Table: "lottery_types", Column: "image_url", Folder: "lottery"},
	{Table: "cms_banners", Column: "image_url", Folder: "banner"},
	{Table: "agent_nodes", Column: "logo_url", Folder: "logo"},
	{Table: "agent_nodes", Column: "favicon_url", Folder: "favicon"},
	{Table: "promotions", Column: "image_url", Folder: "promo"},
	{Table: "agent_bank_accounts", Column: "qr_code_url", Folder: "bank"},
	{Table: "contact_channels", Column: "icon_url", Folder: "contact"},
	{Table: "contact_channels", Column: "qr_code_url", Folder: "contact"},
	{Table: "members", Column: "avatar_url", Folder: "avatar"},
	{Table: "deposit_requests", Column: "slip_url", Folder: "slip"},
}

func main() {
	// ─── CLI flags ─────────────────────────────────────────
	dryRun := flag.Bool("dry-run", false, "show what would be done without making changes")
	uploadsDir := flag.String("uploads-dir", "./lotto-standalone-admin-api/uploads", "path to local /uploads folder (admin-api)")
	flag.Parse()

	if *dryRun {
		log.Println("🧪 DRY RUN MODE — no changes will be made")
	} else {
		log.Println("🚀 LIVE MODE — will modify DB + upload to R2")
	}

	// ─── Load env ───────────────────────────────────────────
	dbPass := getEnvOrFatal("DB_PASSWORD")
	r2AccountID := getEnvOrFatal("R2_ACCOUNT_ID")
	r2AccessKey := getEnvOrFatal("R2_ACCESS_KEY")
	r2SecretKey := getEnvOrFatal("R2_SECRET_KEY")
	r2Bucket := getEnv("R2_BUCKET", "lotto-system")
	r2PublicURL := getEnvOrFatal("R2_PUBLIC_URL")

	// ─── Connect DB ─────────────────────────────────────────
	dsn := fmt.Sprintf("root:%s@tcp(localhost:3306)/lotto_standalone?charset=utf8mb4&parseTime=True&loc=Local", dbPass)
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("❌ Failed to connect DB:", err)
	}
	log.Println("✅ Connected to MySQL")

	// ─── Connect R2 (only if not dry-run) ──────────────────
	var r2Client *s3.Client
	if !*dryRun {
		cfg, err := config.LoadDefaultConfig(context.Background(),
			config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(r2AccessKey, r2SecretKey, "")),
			config.WithRegion("auto"),
		)
		if err != nil {
			log.Fatal("❌ Failed to load R2 config:", err)
		}
		endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", r2AccountID)
		r2Client = s3.NewFromConfig(cfg, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(endpoint)
		})
		log.Println("✅ Connected to R2")
	}

	// ─── Migrate each target ───────────────────────────────
	totalMigrated, totalSkippedSVG, totalMissing, totalAlreadyR2 := 0, 0, 0, 0

	for _, t := range targets {
		log.Printf("\n━━━ %s.%s → folder=%s ━━━", t.Table, t.Column, t.Folder)

		// SELECT รายการที่ยังเป็น path เก่า
		type row struct {
			ID  int64
			URL string
		}
		var rows []row
		err := db.Raw(fmt.Sprintf(`SELECT id, %s AS url FROM %s
			WHERE %s IS NOT NULL AND %s != ''
			AND (%s LIKE '/uploads/%%' OR %s LIKE '%%//%%/uploads/%%')
			AND %s NOT LIKE '%%r2.dev%%'`,
			t.Column, t.Table, t.Column, t.Column, t.Column, t.Column, t.Column)).Scan(&rows).Error
		if err != nil {
			log.Printf("  ⚠️ Query failed: %v — skip", err)
			continue
		}

		if len(rows) == 0 {
			log.Printf("  ℹ️ 0 records with legacy /uploads/ path")
			continue
		}

		log.Printf("  📋 Found %d records", len(rows))

		for _, r := range rows {
			// path เช่น "/uploads/lottery/foo.svg" หรือ "http://localhost:8081/uploads/lottery/foo.svg"
			// → "lottery/foo.svg"
			var relPath string
			if idx := strings.Index(r.URL, "/uploads/"); idx >= 0 {
				relPath = strings.TrimPrefix(r.URL[idx:], "/uploads/")
			} else {
				relPath = r.URL
			}
			fullPath := filepath.Join(*uploadsDir, relPath)

			// อ่านไฟล์
			data, err := os.ReadFile(fullPath)
			if err != nil {
				log.Printf("  ❌ id=%d: file missing (%s) — %v", r.ID, fullPath, err)
				totalMissing++
				continue
			}

			// ตรวจชนิดจาก extension
			ext := strings.ToLower(filepath.Ext(fullPath))
			isSVG := ext == ".svg"

			if isSVG {
				log.Printf("  ⚠️ id=%d: SVG file → NULL out (security: XSS risk)", r.ID)
				totalSkippedSVG++
				if !*dryRun {
					db.Exec(fmt.Sprintf("UPDATE %s SET %s = NULL WHERE id = ?", t.Table, t.Column), r.ID)
				}
				continue
			}

			contentType := mimeFromExt(ext)
			if contentType == "" {
				log.Printf("  ❌ id=%d: unsupported ext %s", r.ID, ext)
				continue
			}

			// สร้าง key ใหม่บน R2
			newKey := fmt.Sprintf("%s/migrated_%s_%d%s", t.Folder, uuid.New().String()[:8], time.Now().Unix(), ext)

			if *dryRun {
				log.Printf("  [DRY] id=%d: would upload %s → R2:%s", r.ID, relPath, newKey)
				totalMigrated++
				continue
			}

			// Upload
			_, err = r2Client.PutObject(context.Background(), &s3.PutObjectInput{
				Bucket:             aws.String(r2Bucket),
				Key:                aws.String(newKey),
				Body:               bytes.NewReader(data),
				ContentType:        aws.String(contentType),
				ContentDisposition: aws.String("inline"),
				CacheControl:       aws.String("public, max-age=31536000, immutable"),
			})
			if err != nil {
				log.Printf("  ❌ id=%d: R2 upload failed: %v", r.ID, err)
				continue
			}

			newURL := fmt.Sprintf("%s/%s", strings.TrimRight(r2PublicURL, "/"), newKey)

			// UPDATE DB
			if err := db.Exec(fmt.Sprintf("UPDATE %s SET %s = ? WHERE id = ?", t.Table, t.Column),
				newURL, r.ID).Error; err != nil {
				log.Printf("  ❌ id=%d: DB update failed: %v", r.ID, err)
				continue
			}

			log.Printf("  ✅ id=%d: %s → %s", r.ID, relPath, newURL)
			totalMigrated++
		}

		// Extra: ตรวจว่ามี URL ที่เป็น R2 แล้ว (รายงานอย่างเดียว — ไม่ทำอะไร)
		var alreadyR2 int64
		db.Raw(fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE %s LIKE '%%r2.dev%%'`, t.Table, t.Column)).Scan(&alreadyR2)
		if alreadyR2 > 0 {
			log.Printf("  ℹ️ %d records already on R2 (skipped)", alreadyR2)
			totalAlreadyR2 += int(alreadyR2)
		}
	}

	// ─── Summary ───────────────────────────────────────────
	log.Printf("\n━━━━━━━━━━━ SUMMARY ━━━━━━━━━━━")
	log.Printf("  ✅ Migrated:      %d files", totalMigrated)
	log.Printf("  ⚠️  SVG NULLed:    %d files (security)", totalSkippedSVG)
	log.Printf("  ❌ Missing:       %d files (file not on disk)", totalMissing)
	log.Printf("  ℹ️  Already R2:    %d records (total across tables)", totalAlreadyR2)
	if *dryRun {
		log.Printf("\n🧪 DRY RUN — rerun without --dry-run to apply")
	} else {
		log.Printf("\n🎉 DONE")
	}
}

// ─── helpers ───────────────────────────────────────────────

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvOrFatal(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("❌ env var %s not set", key)
	}
	return v
}

func mimeFromExt(ext string) string {
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
}

// ensure io used (for build)
var _ = io.Discard
