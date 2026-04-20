# Contact Channels — admin-api

> Last updated: 2026-04-20 (v1 initial — starter rule)
> Related code: `internal/handler/contact_channels.go`, `internal/handler/router.go:197-204`, `router.go:97` (public)

## 🎯 Purpose
ช่องทางติดต่อที่แสดงหน้าเว็บสมาชิก (Line, Facebook, Telegram, ฯลฯ) — CMS สำหรับ owner/admin ปรับ icon + link + ลำดับ + เปิด/ปิด

## 📋 Rules
1. Scope per-agent: ทุก query/insert ต้อง filter ด้วย `agent_id` ของ admin ที่ล็อกอิน (multi-tenant)
2. Protected routes ต้องมี permission `system.cms` (router.go:198)
3. Public endpoint `GET /api/v1/public/contact-channels` — ไม่ต้อง auth, ใช้แสดงบนหน้า member — ต้องส่งเฉพาะ active=true
4. Icon/QR upload ใช้ folder `"contact"` ผ่าน `POST /upload` (ดู `upload.md`)
5. การลบเป็น hard delete — frontend ควรยืนยันก่อน (ใช้ ConfirmDialog, ห้าม confirm())

## 🌐 Endpoints
- GET    `/api/v1/public/contact-channels` → `ListPublicContactChannels` — สำหรับหน้า member (active เท่านั้น)
- GET    `/api/v1/contact-channels`        → `ListContactChannels` — ทั้งหมด (admin)
- POST   `/api/v1/contact-channels`        → `CreateContactChannel`
- PUT    `/api/v1/contact-channels/:id`    → `UpdateContactChannel`
- DELETE `/api/v1/contact-channels/:id`    → `DeleteContactChannel`

## ⚠️ Edge Cases
- ถ้า `agent_id` ไม่ถูกเซ็ตใน JWT → 401 (middleware จัดการ)
- รูป icon/QR ที่ลบจาก DB ไม่ได้ลบจาก R2 — TODO cleanup worker

## 🔗 Related
- Frontend: `lotto-standalone-admin-web/src/app/settings/contact-channels/`
- Member display: `lotto-standalone-member-web/` — หน้า footer/contact
- Upload: [upload.md](./upload.md)

## 📝 Change Log
- 2026-04-20: v1 initial skeleton
