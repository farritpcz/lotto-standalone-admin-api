# Member Management (สมาชิกฝั่งหน้าบ้าน)

> Last updated: 2026-04-20
> Related code: `internal/handler/stubs.go:486` (ListMembers), `:500` (GetMember), `:567` (UpdateMember), `:605` (AdjustMemberBalance), `:640` (UpdateMemberStatus)
> Related migrations: `migrations/001_initial_schema.sql` (members), `migrations/002_multi_agent.sql`, `migrations/020_absorb_agents_into_nodes.sql`

## 🎯 Purpose
จัดการสมาชิกที่สมัครผ่านหน้าบ้าน (เว็บเดิมต่อ 1 agent_node_id): ดูรายการ, แก้ข้อมูล, reset password, freeze/unfreeze, ปรับยอดเงิน (credit/debit) — ทุก action จะถูก scope ตาม Node และลง audit log

## 📋 Rules
1. ทุก query ผ่าน `mw.GetNodeScope(c, h.DB)` — node เห็นเฉพาะ `members` ที่ `agent_node_id = nodeID` (หรือ children ของตัวเอง), admin เห็นของ root node
2. สมาชิกมี `status`: `active` / `frozen` / `suspended` / `deleted`
3. ถอน/แทง/ถอน balance ใด ๆ ใช้ atomic SQL (`UPDATE members SET balance = balance + ?`) — ห้ามคำนวณใน Go แล้ว save
4. AdjustMemberBalance = credit/debit manual ต้องบันทึก `transactions` พร้อม `balance_before`, `balance_after`, `note`, `reference_type`
5. การ freeze → `status='frozen'` — หน้าบ้าน login/แทงไม่ได้ (member-api เช็คเอง)
6. Reset password → bcrypt hash ใหม่, ไม่ควรส่ง password ใหม่กลับใน response
7. Permission ต้องมี: `members.view` (อ่าน), `members.edit` (แก้), `members.status` (freeze/unfreeze), `members.adjust_balance` (ปรับยอด)
8. ห้าม hard delete — ใช้ `status='deleted'` แทน
9. Node Portal (หัวสาย) ไม่มี endpoint members CRUD ตรง — เห็นผ่าน report เท่านั้น

## 🔄 Flow (adjust balance)
```
PUT /api/v1/members/:id/balance  { amount, type: credit|debit, note }
  → scope.HasMember(id) → ถ้าไม่ใช่สมาชิกในสาย → 403
  → tx.Begin
  → atomic UPDATE members SET balance = balance ± amount (WHERE balance >= amount ถ้า debit)
  → INSERT transactions (type=admin_credit|admin_debit, balance_before/after, note)
  → tx.Commit + AuditLog
```

## 🌐 API Endpoints
- `GET  /api/v1/members` — pagination + filter (username, status, level)
- `GET  /api/v1/members/:id` — รายละเอียด
- `PUT  /api/v1/members/:id` — แก้ name, phone, bank, password
- `PUT  /api/v1/members/:id/status` — freeze / activate / suspend
- `PUT  /api/v1/members/:id/balance` — credit / debit manual

## ⚠️ Edge Cases
- Debit เกินยอด → `RowsAffected == 0` → rollback + 400 `ยอดเงินไม่เพียงพอ`
- scope.HasMember(id) = false → 403
- status = deleted → ยัง query เจอได้ด้วย flag `include_deleted` (admin เท่านั้น)
- ไม่มี field ให้แก้ → 400 `no fields to update`

## 🔗 Source of Truth
- Handlers: `internal/handler/stubs.go:486-660`
- Model: `internal/model/models.go:53` (Member)
- Scope: `internal/middleware/node_scope.go` (GetNodeScope, HasMember, ScopeByNodeID)
- Router: `internal/handler/router.go:110-114`

## 📝 Change Log
- 2026-04-20: Initial — CRUD, status, atomic balance, scope rule
