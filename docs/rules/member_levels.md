# Member Levels — ระบบระดับสมาชิก (v3)

> Last updated: 2026-04-20 (v3 rebuild — โละ commission/cashback)
> Related code: `internal/handler/member_levels.go`, `internal/handler/router.go:221-233`
> Related migrations: `../../lotto-standalone-member-api/migrations/026_member_level_v3.sql`
> Cron (recalc): `../../lotto-standalone-member-api/internal/job/member_level_cron.go`

## 🎯 Purpose
จัดระดับสมาชิกเพื่อแสดง **badge/icon** (cosmetic) — **ไม่ผูกกับ commission, cashback, payrate, bonus** ใดๆ
เกณฑ์เดียว: ยอดฝากรวม **rolling 30 วันล่าสุด**

## 📋 Rules

### เกณฑ์ + Window
1. **เกณฑ์**: `min_deposit_30d` (บาท) — ยอดฝากรวม 30 วันล่าสุด ≥ ค่านี้ ผ่านเข้าระดับได้
2. **Window**: Rolling 30 วัน (ไม่ใช่ lifetime, ไม่ใช่ calendar month) → **ตกระดับได้**
3. **Auto-recalc**: ทุกวัน 02:00 น. (Asia/Bangkok) — ดู `member_level_cron.go`
4. **Admin override**: `PUT /members/:id/level` → set + `level_locked=1` (cron ข้าม)
5. **Unlock**: `DELETE /members/:id/level-lock` → ครั้งถัดไปที่ cron รัน จะคำนวณใหม่อัตโนมัติ

### Scope (per-agent)
6. แต่ละ agent (เว็บใต้สาย) มี `member_levels` ของตัวเอง (FK `agent_node_id`)
7. Node admin เห็น/แก้ได้เฉพาะระดับของตัวเอง — root admin เห็น rootNodeID
8. สมาชิกถูกจัดระดับเฉพาะใน scope เดียวกับ `members.agent_node_id`

### Sort order + tier logic
9. `sort_order` น้อย → ต่ำ, มาก → สูง (Bronze=1, Silver=2, Gold=3, ...)
10. **target_level** = max `sort_order` ที่ `min_deposit_30d ≤ member.deposit_30d_cached` ใน scope เดียวกัน + `status='active'`
11. ถ้าไม่มีระดับใดผ่าน threshold → `level_id = NULL` (ยังไม่ถูกจัดระดับ)

### Safety
12. ลบระดับได้**เฉพาะเมื่อ**ไม่มีสมาชิกอยู่ในระดับนั้น (check `COUNT(members WHERE level_id=?)`)
13. Override ต้องระบุ `level_id` ที่อยู่ใน scope เดียวกับ member (ป้องกันข้าม agent)

## 🔄 Flow (Override + Unlock)
```
PUT /members/:id/level   { level_id, note }
  → เช็ค member ใน scope admin
  → เช็ค level_id อยู่ใน scope เดียวกับ member
  → Begin TX:
      UPDATE members SET level_id=?, level_locked=1, level_updated_at=NOW()
      INSERT member_level_history (reason='admin_override', admin_id, note)
  → Commit
  → member จะไม่ถูก cron แตะจนกว่า DELETE /level-lock

DELETE /members/:id/level-lock
  → UPDATE members SET level_locked=0
  → INSERT history (reason='admin_unlock')
  → cron รอบถัดไป (02:00) จะคำนวณ target_level ใหม่ตาม deposit_30d
```

## 🌐 API Endpoints

### CRUD (permission: `system.cms`)
- `GET    /api/v1/member-levels` → `{levels: [...], unassigned: N}`
- `POST   /api/v1/member-levels`
- `PUT    /api/v1/member-levels/:id`
- `DELETE /api/v1/member-levels/:id`
- `PUT    /api/v1/member-levels/reorder` — body: `{orders: [{id, sort_order}]}`

### Per-member ops (permission: `members.edit` / `members.view`)
- `PUT    /api/v1/members/:id/level` — override (body: `{level_id, note}`) + lock
- `DELETE /api/v1/members/:id/level-lock` — unlock (let cron decide)
- `GET    /api/v1/members/:id/level-history` — 100 เหตุการณ์ล่าสุด

## 🗄️ Schema (member-api shared DB)

### `member_levels` (v3)
```
id, agent_node_id, name, color, icon, sort_order,
min_deposit_30d DECIMAL(15,2),   -- ⭐ threshold เดียว
description, status, created_at, updated_at
```

### `members` (เพิ่ม column)
```
level_id BIGINT NULL           -- FK member_levels (soft, no constraint)
level_locked TINYINT(1)        -- 1=admin override (cron ข้าม)
level_updated_at DATETIME
deposit_30d_cached DECIMAL(15,2)  -- cache จาก cron
```

### `member_level_history` (audit)
```
id, member_id, from_level_id, to_level_id,
reason ENUM('auto','admin_override','admin_unlock','initial'),
deposit_30d_snapshot, changed_by_admin_id, note, created_at
```

## ⚠️ Edge Cases
- **สมาชิกใหม่**: level_id=NULL จน cron รันครั้งแรก → history reason='initial'
- **ลบระดับที่มี member**: คืน 400 — admin ต้องย้ายสมาชิกก่อน (ผ่าน override)
- **Override → level NULL**: อนุญาต (ตกระดับ manual) + lock
- **Scope mismatch**: ถ้า admin พยายามเปลี่ยน member ข้าม node → 404
- **Cron ยังไม่รัน + admin ดู report**: `deposit_30d_cached` อาจเก่า 24 ชม. (จดไว้ใน UI help text)
- **v2 → v3 migration**: field เก่า (commission_rate ฯลฯ) ถูก DROP — ถ้ามีโค้ดใดอ้างถึง จะ build fail → ปรับออก

## 🔗 Source of Truth
- Handler: `internal/handler/member_levels.go` (351 lines)
- Routes: `internal/handler/router.go:221-235`
- Migration: `lotto-standalone-member-api/migrations/026_member_level_v3.sql`
- Cron: `lotto-standalone-member-api/internal/job/member_level_cron.go`
- Member view: `lotto-standalone-member-api/internal/handler/member.go:GetMyLevel`

## 📝 Change Log
- 2026-04-20: **v3 rebuild** — โละ commission/cashback/bonus/max_withdraw/min_bets, เพิ่ม `min_deposit_30d`, override + lock + history, cron daily 02:00 Asia/Bangkok
