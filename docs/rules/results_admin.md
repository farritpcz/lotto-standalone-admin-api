# Results (Admin) — admin-api

> Last updated: 2026-04-20 (v1 initial — starter rule)
> Related code: `internal/handler/results_handler.go`, `internal/handler/router.go:132-134`

## 🎯 Purpose
Admin กรอก/preview ผลรางวัล → trigger settle รอบ (คำนวณ win/lose ทุก bet + update wallet)

## 📋 Rules
1. **Scope per-agent**: filter `agent_id` + `lottery_round_id`
2. **Permission**:
   - `lottery.view` → list results
   - `lottery.create` → submit/preview result
3. **Preview mode**: `POST /results/:roundId/preview` — คำนวณ win amount แต่**ไม่** commit (dry-run เพื่อดู impact)
4. **Submit = irreversible**: `POST /results/:roundId` — wrap ใน DB transaction:
   - UPDATE round.result_* + status='settled'
   - INSERT bet results (win_amount, status)
   - UPDATE wallet balance ทุก member ที่ชนะ + INSERT transaction
   - ถ้า error ใด ๆ rollback ทั้งหมด
5. **Result format** ต่างกันตามประเภท: ไทย (6 หลัก + 2-top + 3-top), หุ้น (3 digit), ยี่กี (4 digit), etc.
6. **Yeekee settle** ใช้ endpoint แยก (`yeekee_admin.md`) — ไม่ใช้ endpoint นี้

## 🌐 Endpoints
- GET  `/api/v1/results`                     → `ListResults` (history + filter)
- POST `/api/v1/results/:roundId`            → `SubmitResult` (commit settle)
- POST `/api/v1/results/:roundId/preview`    → `PreviewResult` (dry-run)

## ⚠️ Edge Cases
- Submit result ซ้ำ → reject 409 (round.status ต้องเป็น 'closed' ก่อน submit; หลัง submit เป็น 'settled')
- Result ผิด → ต้องมี flow "reverse settle" (ยังไม่มี — TODO)
- Round ที่ยังเปิดอยู่ → reject (ต้อง cutoff/close ก่อน)
- Downline commission: คำนวณพร้อมกันกับ settle (ดู `downline.md`)

## 🔗 Related
- Frontend: `lotto-standalone-admin-web/src/app/results/page.tsx`
- Settle core logic: `lotto-core/payout/*`
- Member side (ดูผล): `lotto-standalone-member-api/internal/handler/results.go`

## 📝 Change Log
- 2026-04-20: v1 initial skeleton
