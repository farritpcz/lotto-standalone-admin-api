# Promotions — admin-api

> Last updated: 2026-04-20 (v1 initial — starter rule)
> Related code: `internal/handler/promotions.go`, `internal/handler/router.go:236-245`

## 🎯 Purpose
CMS โปรโมชั่น (banner + title + description + link + active flag) ที่แสดงหน้าเว็บ member — admin เพิ่ม/แก้/ลบ/ปิดเปิด

## 📋 Rules
1. Scope per-agent: ทุก row มี `agent_id` — query filter เสมอ
2. Permission: `system.cms` (router.go:239)
3. Status toggle แยก endpoint `PUT /:id/status` — ลด payload เวลาเปิด/ปิดเร็ว ๆ จาก list
4. รูปโปรโมชั่น upload ผ่าน `POST /upload` folder=`promo` (ดู [upload.md](./upload.md))
5. Delete = hard delete — frontend ต้องใช้ ConfirmDialog
6. Order: ควรมี `sort_order` field — ถ้ายังไม่มี TODO เพิ่ม (ตอนนี้อาจ order by created_at)
7. Member-facing: public endpoint อยู่ใน member-api (ไม่ใช่ที่นี่) — อ่านจาก DB ร่วม

## 🌐 Endpoints
- GET    `/api/v1/promotions`            → `ListPromotions`
- POST   `/api/v1/promotions`            → `CreatePromotion`
- PUT    `/api/v1/promotions/:id`        → `UpdatePromotion`
- PUT    `/api/v1/promotions/:id/status` → `UpdatePromotionStatus`
- DELETE `/api/v1/promotions/:id`        → `DeletePromotion`

## ⚠️ Edge Cases
- ลบโปรที่มีรูปใน R2 → ยังไม่ลบรูปจาก R2 (TODO cleanup)
- Active=false แต่ยังอยากเห็นใน admin list → list คืนทั้งหมด, filter ฝั่ง member

## 🔗 Related
- Upload: [upload.md](./upload.md)
- Banner CMS: [banner_cms.md](./banner_cms.md)
- Frontend: `lotto-standalone-admin-web/src/app/cms/promotions/`
- Member display: `lotto-standalone-member-web/` — หน้า home / promo

## 📝 Change Log
- 2026-04-20: v1 initial skeleton
