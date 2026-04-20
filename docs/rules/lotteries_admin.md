# Lotteries (Admin) — admin-api

> Last updated: 2026-04-20 (v1 initial — starter rule)
> Related code: `internal/handler/lotteries_handler.go`, `internal/handler/router.go:117-120`

## 🎯 Purpose
Admin จัดการประเภทหวย (lottery_type): เปิด/ปิด, ตั้งเวลา cutoff/open, อัพโหลดรูป, ตั้งชื่อ + สี — ต่อ agent

## 📋 Rules
1. **Scope per-agent**: ทุก query filter `agent_id`
2. **Permission**:
   - `lottery.view` → GET
   - `lottery.create` → POST (สร้าง lottery type ใหม่)
   - `lottery.edit` → PUT (แก้ metadata + รูป)
3. **Type codes** (ดู memory `lottery_types_structure`): THAI_GOV, LAO_VIP_*, HANOI_*, STOCK_*, YEEKEE — code เป็น enum อ้างอิงจาก `lotto-core`
4. **Image upload**: ใช้ endpoint แยก `PUT /lotteries/:id/image` → upload R2 (ดู `upload.md`)
5. **Cannot delete**: ไม่มี DELETE endpoint — ใช้ `active=false` (soft disable) เพราะ bet history ต้องอ้างอิง
6. **YEEKEE**: เปิดอัตโนมัติทุก Agent (ดู memory `agent_rules`) — admin แก้ได้แค่ config ผ่าน yeekee_admin.md

## 🌐 Endpoints
- GET `/api/v1/lotteries`            → `ListLotteries`
- POST `/api/v1/lotteries`           → `CreateLottery`
- PUT  `/api/v1/lotteries/:id`       → `UpdateLottery`
- PUT  `/api/v1/lotteries/:id/image` → `UpdateLotteryImage`

## ⚠️ Edge Cases
- ปิด lottery_type ที่มีรอบ open อยู่ → ควรเตือน (รอบยังรับแทงอยู่จนกว่าจะ cutoff)
- Code ซ้ำภายใน agent_id → 409
- Image upload ขนาดใหญ่ > 5MB → reject ที่ upload endpoint

## 🔗 Related
- Frontend: `lotto-standalone-admin-web/src/app/lotteries/page.tsx`
- Rounds (ต่อ lottery_type): `round_management.md`
- Rates (ต่อ lottery_type): `rates.md`
- Yeekee config: `yeekee_admin.md`

## 📝 Change Log
- 2026-04-20: v1 initial skeleton
