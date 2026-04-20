# Bets (Admin) — admin-api

> Last updated: 2026-04-20 (v1 initial — starter rule)
> Related code: `internal/handler/bets_handler.go`, `internal/handler/router.go:145-153`

## 🎯 Purpose
Admin ดูรายการแทง (list), ดู bill-level, cancel bet/bill, + audit log ต่อ bet — สำหรับ customer support + dispute

## 📋 Rules
1. **Scope per-agent**: ทุก query filter `agent_id`; members ที่อยู่ใต้ downline ของ admin เห็นได้ตาม scope
2. **Permission**: `finance.bets` ทุก endpoint (group level)
3. **Bill-level structure**: 1 bill (`bet_batch_id`) มีหลาย bet — cancel bill = cancel ทุก bet ใน batch
4. **Cancel bet**:
   - ต้องเกิด**ก่อน** round ออกผล
   - Refund ยอดกลับ wallet + บันทึก transaction type `bet_refund`
   - หลัง settled แล้ว → reject 400 (ยกเว้นมีสิทธิ์ `finance.cancel_settled` — ยังไม่มี)
5. **Cancel reason**: required; บันทึก `admin_id`, `reason`, `timestamp` (audit log)
6. **Bet logs**: แสดงทุก state change ของ bet (placed → settled/cancelled/refunded)

## 🌐 Endpoints
- GET `/api/v1/bets`                      → `ListAllBets` (filter: member, lottery_type, round, status, date range)
- GET `/api/v1/bets/bill/:batchId`        → `GetBillDetail`
- PUT `/api/v1/bets/bill/:batchId/cancel` → `CancelBill`
- GET `/api/v1/bets/:id/logs`             → `GetBetLogs`
- PUT `/api/v1/bets/:id/cancel`           → `CancelBet`

## ⚠️ Edge Cases
- Cancel bet ของรอบที่กำลัง settle อยู่ → race — ใช้ `SELECT ... FOR UPDATE` กัน double-settle
- Member ย้าย downline ระหว่างทาง → bet เก่ายังเห็นโดย admin ใต้สายเดิม (lock ที่ place_bet time)
- Bill ที่ cancel บางส่วนก่อนหน้า → ทำ bill-cancel ซ้ำได้ (idempotent: skip bets ที่ cancelled แล้ว)

## 🔗 Related
- Frontend: `lotto-standalone-admin-web/src/app/bets/page.tsx`
- Member-side place/get bets: `lotto-standalone-member-api/internal/handler/bets.go`
- Audit: `audit_log.md`

## 📝 Change Log
- 2026-04-20: v1 initial skeleton
