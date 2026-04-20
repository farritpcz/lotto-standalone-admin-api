# Notifications (Telegram) — admin-api

> Last updated: 2026-04-20 (v1 initial — starter rule)
> Related code: `internal/handler/notifications.go`, `internal/handler/router.go:260-267`

## 🎯 Purpose
ตั้งค่า group Telegram + event types ที่อยาก notify (ฝาก/ถอน/สมัคร/ฯลฯ) — admin ตั้งค่าได้, ระบบยิง message อัตโนมัติเมื่อมี event

## 📋 Rules
1. Scope per-agent: `notifyKeyScoped()` (notifications.go:71) สร้าง key ต่อ agent — ห้ามใช้ key global
2. Permission: `system.settings` (router.go:263)
3. Store: ใช้ `system_settings` table (key/value JSON) เก็บ array ของ notify groups — 1 group = 1 chat + event subscriptions
4. Bot token: ใช้ global (env) **หรือ** per-agent ตาม config (TODO confirm — ดูใน loadNotifyGroups)
5. TestNotification: ยิง message ทดสอบไปยัง group ที่ระบุใน request (ยังไม่ save) — ใช้ก่อน update
6. SendNotification (internal): ถูกเรียกจาก event point อื่น ๆ (deposit approved, withdraw, ฯลฯ) — non-blocking (goroutine) ห้ามให้ main flow ค้าง
7. JSON escape: `escapeJSON()` (notifications.go:316) ป้องกันตัวอักษรพิเศษใน message ทำ JSON Telegram API พัง

## 🌐 Endpoints
- GET  `/api/v1/notifications/config`  → `GetNotificationConfig`
- PUT  `/api/v1/notifications/config`  → `UpdateNotificationConfig`
- POST `/api/v1/notifications/test`    → `TestNotification`

## 🔧 Internal
- `SendNotification(eventType, message)` — เรียกจาก handler อื่น ๆ (ไม่ใช่ HTTP endpoint)
- `sendTelegramMessage(botToken, chatID, text)` — raw Telegram API call

## ⚠️ Edge Cases
- Telegram bot ถูก kick จาก group → yield error, log, ไม่หยุด main flow
- Network timeout → retry? (ปัจจุบัน no retry — TODO exponential backoff)
- Message ยาวเกิน 4096 chars → Telegram ตัดเอง (ไม่ต้องเช็คฝั่งเรา, แต่ควร truncate สวย ๆ — TODO)

## 🔗 Related
- Deposit/Withdraw events trigger: [deposit_withdraw_admin.md](./deposit_withdraw_admin.md)
- Frontend: `lotto-standalone-admin-web/src/app/settings/notifications/`
- System settings: [system_settings.md](./system_settings.md)

## 📝 Change Log
- 2026-04-20: v1 initial skeleton
