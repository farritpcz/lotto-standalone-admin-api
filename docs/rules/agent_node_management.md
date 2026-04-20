# Agent Node Management (สายงาน / Downline Tree)

> Last updated: 2026-04-20
> Related code: `internal/handler/downline_handler.go`, `internal/handler/node_portal_handler.go`, `internal/handler/deploy.go`
> Related migrations: `migrations/016_agent_downline.sql`, `migrations/020_absorb_agents_into_nodes.sql`

## 🎯 Purpose
ระบบปล่อยสาย (hierarchical profit sharing): สร้าง/แก้/ลบ node ใน tree (`agent_nodes`) โดย 1 root node = 1 เว็บหวย (1 domain) — ลูกในสายได้กำไรจากส่วนต่าง `share_percent` กับหัวสาย

## 📋 Rules
1. โครงสร้าง tree: `admin → share_holder → senior → master → agent → agent_downline` (`model.RoleHierarchy`)
2. ทุก node เก็บ `parent_id`, `path` (`/1/4/9/`), `depth`, `share_percent` (0..100)
3. **ลูก < พ่อ**: `share_percent` ของลูกต้องน้อยกว่า parent (`downline_handler.go:332`)
4. **ลูกทุกคนต้อง <** `share_percent` ของตัวเอง ตอนแก้ (validate ที่ UpdateDownlineNode)
5. Role ของลูกต้องต่ำกว่าหรือเท่ากับ parent (`agent_downline` ซ้อนได้ไม่จำกัด)
6. `username` unique — ถ้าซ้ำ → 400 `"username ซ้ำ"`
7. `domain` + `code` unique ถ้าระบุ → ต้อง lowercase + trim + regex `validDomainRegex`
8. ลบ node ห้ามลบถ้ามีลูก (ต้องลบลูกก่อน)
9. Password ต้อง bcrypt — เปลี่ยนเองได้ผ่าน UpdateDownlineNode
10. การสร้าง node พร้อม `domain` → trigger `DeployNginxConfig` + (ถ้ามี CF) `DeployCloudflareZone`
11. Node login ผ่าน `/api/v1/node/auth/login` (แยกจาก admin) — เห็นทั้งสายงาน, แก้ได้เฉพาะ "ลูกตรง" เท่านั้น, หลาน = read-only

## 🔄 Flow (create)
```
POST /api/v1/downline/nodes
  → validate parent (share_percent < parent, role ถูกลำดับ)
  → validate domain/code uniqueness
  → bcrypt hash password
  → INSERT agent_nodes (temp path)
  → UPDATE path = parent.path + "/{ID}/"
  → applyThemeFromDB (สีตามธีม)
  → ถ้ามี domain:
      DeployNginxConfig → ไฟล์ /etc/nginx/sites-enabled/{domain}.conf + nginx -t + reload
      ถ้ามี CFClient → DeployCloudflareZone → zone + A record (proxied) + SSL flexible
      UPDATE cf_zone_id
  → return { node, deploy }
```

## 🌐 API Endpoints (admin scope)
- `GET  /api/v1/downline/tree` — tree ทั้งหมด
- `GET  /api/v1/downline/nodes` / `:id`
- `POST /api/v1/downline/nodes` — สร้าง node + auto deploy
- `PUT  /api/v1/downline/nodes/:id` — แก้ name/share_percent/phone/password/site_name/theme
- `DELETE /api/v1/downline/nodes/:id`
- `GET/PUT /api/v1/downline/nodes/:id/commission` — ตั้ง % คอมแยกประเภทหวย

## 🌐 API Endpoints (node portal)
- `GET  /api/v1/node/me` / `tree` / `children` / `profits`
- `POST /api/v1/node/children` — สร้างลูกตรง
- `PUT/DELETE /api/v1/node/children/:id` — เฉพาะลูกตรง (ห้ามแก้หลาน)

## ⚠️ Edge Cases
- `share_percent >= parent` → 400
- `share_percent <= 0` → 400
- domain format ผิด → `validDomain` false → DeployResult.Success=false
- nginx -t fail → rollback ลบไฟล์ config ที่สร้าง
- CF deploy fail → fallback เป็น `server_ip` (ชี้ DNS ตรง)
- Node พยายามแก้หลาน → 403

## 🔗 Source of Truth
- Admin downline CRUD: `internal/handler/downline_handler.go:45` (tree), `:267` (create), `:469` (update), `:618` (delete)
- Node portal: `internal/handler/node_portal_handler.go:47` (login), `:305` (create child)
- Auto deploy: `internal/handler/deploy.go:99` (nginx), `:182` (cloudflare)
- Role hierarchy: `internal/model/models.go:350` (AgentNode), `NextRole`, `RoleHierarchy`
- Router: `internal/handler/router.go:305-326`, `:366-382`
- Migration: `migrations/016_agent_downline.sql`

## 📝 Change Log
- 2026-04-20: Initial — สรุปกฏ tree, share_percent constraint, auto deploy flow
