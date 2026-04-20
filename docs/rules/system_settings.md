# System Settings (Key-Value + Per-Node Override)

> Last updated: 2026-04-20
> Related code: `internal/handler/stubs.go:1855` (GetSettings), `:1872` (UpdateSettings), `:1782` (AgentTheme)
> Related migrations: `migrations/001_initial_schema.sql` (settings), `migrations/018_per_node_settings.sql`

## 🎯 Purpose
ระบบ config แบบ key-value ใน DB (`settings`) ใช้เก็บค่าที่แอดมินเปลี่ยนได้ระหว่าง runtime — รองรับ override ต่อ node ผ่าน key prefix `node_{nodeID}_`

## 📋 Rules
1. ตาราง `settings (key PK, value)` — utf8mb4 / utf8mb4_unicode_ci
2. **Per-node override**: key ของ node ต้องขึ้นต้นด้วย `node_{nodeID}_` เสมอ — ถ้า client ส่งมาไม่มี prefix → handler เติมให้เอง (UpdateSettings)
3. GetSettings:
   - node → `WHERE key NOT LIKE 'node_%' OR key LIKE 'node_{myID}_%'`
   - admin → อ่านทุก key
4. UpdateSettings = upsert ต่อคู่ key/value (ถ้ามี → update value, ไม่มี → insert)
5. Theme (agent branding): เก็บใน `agent_nodes` (ไม่ใช่ settings) — fields `theme_primary_color`, `theme_secondary_color`, `theme_bg_color`, `theme_accent_color`, `theme_card_gradient1`, `theme_card_gradient2`, `theme_nav_bg`, `theme_header_bg`, `theme_version`
6. อัพเดท theme → `theme_version += 1` → หน้าบ้าน refetch เมื่อ version ไม่ตรง
7. node แก้ theme ของระบบไม่ได้ → 403 `"node ไม่สามารถแก้ไข theme ของระบบได้"`
8. Permission: `system.settings` สำหรับ settings, `system.cms` สำหรับ agent theme
9. Known settings keys (ตัวอย่าง): `first_deposit_bonus_enabled|_percent|_max|_turnover`, `ticker_text`, EasySlip config keys, referral/affiliate keys

## 🔄 Flow (update node setting)
```
PUT /api/v1/settings  { "welcome_msg": "สวัสดี" }
  → scope.IsNode → actualKey = "node_{id}_welcome_msg"
  → upsert settings(key=actualKey, value="สวัสดี")
```

## 🌐 API Endpoints
- `GET /api/v1/settings` — ดู settings ที่ scope เห็น
- `PUT /api/v1/settings` — upsert key-value (object)
- `GET /api/v1/agent/theme`
- `PUT /api/v1/agent/theme` — bump theme_version
- `GET /api/v1/themes` — รายการธีมสำเร็จรูป (global)

## ⚠️ Edge Cases
- Key ซ้ำใน request body → ใช้ค่าล่าสุด (Go map behavior)
- value ขนาดใหญ่ → ต้อง utf8mb4 — เช็ค encoding ในทุก INSERT
- Theme record ของ root node ไม่มี → 404 `"agent not found"`

## 🔗 Source of Truth
- Handlers: `internal/handler/stubs.go:1782-1900`
- Model: `internal/model/models.go:182` (Setting)
- Router: `internal/handler/router.go:163-172`
- Migration override: `migrations/018_per_node_settings.sql`

## 📝 Change Log
- 2026-04-20: Initial — key-value, node prefix override, theme versioning
