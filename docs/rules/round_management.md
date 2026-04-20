# Round Management (เปิด/ปิด/ออกผลรอบหวย)

> Last updated: 2026-04-20
> Related code: `internal/handler/stubs.go:767` (list/create/status), `:804` (ManualOpen), `:817` (ManualClose), `:831` (Void), `:871` (Preview), `:1022` (SubmitResult); `internal/service/round_svc.go`, `internal/service/result_service.go`; `internal/job/` (round lifecycle)
> Related migrations: `migrations/001_initial_schema.sql` (lottery_rounds), `migrations/015_fix_lottery_rounds_unique_key.sql`, `migrations/019_lottery_per_node.sql`

## 🎯 Purpose
จัดการวงจรชีวิตรอบหวย (upcoming → open → closed → resulted / voided) + กรอกผลรางวัลและ settle bet ผ่าน `lotto-core/payout` — รอบหวยหลักใช้ร่วมทุกเว็บ (ผลเดียวกันทั้งระบบ), สถานะเปิด/ปิดแยกต่อ node ผ่าน `agent_round_config`

## 📋 Rules
1. สถานะรอบ: `upcoming` → `open` → `closed` → `resulted` | `voided`
2. สร้างรอบ (`CreateRound`) เริ่มที่ `upcoming` เสมอ — จะถูก Job `StartRoundLifecycleJob` เปลี่ยนสถานะตามเวลา
3. ManualOpen/ManualClose เรียก `RoundService` เพื่อ validate เวลา + transition ที่ถูกต้อง
4. Void ต้องใส่ `reason` (default `"ยกเลิกโดยแอดมิน"`) — refund ทุก bet + หักรางวัลคืน (ทำใน service)
5. SubmitResult: ต้องกรอก `top3`, `top2`, `bottom2` (required), `front3`, `bottom3` optional
6. ถ้า `round.status == "resulted"` → 400 `"round already has result"` (ห้ามออกผลซ้ำ)
7. ออกผลแล้ว:
   - UPDATE `lottery_rounds.status='resulted'`, เซ็ต `result_*` + `resulted_at`
   - ดึง bets `status=pending` → แปลงเป็น `coreTypes.Bet` → `payout.SettleRound()` (lotto-core)
   - update แต่ละ bet (`won`/`lost` + `win_amount` + `settled_at`)
   - เครดิตรางวัล: `GroupWinnersByMember` → atomic `balance = balance + totalWin` + INSERT transaction `type='win'`
   - goroutine: `CalculateCommissions` + `CalculateDownlineProfits`
8. Preview ไม่บันทึกอะไร — แค่คำนวณใครถูก เท่าไรกลับไป frontend
9. Permission ต้องมี `lottery.create` สำหรับ mutating (open/close/void/result), `lottery.view` สำหรับ GET
10. `ListSchedules` คืน `job.GetDefaultSchedules(db)` — ตารางสร้างรอบอัตโนมัติ (อ่านจาก DB ตั้งแต่ migration 025)

### ⭐ Auto-Create Rounds (since 2026-04-20)
11. Cron `StartRoundLifecycleJob` สร้างรอบล่วงหน้า **30 วัน** ทุก 1 ชม. (เดิม 7 วัน)
12. อ่าน schedule จาก `lottery_types.schedule_config` JSON — ไม่ hard-code ใน Go
13. Schema JSON: `{"day_type": "daily|weekday|thai_gov", "open_time": "HH:MM", "close_time": "HH:MM"}`
14. `schedule_config = NULL` (เช่น YEEKEE) → **ข้าม** ไม่ auto-create (ยี่กีใช้ cron แยกที่ member-api)
15. agent_node_id = NULL (global — ทุก agent ใช้รอบเดียวกัน) ตามกฏ `lotto-core/docs/rules/multi_agent_scoping.md`
16. Unique: `(lottery_type_id, round_number)` → re-run cron ซ้ำได้ (duplicate ถูก skip)
17. `close_time < open_time` → ปิดข้ามวัน (บวก 1 วันให้ close_time) — ใช้กับ DJ 20:30→03:00

## 🔄 Flow (submit result)
```
POST /api/v1/results/:roundId  { top3, top2, bottom2, front3?, bottom3? }
  → First round — ถ้าไม่พบ 404
  → ถ้า status=resulted → 400
  → UPDATE lottery_rounds (result + status=resulted + resulted_at)
  → ดึง bets pending ของรอบนี้
  → payout.SettleRound() (lotto-core)
  → UPDATE แต่ละ bet (won/lost, win_amount)
  → credit winners (atomic) + INSERT transactions type=win
  → go CalculateCommissions + CalculateDownlineProfits
  → return { total_bets, settled, total_winners, total_win, total_profit }
```

## 🌐 API Endpoints
- `GET  /api/v1/rounds` — list + filter status/lottery_type_id
- `POST /api/v1/rounds` — สร้าง
- `PUT  /api/v1/rounds/:id/status` — เปลี่ยน status ตรง (ใช้น้อย)
- `PUT  /api/v1/rounds/:id/open|close|void`
- `GET  /api/v1/rounds/schedules`
- `POST /api/v1/results/:roundId/preview`
- `POST /api/v1/results/:roundId` — submit result + settle
- `GET  /api/v1/results`

## ⚠️ Edge Cases
- `RoundService == nil` → 500 `"round service not configured"`
- `SubmitResult` แต่ไม่มี bet pending → return total_bets=0 (ไม่ error)
- Void รอบ resulted → ต้องคืนรางวัลที่จ่ายไปแล้วด้วย (ทำใน `svc.VoidRound`)
- Bet type code ไม่รู้จัก → ข้าม (ไม่ถูกคำนวณ)

## 🔗 Source of Truth
- Handlers: `internal/handler/stubs.go:767-1176`
- Service: `internal/service/round_svc.go`, `internal/service/result_service.go`
- Lifecycle job: `internal/job/` (StartRoundLifecycleJob, StartAutoResultJob)
- Router: `internal/handler/router.go:123-134`
- Core payout: `lotto-core` package (`SettleRound`, `GroupWinnersByMember`)

## 📝 Change Log
- 2026-04-20: Initial — สรุป lifecycle, validation SubmitResult, settle flow ผ่าน lotto-core
- 2026-04-20: Auto-create refactor — ย้าย schedule hard-code → `lottery_types.schedule_config` (migration 025), window 7→30 วัน, `GetDefaultSchedules(db)` อ่าน DB
