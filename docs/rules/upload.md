# Upload (R2 / S3) — admin-api

> Last updated: 2026-04-20 (v1 initial — starter rule)
> Related code: `internal/handler/upload.go`, `internal/handler/router.go:194`, `internal/storage/` (R2Client)

## 🎯 Purpose
Upload endpoint กลางสำหรับ admin — เก็บรูปใน Cloudflare R2 (S3-compatible), whitelist folder, re-encode กัน XSS/EXIF/payload แฝง

## 📋 Rules (Security — สำคัญ)
1. **Auth bound**: route อยู่ใน `protected` group → ต้องมี admin JWT
2. **Folder whitelist** (upload.go:32-42): `lottery | banner | logo | favicon | promo | bank | contact | avatar | general`
   - ⚠️ admin ไม่ให้ upload folder `"slip"` (member-only)
3. **Magic bytes validation** — ไม่เชื่อ Content-Type จาก client
4. **Max size ต่าง folder** (banner/promo 2MB, logo 500KB, ฯลฯ — config ใน storage package)
5. **Max dimensions** — กัน decompression bomb
6. **Re-encode** ทุกไฟล์ → strip EXIF + metadata + payload แฝง
7. **UUID filename** — ไม่เก็บชื่อ user input (กัน path traversal + name collision)
8. **SVG ห้าม** — เสี่ยง stored XSS (SVG รัน JavaScript ได้)
9. R2 ไม่ configured → `503 Service Unavailable` (upload.go:47-54)
10. Response ต้องคืน URL public ที่หน้าเว็บเอาไปใช้ได้ทันที

## 🌐 Endpoints
- POST `/api/v1/upload` — multipart/form-data
  - `file`: รูป (required)
  - `folder`: whitelist เท่านั้น (required)

## 🔧 Static Serving
- `GET /uploads/*` — serve local fallback path (router.go:389) — ใช้เฉพาะ dev/local (production ใช้ URL R2 ตรง)

## ⚠️ Edge Cases
- Upload ไฟล์ไม่ใช่รูป (เช่น .exe เปลี่ยนนามสกุล) → magic bytes ไม่ผ่าน → 400
- รูปใหญ่เกิน max size → 413 พร้อม error ชัดเจน
- R2 upload fail (network) → 502, ไม่ save metadata

## 🔗 Related
- ใช้โดย: [promotions.md](./promotions.md) (promo), [banner_cms.md](./banner_cms.md) (banner), [bank_account_settings.md](./bank_account_settings.md) (bank), [contact_channels.md](./contact_channels.md) (contact)
- Storage client: `internal/storage/r2.go`
- Member-side upload (slip): `lotto-standalone-member-api` (คนละ handler, มี folder `slip`)

## 📝 Change Log
- 2026-04-20: v1 initial skeleton
