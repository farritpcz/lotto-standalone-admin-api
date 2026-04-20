# Yeekee (Admin) — admin-api

> Last updated: 2026-04-20 (v1 initial — starter rule)
> Related code: `internal/handler/yeekee_handler.go`, `internal/handler/router.go:284-292`

## 🎯 Purpose
ยี่กี = หวยออนไลน์ความถี่สูง (รอบใหม่ทุก 15 นาที, มี shoots เลขยิงแต่ละนาที) — admin ดู/ตรวจ shoots, manual settle, ตั้งค่าที่เปิดต่อ agent

## 📋 Rules
1. **Scope per-agent**: รอบ + config ผูก `agent_id`
2. **Permission**:
   - `lottery.view` → list/get (rounds, shoots, stats, config)
   - `lottery.create` → settle round + set config
3. **Auto-open**: ยี่กีเปิดอัตโนมัติทุก Agent ผ่าน cron/worker — admin ไม่ต้องสร้างรอบเอง (ดู memory `agent_rules`)
4. **Shoots**: เลขยิงแต่ละนาที — sum ของทุก shoots → ผลรางวัลตอน round ปิด (คำนวณ auto)
5. **Manual settle**: ถ้า auto-settle error (ล่ม/ผิด) admin สามารถ trigger manual settle ผ่าน endpoint นี้
6. **Config per-agent**: จำนวนรอบต่อวัน, ช่วงเวลา, min/max bet — แยกต่อ agent
7. **Manual settle = irreversible**: wrap DB transaction (เหมือน `results_admin.md` rule 4)

## 🌐 Endpoints
- GET  `/api/v1/yeekee/rounds`                → `ListYeekeeRounds`
- GET  `/api/v1/yeekee/rounds/:id`            → `GetYeekeeRoundDetail`
- GET  `/api/v1/yeekee/rounds/:id/shoots`     → `ListYeekeeShoots`
- POST `/api/v1/yeekee/rounds/:id/settle`     → `ManualSettleYeekeeRound`
- GET  `/api/v1/yeekee/stats`                 → `GetYeekeeStats`
- GET  `/api/v1/yeekee/config`                → `GetYeekeeAgentConfig`
- POST `/api/v1/yeekee/config`                → `SetYeekeeAgentConfig`

## ⚠️ Edge Cases
- Round ที่ยัง open อยู่ settle ไม่ได้ → reject 400
- Shoots หายระหว่าง round → fallback คำนวณจาก shoots ที่มี + log warning
- Config เปลี่ยนกลางวัน → มีผลกับรอบถัดไป ไม่กระทบรอบกำลังเปิด

## 🔗 Related
- Frontend: `lotto-standalone-admin-web/src/app/yeekee/page.tsx`, `yeekee/config/page.tsx`, `yeekee/[id]/page.tsx`
- Member-side: `lotto-standalone-member-api/internal/handler/yeekee.go`
- Lottery types: `lotteries_admin.md`

## 📝 Change Log
- 2026-04-20: v1 initial skeleton
