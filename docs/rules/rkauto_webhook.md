# RKAUTO Webhook (GobexPay) — admin-api

> Last updated: 2026-04-20 (v1 initial — starter rule)
> Related code: `internal/handler/rkauto_webhook.go`, `internal/handler/router.go:400-405`, `internal/rkauto/`

## 🎯 Purpose
รับ webhook callback จาก RKAUTO (= GobexPay) — เงินเข้า/ออกบัญชี → auto-approve deposit + mark withdraw สำเร็จ/ล้มเหลว  
อ้างอิง memory `rkauto_gobexpay.md`

## 📋 Rules
1. **Public endpoints** — ไม่ต้อง admin JWT (RKAUTO server ยิงมา)
2. ป้องกันด้วย `mw.WebhookSecurity` middleware (router.go:401) — verify signature/secret
3. **Idempotency บังคับ**: ถ้า `rkauto_uuid` (transaction_id) ซ้ำใน `deposit_requests` → return 200 OK ไม่ process ซ้ำ (rkauto_webhook.go:48-50)
4. Body ต้องอ่านผ่าน `c.Get("webhook_body")` (middleware เก็บไว้) — ห้ามอ่าน `c.Request.Body` ตรง (middleware consume แล้ว)
5. Deposit match: exact amount + matching bank account (registered with RKAUTO) → auto-approve + credit balance
6. Deposit ไม่ match → บันทึก unmatched สำหรับ admin ดู manual
7. Withdraw notify: update status `pending → success/failed` ตาม event + trigger notification
8. ทุก state change ต้อง audit log + ยิง Telegram notification (ดู [notifications.md](./notifications.md))
9. Scope: deposit/withdraw request มี `agent_id` — webhook resolve agent จาก bank account ที่ register

## 🌐 Endpoints
- POST `/webhooks/rkauto/deposit-notify`  → `HandleDepositNotify`  — เงินเข้า
- POST `/webhooks/rkauto/withdraw-notify` → `HandleWithdrawNotify` — ถอนเสร็จ/ล้มเหลว

## ⚠️ Edge Cases
- Signature ผิด → `WebhookSecurity` return 401 ก่อนถึง handler
- Amount ไม่ตรง pending request เลย → unmatched, 200 OK ให้ RKAUTO ไม่ retry
- Withdraw ที่ไม่เจอใน DB → log + 200 (กันลูป RKAUTO retry ค้าง)
- Concurrency: deposit ชน 2 requests → DB transaction + idempotency lock on `rkauto_uuid`

## 🔗 Related
- Bank accounts (register/activate RKAUTO): [bank_account_settings.md](./bank_account_settings.md)
- Deposit/Withdraw admin flows: [deposit_withdraw_admin.md](./deposit_withdraw_admin.md)
- Notifications: [notifications.md](./notifications.md)
- EasySlip (manual slip verify, ต่างจาก webhook auto): [easyslip_config.md](./easyslip_config.md)
- Memory: `rkauto_gobexpay.md`
- Package: `internal/rkauto/` (client + types)

## 📝 Change Log
- 2026-04-20: v1 initial skeleton
