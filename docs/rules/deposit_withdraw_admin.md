# Deposit / Withdraw Admin (อนุมัติ ฝาก-ถอน + RKAUTO)

> Last updated: 2026-04-20
> Related code: `internal/handler/stubs.go:2267` (ListDeposits), `:2312` (Approve), `:2409` (Reject), `:2444` (Cancel), `:2525` (ListWithdraws), `:2575` (ApproveWithdraw), `:2661` (RejectWithdraw); `internal/handler/rkauto_webhook.go`; `internal/rkauto/client.go`
> Related migrations: `migrations/001_initial_schema.sql`, `migrations/021_easyslip_integration.sql`, `migrations/022_optimize_deposit_transactions.sql`

## 🎯 Purpose
อนุมัติ / ปฏิเสธ / ยกเลิกคำขอฝาก-ถอนของสมาชิก + webhook idempotent จาก RKAUTO (GobexPay) สำหรับ auto-match ฝากเงิน / auto-transfer ถอนเงิน

## 📋 Rules — Deposits
1. **ApproveDeposit** ต้องสถานะ `pending` เท่านั้น — นอกจากนั้น 400
2. scope.HasMember(memberID) = false → 403
3. ใน transaction:
   - `UPDATE deposit_requests SET status='approved', approved_at, approved_by`
   - `UPDATE members SET balance = balance + amount`
   - `INSERT transactions type='deposit', balance_before/after, note`
4. **First Deposit Bonus** (optional): เช็ค settings `first_deposit_bonus_enabled=true` + ตรวจว่าเป็น deposit แรกจริง (นับจาก `transactions`) → credit bonus + บันทึก `bonus` transaction + set `turnover_required`
5. **RejectDeposit**: ต้อง `pending` → status='rejected' + `reject_reason` + `approved_by` (RowsAffected=0 → 400)
6. **CancelDeposit**: ต้อง `approved` → status='cancelled' + `refund` flag:
   - `refund=true` (default): หักเงินคืน (atomic WHERE balance >= amount)
   - `refund=false`: ไม่หัก แต่บันทึก audit `admin_debit` amount=0

## 📋 Rules — Withdrawals
7. **member-api หักเงินตอนสร้างคำขอแล้ว** → Approve ไม่ต้องหักอีก, Reject ต้องคืน
8. **ApproveWithdraw**: body `mode: auto|manual`
   - `mode=auto` + RKAutoClient + มี `rkauto_uuid` source bank active → `CreateWithdrawal()` → บันทึก `rkauto_uuid`, `rkauto_transaction_id`, `rkauto_status='processing'`
   - RKAUTO fail หรือไม่มี source bank → fallback `mode='manual'` + log warning
   - UPDATE status='approved' + `transfer_mode` + `approved_by`
9. **RejectWithdraw**: body `refund` (default true)
   - `refund=true`: `balance += amount` + INSERT transaction `type='refund'`
   - `refund=false` (กรณีทุจริต): ไม่คืน แต่บันทึก `admin_debit` amount=0

## 📋 Rules — RKAUTO Webhook
10. endpoints PUBLIC แต่ป้องกันด้วย `WebhookSecurity` (IP whitelist + HMAC signature + rate limit 100/s)
11. **Idempotency**: เช็ค `deposit_requests.rkauto_uuid` ซ้ำ → return 200 `already_processed` (ไม่ process ซ้ำ)
12. Match: pending + amount exact + สร้าง ≤ 24 ชม. → auto-approve + credit
13. ไม่ match → INSERT `deposit_requests` status `unmatched` member_id=0 ให้แอดมินดู

## 🔄 Flow (approve deposit)
```
PUT /api/v1/deposits/:id/approve
  → เช็ค status=pending + scope
  → tx: UPDATE deposit + balance + INSERT transaction
  → (optional) first-deposit bonus flow
  → tx.Commit
```

## 🌐 API Endpoints
- `GET  /api/v1/deposits` — filter status, date_from, date_to (permission: `finance.deposits`)
- `GET  /api/v1/deposits/:id/logs`
- `PUT  /api/v1/deposits/:id/approve|reject|cancel` (permission: `finance.approve_deposit`)
- `GET  /api/v1/withdrawals` (permission: `finance.withdrawals`)
- `PUT  /api/v1/withdrawals/:id/approve|reject` (permission: `finance.approve_withdraw`)
- `POST /webhooks/rkauto/deposit-notify` / `withdraw-notify` (PUBLIC — secured by middleware)
- `POST /api/v1/bank-accounts/:id/register-rkauto|activate-rkauto|deactivate-rkauto`

## ⚠️ Edge Cases
- สถานะไม่ตรง → 400 with status ปัจจุบัน
- scope mismatch → 403
- Debit refund เกินยอด → rollback + 400
- RKAUTO source bank ไม่มี → fallback manual
- Webhook signature ผิด / IP นอก whitelist → middleware ตัดก่อนเข้า handler

## 🔗 Source of Truth
- Deposit handlers: `internal/handler/stubs.go:2267-2513`
- Withdraw handlers: `internal/handler/stubs.go:2525-2728`
- RKAUTO webhook: `internal/handler/rkauto_webhook.go:33`, `:129`
- RKAUTO client: `internal/rkauto/client.go`, types: `internal/rkauto/types.go`
- Webhook middleware: `internal/middleware/webhook_security.go`
- Router: `internal/handler/router.go:174-191`, `:395-401`

## 📝 Change Log
- 2026-04-20: Initial — approve/reject/cancel, first-deposit bonus, RKAUTO auto withdraw, webhook idempotency
