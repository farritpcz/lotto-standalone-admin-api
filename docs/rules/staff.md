# Staff — admin-api

> Last updated: 2026-04-20 (v1 initial — starter rule)
> Related code: `internal/handler/staff_handler.go`, `internal/handler/permissions_handler.go`, `internal/handler/router.go:295-306`

## 🎯 Purpose
Staff = user admin ระดับล่างใต้สายเดียวกัน (ไม่ใช่ owner) — owner/senior admin สร้าง staff, กำหนด permission, ดู login history + activity

## 📋 Rules
1. **Scope per-agent**: staff ผูก `agent_id` เดียวกับผู้สร้าง — เห็นได้เฉพาะ staff ใน agent เดียวกัน
2. **Permission**: `system.staff` (group level)
3. **Staff = admin user แบบจำกัดสิทธิ์**: ใช้ JWT เดียวกันกับ owner แต่ permission map ต่าง (ดู `admin_auth.md`)
4. **Permissions เป็นรายการ** (ดู `GetAvailablePermissions`): `finance.*`, `lottery.*`, `system.*`, `dashboard.*` — admin สร้างกลุ่มแล้วจัด role
5. **Status**: active / suspended — `UpdateStaffStatus` toggle ได้ แทนการลบ
6. **Delete = soft delete**: ตั้ง status=deleted + เก็บ audit (bet/activity history ยังเข้าถึงได้)
7. **Login history**: บันทึก IP + user-agent + timestamp ทุก login (ดู `audit_log.md`)
8. **ห้าม demote owner**: staff role เปลี่ยนเป็น owner ไม่ได้ ผ่าน API นี้ (ต้อง DB migration)

## 🌐 Endpoints
- GET    `/api/v1/staff`                     → `ListStaff`
- GET    `/api/v1/staff/permissions`         → `GetAvailablePermissions` (catalog)
- POST   `/api/v1/staff`                     → `CreateStaff`
- PUT    `/api/v1/staff/:id`                 → `UpdateStaff`
- PUT    `/api/v1/staff/:id/status`          → `UpdateStaffStatus` (active/suspended)
- DELETE `/api/v1/staff/:id`                 → `DeleteStaff`
- GET    `/api/v1/staff/:id/login-history`   → `GetStaffLoginHistory`
- GET    `/api/v1/staff/:id/activity`        → `GetStaffActivity`

## ⚠️ Edge Cases
- Staff ลบตัวเอง → reject (400 "cannot delete self")
- Suspend owner → reject (invariant: ต้องมี owner อย่างน้อย 1)
- Password reset ของ staff → ผ่าน `admin_auth.md` flow (ยังไม่แยก endpoint)

## 🔗 Related
- Frontend: `lotto-standalone-admin-web/src/app/staff/page.tsx`
- Permissions model: `permissions_handler.go`
- Auth: `admin_auth.md`
- Audit: `audit_log.md`

## 📝 Change Log
- 2026-04-20: v1 initial skeleton
