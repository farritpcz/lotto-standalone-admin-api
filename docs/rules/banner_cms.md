# Banner CMS (สไลด์หน้าแรก + Ticker)

> Last updated: 2026-04-20
> Related code: `internal/handler/cms.go:61` (List), `:80` (Create), `:126` (Update), `:182` (Delete), `:207` (Reorder); `internal/handler/upload.go`; `internal/storage/r2.go`
> Related migrations: `migrations/024_cms_banners_node_scope.sql`, `migrations/023_image_uploads_r2.sql`

## 🎯 Purpose
จัดการแบนเนอร์สไลด์หน้าแรก + ตัวอักษรวิ่ง (ticker) ต่อ node — admin เห็น/แก้เฉพาะของ root node ตัวเอง, node เห็น/แก้เฉพาะของ node นั้น

## 📋 Rules
1. `cms_banners` มี `agent_node_id` — NULL ในอดีตหมายถึงระบบกลาง (ปัจจุบันใช้ root node ของ agent เดิม)
2. **Scope**: 
   - node → `WHERE agent_node_id = scope.NodeID`
   - admin → `WHERE agent_node_id = scope.RootNodeID`
3. Create ใช้ `scope.SettingNodeID()` — admin=NULL, node=&nodeID
4. ฟิลด์: `title`, `image_url` (required), `link_url`, `sort_order`, `status` (`active`|`inactive`)
5. ใหม่ → `sort_order = COALESCE(MAX(sort_order), 0) + 1` (อยู่ท้ายสุด)
6. Reorder: body `orders: [{id, sort_order}]` — loop UPDATE ใน transaction พร้อม scope guard
7. Update/Delete: ถ้า `RowsAffected=0` → 404 `"ไม่พบแบนเนอร์นี้หรือไม่มีสิทธิ์"`
8. **Variants sm/md/lg** (status: 🚧 planned — ยังไม่ถูก implement ใน handler): ปัจจุบันเก็บ URL เดียว; frontend แปลง responsive เอง — จะ migrate ไปเก็บ 3 URL เมื่อเชื่อม R2 resize pipeline
9. Upload: `POST /api/v1/upload` → ถ้า R2 config → ส่งไปฝาก Cloudflare R2 + return public URL, ถ้าไม่มี → local filesystem `./uploads/`
10. Ticker เก็บใน `settings` key = `ticker_text` (per-node ใช้ prefix `node_{id}_ticker_text`)
11. Permission: ทุก route ใน `/cms/*` ต้องมี `system.cms`

## 🔄 Flow (create banner)
```
1. Frontend upload รูป: POST /api/v1/upload → ได้ image_url
2. POST /api/v1/cms/banners { title, image_url, link_url }
   → scope → sort_order = max+1 → INSERT with agent_node_id
```

## 🌐 API Endpoints
- `GET    /api/v1/cms/banners`
- `POST   /api/v1/cms/banners`
- `PUT    /api/v1/cms/banners/reorder`
- `PUT    /api/v1/cms/banners/:id`
- `DELETE /api/v1/cms/banners/:id`
- `GET    /api/v1/cms/ticker`
- `PUT    /api/v1/cms/ticker`
- `POST   /api/v1/upload` — อัพโหลดรูป (R2 or local)

## ⚠️ Edge Cases
- node พยายามแก้/ลบของ node อื่น → RowsAffected=0 → 404
- image_url ว่าง → 400
- ไม่มี update field → 400 `"ไม่มีข้อมูลให้อัพเดท"`

## 🔗 Source of Truth
- Handlers: `internal/handler/cms.go:61-250`
- Upload: `internal/handler/upload.go`
- R2 client: `internal/storage/r2.go`
- Router: `internal/handler/router.go:244-254`, `:194`
- Migration scope: `migrations/024_cms_banners_node_scope.sql`

## 📝 Change Log
- 2026-04-20: Initial — CRUD + reorder + node scope; variants sm/md/lg marked planned
