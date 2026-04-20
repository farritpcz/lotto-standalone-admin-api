# Reports & Analytics (Win/Loss + Downline)

> Last updated: 2026-04-20
> Related code: `internal/handler/stubs.go:1719` (Summary), `:1746` (Profit); `internal/handler/reports.go:39` (MemberCredit); `internal/handler/downline_handler.go:809` (DownlineProfits), `:925` (NodeProfits), `:1001` (DownlineReport)
> Related migrations: `migrations/016_agent_downline.sql`, `migrations/022_optimize_deposit_transactions.sql`

## Purpose
สรุปยอดเล่น/ได้เสีย/กำไรและรายงานสายงาน (เคลียสายงาน) — scope ทุก query ตาม NodeScope รองรับ filter `from`/`to` (YYYY-MM-DD)

## Rules
1. **GetSummaryReport**: คืน `total_bets`, `total_amount`, `total_win`, `profit = amount - win` จาก `bets` WHERE `DATE(created_at) BETWEEN from AND to`
2. ทุก query รายงาน **ต้อง ScopeByMemberID** เมื่อ `scope.IsNode` (ผ่าน `mw.GetNodeScope.ScopeByMemberID(q, "member_id")`)
3. Default range: Summary = 7 วันล่าสุด, Profit = 30 วันล่าสุด
4. **GetProfitReport**: group by `DATE(created_at)` - แถวต่อวันพร้อม `profit`
5. **GetMemberCreditReport**: ยอดเครดิตปัจจุบันของสมาชิก
6. **Downline Report** (`GetDownlineReport` — เคลียสายงาน):
   - สูตร 3 ยอด (memory `downline_report_formulas.md`):
     - เก็บใต้สาย = `(100 - child_share_percent)%`
     - จ่ายหัวสาย = `(100 - my_share_percent)%`
   - Walk up tree - สะสมส่วนต่างทุกระดับ
   - 3 ส่วน: เว็บตัวเอง, ใต้สาย, สรุปจ่ายหัว
7. Permission: `reports.view`
8. Downline routes เช็ค scope (node เห็นเฉพาะใต้สายตัวเอง)

## Flow (summary)
```
GET /api/v1/reports/summary?from=2026-04-01&to=2026-04-20
  -> scope = GetNodeScope
  -> q1 = Count(bets) WHERE date in range + scope
  -> q2 = SUM(amount)
  -> q3 = SUM(win_amount) WHERE status=won
  -> profit = total_amount - total_win
```

## API Endpoints
- `GET /api/v1/reports/summary?from=&to=`
- `GET /api/v1/reports/profit?from=&to=`
- `GET /api/v1/reports/member-credit`
- `GET /api/v1/downline/profits` / `/:nodeId`
- `GET /api/v1/downline/report`
- `GET /api/v1/node/profits`

## Edge Cases
- `from > to` -> คืน 0
- date format ผิด -> ใช้ default
- Node ไม่มีลูก -> ใต้สาย = 0
- Profit ติดลบ -> คืนค่าลบตรงๆ

## Source of Truth
- Handlers: `internal/handler/stubs.go:1719-1770`, `internal/handler/reports.go:39`, `internal/handler/downline_handler.go:809-1200`
- Scope: `internal/middleware/node_scope.go`
- Formulas: memory `downline_report_formulas.md`, `downline_profit_calc.md`
- Router: `internal/handler/router.go:160-161`, `:266`, `:322-326`, `:381`

## Change Log
- 2026-04-20: Initial — summary/profit/member-credit + downline reports (เคลียสายงาน 3 ยอด)
