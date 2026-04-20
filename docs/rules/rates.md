# Pay Rates — admin-api

> Last updated: 2026-04-20 (v1 initial — starter rule)
> Related code: `internal/handler/rates_handler.go`, `internal/handler/router.go:141-143`

## 🎯 Purpose
อัตราจ่าย (payout rate) ต่อ `lottery_type × bet_type` — ใช้ตอนคำนวณยอดจ่ายเมื่อ settle

## 📋 Rules
1. **Scope per-agent**: rate แยกตาม `agent_id` — แต่ละเว็บตั้งเองได้
2. **Permission**: `lottery.rates`
3. **Unit**: rate เก็บเป็น "เท่า" (เช่น 95 = จ่าย 95 บาทต่อ 1 บาทที่แทง สำหรับ 3-top 2-digit ที่ 90 etc.); ต้องตรงกับ member-side `BetTypeInfo.rate`
4. **Cannot create/delete**: ไม่มี POST/DELETE — seed ตอน migration/bootstrap แล้ว PUT แก้ rate เดิม
5. **Edit มีผลกับรอบถัดไป**: bet ที่ settle แล้วใช้ rate ณ เวลา placed (lock-in at place_bet time — ดู `bets_handler.go`)
6. **Downline diff%**: rate ของ child ต้องไม่สูงกว่า parent (enforce ที่ level downline — ดู memory `downline_profit_calc`)

## 🌐 Endpoints
- GET `/api/v1/rates`     → `ListRates`
- PUT `/api/v1/rates/:id` → `UpdateRate`

## ⚠️ Edge Cases
- Admin ใต้สายตั้ง rate สูงกว่าหัว → reject (invariant protected)
- แก้ rate ระหว่าง round เปิด → bet ที่แทงไปแล้ว lock rate เดิม (ไม่กระทบ)
- Rate = 0 → ถือว่า disable bet_type นั้น (frontend ซ่อน)

## 🔗 Related
- Frontend: `lotto-standalone-admin-web/src/app/rates/page.tsx`
- Member display: `lotto-standalone-member-web/src/app/(member)/rates/page.tsx`
- Settle flow: `bets_handler.go` + `lotto-core/payout/`
- Downline rule: memory `downline_profit_calc`

## 📝 Change Log
- 2026-04-20: v1 initial skeleton
