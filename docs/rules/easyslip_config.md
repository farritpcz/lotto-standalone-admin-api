# EasySlip Config — admin-api

> Last updated: 2026-04-20 (v1 initial — starter rule)
> Related code: `internal/handler/easyslip_config.go`, `internal/handler/router.go:333-342`

## 🎯 Purpose
ตั้งค่า EasySlip API (ตรวจสลิปโอนเงินอัตโนมัติ) ต่อ agent + ดู log การ verify + ทดสอบ API key

## 📋 Rules
1. Scope per-agent: `getEasySlipNodeID()` (easyslip_config.go:30) ดึง agent_id จาก JWT — ทุก query filter ด้วย agent_id
2. Permissions:
   - Config CRUD + test: ต้อง `system.settings`
   - List verifications + get verification: ต้อง `finance.deposits` (router.go:340-341)
3. API key เก็บใน DB ต้อง encrypted (ถ้ายังไม่เข้ารหัส → TODO)
4. TestConnection: ยิงจริงไป EasySlip API ด้วย key ที่ user กรอก (ยังไม่ save) → ใช้ก่อน upsert
5. Verifications คือ log การยิงสลิปไป EasySlip ต่อ deposit_request — 1:1 relation
6. Delete config = ปิด EasySlip (ไม่ลบ verification history)

## 🌐 Endpoints
- GET    `/api/v1/easyslip/config`                    → `GetEasySlipConfig`
- POST   `/api/v1/easyslip/config`                    → `UpsertEasySlipConfig` (create/update)
- DELETE `/api/v1/easyslip/config`                    → `DeleteEasySlipConfig`
- POST   `/api/v1/easyslip/test`                      → `TestEasySlipConnection`
- GET    `/api/v1/easyslip/verifications`             → `ListEasySlipVerifications` (perm: finance.deposits)
- GET    `/api/v1/easyslip/deposits/:id/verification` → `GetDepositVerification`

## ⚠️ Edge Cases
- API key ผิด → TestConnection คืน error ของ EasySlip ตรง ๆ
- Quota หมด → EasySlip คืน specific error code → ต้องแสดงให้ user เข้าใจ
- Deposit ที่ไม่มี verification (manual/old) → GetDepositVerification คืน 404 friendly

## 🔗 Related
- Deposits: [deposit_withdraw_admin.md](./deposit_withdraw_admin.md)
- Upload (slip): member-api upload slip → เรียก EasySlip verify

## 📝 Change Log
- 2026-04-20: v1 initial skeleton
