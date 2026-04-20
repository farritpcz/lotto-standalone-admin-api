# Number Bans — admin-api

> Last updated: 2026-04-20 (v1 initial — starter rule)
> Related code: `internal/handler/bans_handler.go`, `internal/handler/autoban_handler.go`, `internal/handler/router.go:136-139,272-279`

## 🎯 Purpose
เลขอั้น (ban numbers) ลดความเสี่ยงเจ้ามือ — admin สามารถอั้น/ลดเรท/จำกัดยอด ต่อ `lottery_round_id × bet_type × number`

## 📋 Rules
1. **Scope per-agent + per-round**: ทุก query filter ด้วย `agent_id` + `lottery_round_id`
2. **Permission**: `lottery.bans` ทุก endpoint
3. **Types ของการอั้น** (ดู `bet_number_limits` table):
   - `full` = อั้นเต็ม (ไม่รับแทง)
   - `partial` = รับแต่ลด payout หรือจำกัดยอดรวม
4. **Auto-ban rules**: threshold-based — backend cron/worker ตรวจยอดรวมต่อเลข → apply ban อัตโนมัติ (`autoban_handler.go`)
5. **Delete = hard delete**: frontend ต้อง ConfirmDialog ก่อน (ห้าม `confirm()`)
6. เลขที่อั้นแล้วมี bet ค้าง → bet นั้นยังอยู่, แต่ห้ามรับเพิ่ม (enforce ใน `bets_handler.go`/member-api place_bet)

## 🌐 Endpoints
### Manual bans
- GET    `/api/v1/bans`         → `ListBans` (filter ด้วย round + bet_type)
- POST   `/api/v1/bans`         → `CreateBan`
- DELETE `/api/v1/bans/:id`     → `DeleteBan`

### Auto-ban rules
- GET    `/api/v1/auto-bans`    → `ListAutoBanRules`
- POST   `/api/v1/auto-bans`    → `CreateAutoBanRule`
- PUT    `/api/v1/auto-bans/:id` → `UpdateAutoBanRule`
- DELETE `/api/v1/auto-bans/:id` → `DeleteAutoBanRule`

## ⚠️ Edge Cases
- สร้าง ban ซ้ำ (number เดียวกัน + bet_type เดียวกัน + round เดียวกัน) → 409 Conflict
- ลบ ban หลังหวยออกผลแล้ว → อนุญาต (เพื่อ cleanup) แต่ไม่กระทบ settled bets
- Auto-ban race condition: ใช้ DB transaction + unique index ป้องกัน double-apply

## 🔗 Related
- Frontend: `lotto-standalone-admin-web/src/app/bans/page.tsx`, `bans/auto/page.tsx`
- Enforce แทง: `lotto-standalone-member-api/internal/handler/bets.go` (check before insert)
- Core: `lotto-core/services/autoban/*`

## 📝 Change Log
- 2026-04-20: v1 initial skeleton
