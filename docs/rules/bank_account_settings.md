# Bank Account Settings (บัญชีธนาคาร agent + PromptPay)

> Last updated: 2026-04-20
> Related code: `internal/handler/bank_accounts.go:42` (List), `:60` (Create), `:107` (Update), `:158` (Delete); RKAUTO ops: `internal/handler/stubs.go:3277+`
> Related migrations: `migrations/001_initial_schema.sql`, `migrations/020_absorb_agents_into_nodes.sql`

## 🎯 Purpose
จัดการบัญชีธนาคารของ agent/node สำหรับรับเงินฝาก (deposit) + บัญชีต้นทางถอนเงิน (withdraw) — รองรับ manual + auto transfer ผ่าน RKAUTO (GobexPay)

## 📋 Rules
1. ตาราง `agent_bank_accounts` — scope ด้วย `agent_node_id` (admin=root node, node=ตัวเอง)
2. ฟิลด์หลัก: `bank_code`, `bank_name`, `account_number`, `account_name`, `account_type` (`deposit|withdraw`), `transfer_mode` (`manual|auto`), `is_default`, `status`, `qr_code_url`, `bank_system` (`SMS|BANK|KBIZ` — เฉพาะ auto), `rkauto_uuid`, `rkauto_status`
3. Default: `account_type='deposit'`, `transfer_mode='manual'`, `status='active'`
4. ถ้า `transfer_mode='auto'` + token1/token2 + RKAutoClient → register กับ RKAUTO ทันที (TODO: ยังไม่ทำครบใน CreateAgentBankAccount; ใช้ endpoint `/register-rkauto` แยก)
5. `qr_code_url` optional — frontend upload ผ่าน `/api/v1/upload` แล้วส่ง URL เข้ามา; ถ้าส่ง `""` (pointer) = ลบ QR
6. Update/Delete: node แก้/ลบได้เฉพาะของตัวเอง (WHERE agent_node_id = scope.NodeID) — RowsAffected=0 → 404
7. Permission: `system.settings` สำหรับทุก route ใน `/agent/bank-accounts/*`

## 🔄 Flow (register RKAUTO)
```
POST /api/v1/bank-accounts/:id/register-rkauto
  body { bank_system, username, password, mobile_number(SMS) | bank_code(BANK) }
  → encrypt credentials (AES-256 ด้วย EncryptionKey)
  → rkauto.Client.RegisterBank() → ได้ rkauto_uuid
  → UPDATE rkauto_uuid, rkauto_status='pending'
POST /api/v1/bank-accounts/:id/activate-rkauto   → rkauto_status='active'
POST /api/v1/bank-accounts/:id/deactivate-rkauto → rkauto_status='inactive'
```

## 🌐 API Endpoints
- `GET    /api/v1/agent/bank-accounts`
- `POST   /api/v1/agent/bank-accounts`
- `PUT    /api/v1/agent/bank-accounts/:id`
- `DELETE /api/v1/agent/bank-accounts/:id`
- `POST   /api/v1/bank-accounts/:id/register-rkauto`
- `POST   /api/v1/bank-accounts/:id/activate-rkauto`
- `POST   /api/v1/bank-accounts/:id/deactivate-rkauto`

## ⚠️ Edge Cases
- bank_code / account_number / account_name ว่างตอน create → 400
- Auto mode แต่ RKAutoClient nil (config ปิด) → บันทึกบัญชีได้แต่ไม่ register — ต้องเรียก `/register-rkauto` เอง
- ลบบัญชีที่ถูกอ้างอิงจาก withdraw_requests pending → ควรเช็คก่อน (currently ไม่ได้เช็ค — ควรเพิ่ม)
- PromptPay QR: frontend generate QR image ภายนอก แล้ว upload URL เข้ามาใน `qr_code_url`

## 🔗 Source of Truth
- Handlers: `internal/handler/bank_accounts.go` (CRUD)
- RKAUTO bank ops: `internal/handler/stubs.go:3277+` (Register/Activate/Deactivate)
- RKAUTO client: `internal/rkauto/client.go`, encrypt: `internal/rkauto/encrypt.go`
- Router: `internal/handler/router.go:207-219`

## 📝 Change Log
- 2026-04-20: Initial — CRUD, transfer_mode, RKAUTO lifecycle, QR URL
