# Admin Authentication & Role / Permission

> Last updated: 2026-04-20
> Related code: `internal/handler/stubs.go:73`, `internal/middleware/auth.go:34`, `internal/middleware/permission.go`, `internal/middleware/csrf.go`
> Related migrations: `migrations/001_initial_schema.sql` (admins), `migrations/020_absorb_agents_into_nodes.sql`

## 🎯 Purpose
ระบบเข้าสู่ระบบของหลังบ้าน (admin + staff + node user) + ออก JWT ใส่ httpOnly cookie + ตรวจสิทธิ์ permission รายเมนู ก่อนเข้าถึง route ที่ปลอดภัย

## 📋 Rules
1. login ใช้ตาราง `admins` ก่อน ถ้าไม่เจอ fallback ไปตาราง `agent_nodes` (หัวสาย/ปล่อยสาย)
2. password เก็บด้วย `bcrypt.DefaultCost` — เทียบด้วย `bcrypt.CompareHashAndPassword`
3. ถ้า `admins.status != "active"` → 403 `account suspended`
4. JWT เซ็นด้วย HS256, secret จาก `ADMIN_JWT_SECRET` (production ห้ามใช้ default)
5. Token อายุ = `ADMIN_JWT_EXPIRY_HOURS` ชม. (default config)
6. ส่งกลับเป็น httpOnly cookie `admin_token` + `csrf_token` (double-submit) — client ต้องแนบ header `X-CSRF-Token` ทุก mutating request
7. Role ที่ใช้: `owner`, `admin`, `operator`, `viewer`, และ `node` (login ผ่าน agent_nodes)
8. ถ้า `admins.agent_node_id IS NOT NULL` → token ใช้ role `"node"` เพื่อเข้า NodeScope (เห็นเฉพาะข้อมูลเว็บนั้น)
9. ทุก protected route ผ่าน 3 middleware เรียงลำดับ: `AdminJWTAuth` → `CSRFProtect` → `AuditLog` → `RequirePermission(<scope>)`
10. `permissions` เก็บเป็น JSON array เช่น `["members.view","finance.deposits"]` — ถ้าไม่มี permission ที่ต้อง → 403
11. การเปลี่ยน role คนอื่น: owner เปลี่ยนได้ทุก role, admin ให้ได้แค่ `operator|viewer`
12. บันทึกทุก login (สำเร็จ/ล้มเหลว) ลง `admin_login_history` — pakit `ip`, `user_agent`, `success`

## 🔄 Flow
```
POST /api/v1/auth/login {username, password}
  → เช็ค admins → ถ้าไม่เจอ เช็ค agent_nodes
  → bcrypt compare → สถานะ active
  → GenerateAdminToken (HS256)
  → SetAdminTokenCookie + SetCSRFCookie
  → response { admin, token, permissions, user_type, node_id? }

ทุก request ต่อไป:
  httpOnly cookie admin_token → AdminJWTAuth parse claims
  → Set c.admin_id / admin_username / admin_role / is_node_user
  → CSRFProtect → AuditLog (POST/PUT/DELETE เท่านั้น) → RequirePermission
```

## 🌐 API Endpoints
- `POST /api/v1/auth/login` — login (admin หรือ agent_node)
- `POST /api/v1/auth/logout` — ล้าง cookie `admin_token` + `csrf_token`
- `POST /api/v1/node/auth/login` / `logout` — login แยกสำหรับ Node Portal (JWT cookie `node_token`)

## ⚠️ Edge Cases
- production + JWT secret = default → `log.Fatal` ตอน boot (main.go:41)
- ไม่มี cookie + ไม่มี `Authorization: Bearer` → 401 `missing authentication token`
- Token หมดอายุ / signature ผิด → 401 `invalid or expired token`
- login ผ่าน agent_node → ออก 2 token พร้อมกัน (`node_token` + `admin_token` role=`node`)
- ห้ามลบตัวเอง (DeleteStaff: `id == adminID` → 400)

## 🔗 Source of Truth
- Handler login/logout: `internal/handler/stubs.go:73`, `:171`
- JWT middleware: `internal/middleware/auth.go:34`
- Token generator: `internal/middleware/auth.go:78`
- Cookie helper: `internal/middleware/cookie.go`
- CSRF: `internal/middleware/csrf.go`
- Permission check: `internal/middleware/permission.go`
- Audit log: `internal/middleware/auditlog.go:20`
- Router wire-up: `internal/handler/router.go:95-103`
- Admin model: `internal/model/models.go:14`
- Login history: `internal/model/models.go:30`

## 📝 Change Log
- 2026-04-20: Initial — สรุปกฏ login 2 แหล่ง, JWT cookie, role hierarchy, permission check
