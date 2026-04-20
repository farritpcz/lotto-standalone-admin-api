# Downline (ระบบปล่อยสาย) — admin-api

> Last updated: 2026-04-20 (v1 initial — starter rule, GetDownlineReport restored)
> Related code: `internal/handler/downline_handler.go`, `internal/handler/router.go:310-330`

## 🎯 Purpose
ระบบปล่อยสายงาน (admin → share_holder → senior → master → agent → agent_downline) — จัดการ tree, commission per lottery type, กำไรสุทธิ, รายงานเคลียสาย

## 📋 Rules
1. Scope per-agent: filter `agent_id` เสมอ + scope ตาม subtree ของ caller (เห็นเฉพาะลูกหลานตัวเอง)
2. Permissions: ทุก route ใน `/downline/*` ต้องผ่าน admin JWT + permission check (router.go:310+)
3. Tree depth: admin (root) = 0, ลูกเพิ่ม +1 — จำกัด max depth ตาม business (ดู memory `downline_system.md`)
4. `share_percent` ของลูกต้อง ≤ share ของพ่อ (validation ใน Create/Update)
5. ลบ node ต้องไม่มีลูก (cascade = ห้าม — อธิบาย UX ให้ user)
6. Commission settings ต่อประเภทหวย (per lottery_type) — stored ใน `agent_node_commissions` table
7. Profit calc walk ขึ้น tree — ดู memory `downline_profit_calc.md` สำหรับ breakdown 3 ยอด
8. Create node พร้อม domain → trigger deploy (ดู [deploy.md](./deploy.md))

## 🌐 Endpoints
- GET    `/api/v1/downline/tree`                     → `GetDownlineTree` — คืน tree ทั้งสายของ caller
- GET    `/api/v1/downline/nodes`                    → `ListDownlineNodes` — flat list
- GET    `/api/v1/downline/nodes/:id`                → `GetDownlineNode`
- POST   `/api/v1/downline/nodes`                    → `CreateDownlineNode` (trigger deploy + CF zone)
- PUT    `/api/v1/downline/nodes/:id`                → `UpdateDownlineNode`
- DELETE `/api/v1/downline/nodes/:id`                → `DeleteDownlineNode`
- GET    `/api/v1/downline/nodes/:id/commission`     → `GetNodeCommissionSettings`
- PUT    `/api/v1/downline/nodes/:id/commission`     → `UpdateNodeCommissionSettings`
- GET    `/api/v1/downline/profits`                  → `GetDownlineProfits` — ของ caller
- GET    `/api/v1/downline/profits/:nodeId`          → `GetNodeProfits`
- GET    `/api/v1/downline/report`                   → `GetDownlineReport` ⭐ รายงานเคลีย (เพิ่ง restore)

## 📐 Report Formulas — ดู memory `downline_report_formulas.md`
ยืนยันถูกต้องแล้ว 2026-04-06:

- **เคลียใต้สาย** = `child_tree_net × (100 − child_share%) / 100`
- **เคลียหัวสาย** = `total_tree_net × (100 − my_share%) / 100`
- **กำไรสุทธิ**    = กำไรเว็บตัวเอง + (เคลียใต้สาย − ส่งต่อขึ้นหัว)
  - เว็บตัวเอง = `direct_net × my_share% / 100`
  - ส่วนต่างใต้สาย = `child_tree_net × diff% / 100` (diff = my% − child%)

**Key**: ใช้ leaf records (`child_percent = 0`) ใน `agent_profit_transactions` เพื่อนับ `net_result` ไม่ซ้ำ
— `child_percent = 0` = bet จาก member ที่สังกัด node นั้นตรง ๆ

## ⚠️ Edge Cases
- Node ไม่มี profit records → ทุก field = 0 (ไม่ error)
- share% = 100 → เคลียขึ้นหัว = 0 (ถือเอง 100%)
- Walk up: loop prevention (parent_id chain, หยุดเมื่อ parent_id IS NULL)
- Orphan node (parent ถูกลบ) → ไม่ควรเกิด (FK) — ถ้าเกิด ให้ log + skip

## 🔗 Related
- Frontend: `lotto-standalone-admin-web/src/app/downline/` (report, tree, profits)
- API client: `lotto-standalone-admin-web/src/lib/api.ts` → `downlineApi`
- Node portal (portal สำหรับ agent-node เอง): [node_portal.md](./node_portal.md)
- Agent nodes CRUD: [agent_node_management.md](./agent_node_management.md)
- Deploy: [deploy.md](./deploy.md)
- Member levels / commission: [member_levels.md](./member_levels.md)
- Memories: `downline_system.md`, `downline_scoping.md`, `downline_profit_calc.md`, `downline_report_formulas.md`

## 📝 Change Log
- 2026-04-20: v1 initial skeleton — GetDownlineReport restored, formulas embedded
