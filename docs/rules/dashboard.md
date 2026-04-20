# Dashboard — admin-api

> Last updated: 2026-04-20 (v1 initial — starter rule)
> Related code: `internal/handler/dashboard_handler.go`, `internal/handler/router.go:106-107`

## 🎯 Purpose
Dashboard รวมสรุป KPI: ยอดแทงวันนี้, ยอดจ่าย, กำไร/ขาดทุน, สมาชิกใหม่, รอบที่เปิด — แสดงหน้าแรกของ admin-web

## 📋 Rules
1. **Scope per-agent**: ทุก aggregate filter `agent_id` (multi-tenant)
2. **Permission**: `dashboard.view`
3. **Caching**: ใช้ Redis (`h.Redis`) cache ผลรวมหนัก ๆ (เช่น 60s TTL สำหรับ stats วันนี้)
4. **Time zone**: คำนวณ "วันนี้" ตาม Asia/Bangkok (UTC+7) — ห้าม UTC เพราะรอบไทย cutoff 15:00 local
5. **Two versions**: `GET /dashboard` = v1 (simple), `GET /dashboard/v2` = v2 (richer breakdown)
6. **Downline aware**: ถ้า admin มี downline → stats รวมยอดใต้สายทั้งหมดด้วย (diff% calc ดู `downline_profit_calc`)

## 🌐 Endpoints
- GET `/api/v1/dashboard`    → `GetDashboard`   (v1)
- GET `/api/v1/dashboard/v2` → `GetDashboardV2` (v2 — richer data)

## ⚠️ Edge Cases
- Redis ล่ม → fallback คำนวณ on-the-fly (slow แต่ไม่ error)
- วันที่ boundary (00:00 Bangkok) → cache ต้อง invalidate ที่เที่ยงคืน (cron job)
- Admin ที่เพิ่งสมัคร ยังไม่มี data → return 0s + empty arrays

## 🔗 Related
- Frontend: `lotto-standalone-admin-web/src/app/dashboard/page.tsx`
- Downline rollup: `downline.md`, memory `downline_profit_calc`
- Redis: docker-compose `redis` service

## 📝 Change Log
- 2026-04-20: v1 initial skeleton
