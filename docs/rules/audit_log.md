# Audit Log (Activity Log + Admin Action Log)

> Last updated: 2026-04-20
> Related code: `internal/middleware/auditlog.go:20`, `internal/handler/logs.go`
> Related migrations: `migrations/011_admin_action_logs.sql`, `migrations/001_initial_schema.sql` (activity_logs)

## 🎯 Purpose
บันทึกทุก mutating action ของแอดมิน/staff เพื่อ trace ย้อนหลัง — มี 2 ตาราง:
1. `activity_logs` — generic audit ทุก POST/PUT/DELETE (อัตโนมัติผ่าน middleware)
2. `admin_action_logs` — เจาะจง action สำคัญ (เช่น `yeekee_manual_settle`) พร้อม `target_type`/`target_id`/`details JSON`

## 📋 Rules
1. `AuditLog` middleware ติดที่ group `protected` → ครอบทุก protected route
2. **ข้าม** `GET`, `OPTIONS`, `HEAD` (ไม่บันทึก)
3. Request body เก็บได้ไม่เกิน 2KB — ถ้าเกิน suffix `...(truncated)`
4. ต้อง restore `c.Request.Body` หลังอ่าน (ใช้ `bytes.NewBuffer`) — ห้าม consume body จน handler อ่านไม่ได้
5. INSERT หลัง handler เสร็จ (`c.Next()`) → รู้ status code ก่อน
6. เป็น fire-and-forget (goroutine) — ไม่ block response
7. ฟิลด์ `activity_logs`: `admin_id`, `method`, `path`, `request_body`, `status_code`, `ip_address`, `created_at`
8. ฟิลด์ `admin_action_logs`: `admin_id`, `action`, `target_type`, `target_id`, `details JSON`, `ip`, `created_at`
9. `admin_action_logs` ใช้ INSERT ตรงจาก handler เฉพาะจุด (ไม่ผ่าน middleware) — เช่น void round, manual settle yeekee
10. ค้นดูผ่าน `/api/v1/staff/:id/activity` และ `/staff/:id/login-history` (เฉพาะ staff ในเว็บตัวเอง)

## 🔄 Flow
```
POST/PUT/DELETE /api/v1/*
  → middleware อ่าน body (≤2KB) → restore body → c.Next()
  → หลัง handler: goroutine INSERT activity_logs (admin_id, method, path, body, status, ip)

handler พิเศษ:
  → INSERT admin_action_logs(action='yeekee_manual_settle', target_type='yeekee_round', target_id=?, details=JSON)
```

## 🌐 API Endpoints (อ่าน log)
- `GET /api/v1/staff/:id/login-history` — 50 ล่าสุด (`admin_login_history`)
- `GET /api/v1/staff/:id/activity` — 50 ล่าสุด (`activity_logs`)
- `GET /api/v1/bets/:id/logs` — log ของ bet
- `GET /api/v1/deposits/:id/logs`
- `GET /api/v1/withdrawals/:id/logs`
- _General activity feed endpoint_: Status: 🚧 planned — ยังไม่มี endpoint global (/admin-action-logs) — dashboard ยังต้อง query เองผ่าน DB

## ⚠️ Edge Cases
- Body ว่าง → `bodyStr=""` (ไม่ error)
- admin_id = 0 (guest/public webhook) → ยังบันทึกได้
- DB down ตอน INSERT → goroutine ล้มเงียบ ๆ (ไม่กระทบ response)
- utf8mb4 บังคับ (ข้อความไทยใน request_body) — ถ้า DB ไม่ใช่ utf8mb4 จะ mojibake

## 🔗 Source of Truth
- Middleware: `internal/middleware/auditlog.go:20` (AuditLog)
- Router wire-up: `internal/handler/router.go:103`
- Staff log handlers: `internal/handler/stubs.go:3224-3267`
- Admin action log migration: `migrations/011_admin_action_logs.sql`
- Activity logs migration: `migrations/001_initial_schema.sql`

## 📝 Change Log
- 2026-04-20: Initial — activity_logs middleware + admin_action_logs จุดเฉพาะ; global feed endpoint marked planned
