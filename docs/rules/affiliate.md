# Affiliate (Admin) — admin-api

> Last updated: 2026-04-20 (v1 initial — starter rule)
> Related code: `internal/handler/affiliate_handler.go`, `internal/handler/router.go:344-360`

## 🎯 Purpose
Admin ตั้งค่าระบบแนะนำเพื่อน (referral): commission rate ต่อประเภทหวย, withdrawal conditions, share templates, และ adjust commission ด้วยมือ

## 📋 Rules
1. **Scope per-agent**: ทุก query/update ต้อง filter `agent_id` ของ admin (multi-tenant)
2. **Permission**: group `/affiliate/*` — ไม่ enforce permission ใน router ปัจจุบัน (ใช้ auth + agent scope เพียงพอ); ถ้ามี staff role ในอนาคตให้เติม `finance.affiliate`
3. **Commission rate** ต่อ `lottery_type_id`: NULL = rate default สำหรับทุกประเภท
4. **Share templates**: ข้อความสำเร็จรูปส่งให้ member ใช้แชร์ (platform: line, facebook, twitter, etc.)
5. **Manual adjustment**: audit trail ต้องบันทึก admin_id + reason (reference: audit_log.md)

## 🌐 Endpoints
- GET    `/api/v1/affiliate/settings`            → `GetAffiliateSettings`
- POST   `/api/v1/affiliate/settings`            → `UpsertAffiliateSetting`
- DELETE `/api/v1/affiliate/settings/:id`        → `DeleteAffiliateSetting`
- GET    `/api/v1/affiliate/report`              → `GetAffiliateReport` (summary per member)
- GET    `/api/v1/affiliate/share-templates`     → `ListShareTemplates`
- POST   `/api/v1/affiliate/share-templates`     → `CreateShareTemplate`
- PUT    `/api/v1/affiliate/share-templates/:id` → `UpdateShareTemplate`
- DELETE `/api/v1/affiliate/share-templates/:id` → `DeleteShareTemplate`
- GET    `/api/v1/affiliate/adjustments`         → `ListCommissionAdjustments`
- POST   `/api/v1/affiliate/adjustments`         → `CreateCommissionAdjustment`

## ⚠️ Edge Cases
- ลบ setting ที่มี commission pending อยู่ → ควร reject 409 (rate ต้องอ้างอิงได้จนกว่าจะจ่ายครบ)
- ปรับ rate ระหว่างรอบ → มีผลกับ bet ใหม่เท่านั้น (commission คำนวณตอน settle)

## 🔗 Related
- Frontend: `lotto-standalone-admin-web/src/app/affiliate/page.tsx`
- Member-side: `lotto-standalone-member-api/internal/handler/referral.go`
- Referral system plan: memory `referral_system_plan`

## 📝 Change Log
- 2026-04-20: v1 initial skeleton
