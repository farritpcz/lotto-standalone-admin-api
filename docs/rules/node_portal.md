# Node Portal (Agent Node Self-Portal) — admin-api

> Last updated: 2026-04-20 (v1 initial — starter rule)
> Related code: `internal/handler/node_portal_handler.go`, `internal/handler/router.go:365-386`

## 🎯 Purpose
Portal สำหรับ Agent Node เจ้าของเว็บลูกสาย — login ด้วย username/password ของ node → ดูสายงาน + CRUD ลูกตรง + ดูกำไรตัวเอง  
**แยกจาก admin portal** — ใช้ JWT cookie ชื่อ `node_token` คนละตัวกับ admin

## 📋 Rules
1. Auth: `mw.NodeJWTAuth()` ตรวจ `node_token` cookie (router.go:377) — คนละ middleware กับ admin
2. Scope: node เห็นได้เฉพาะ **สายงานใต้ตัวเอง** (ตัวเอง + ลูกหลานทั้งหมด)
3. แก้ไข/ลบ ได้เฉพาะ **ลูกตรง** เท่านั้น — หลาน (grandchild+) = read-only
4. Login: match username + bcrypt password ใน `agent_nodes` table, scope ด้วย agent_id ของ domain ที่ยิงมา (multi-tenant)
5. ตั้ง cookie ด้วย `mw.SetNodeTokenCookie` — secure/httponly/samesite ตาม env
6. Create child: validate share% ≤ ของตัวเอง (เหมือน admin CreateDownlineNode แต่ scope ที่ parent_id = caller node)
7. Logout: `ClearNodeTokenCookie` — frontend redirect ไปหน้า login

## 🌐 Endpoints
Public:
- POST `/api/v1/node/auth/login`   → `NodeLogin`
- POST `/api/v1/node/auth/logout`  → `NodeLogout`

Protected (`NodeJWTAuth`):
- GET    `/api/v1/node/me`              → `NodeGetMe` (info + child count + parent)
- GET    `/api/v1/node/tree`            → `NodeGetTree` (subtree ของตัวเอง)
- GET    `/api/v1/node/children`        → `NodeListChildren`
- POST   `/api/v1/node/children`        → `NodeCreateChild` (trigger deploy ถ้ามี domain)
- PUT    `/api/v1/node/children/:id`    → `NodeUpdateChild` (ลูกตรงเท่านั้น)
- DELETE `/api/v1/node/children/:id`    → `NodeDeleteChild` (ลูกตรง + ไม่มีหลาน)
- GET    `/api/v1/node/profits`         → `NodeGetProfits`

## ⚠️ Edge Cases
- พยายามแก้หลาน (ไม่ใช่ลูกตรง) → 403 forbidden
- share% ลูกเกินของตัวเอง → 400 validation
- Login wrong password → 401 generic (ไม่บอกว่า user/pass ผิดอันไหน)
- Node ถูก ban/disable → login ปฏิเสธ

## 🔗 Related
- Admin-side downline CRUD: [downline.md](./downline.md)
- Agent nodes management: [agent_node_management.md](./agent_node_management.md)
- Deploy (เมื่อสร้างลูกพร้อม domain): [deploy.md](./deploy.md)
- Frontend: `lotto-standalone-admin-web/src/app/node/` (login + portal)
- Memories: `downline_scoping.md`, `downline_system.md`

## 📝 Change Log
- 2026-04-20: v1 initial skeleton
