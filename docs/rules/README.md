# 📋 Rule Files — `lotto-standalone-admin-api`

> **Source of truth** ของกฏ/เงื่อนไขแต่ละฟังก์ชันใน admin backend (Go)
> ทุกครั้งที่แก้ logic ในไฟล์ที่ rule อ้างถึง → **ต้องอัพเดท rule ในคอมมิตเดียวกัน**
> กฏ shared logic อยู่ที่ `../../lotto-core/docs/rules/`

---

## 📚 Index — Rule Files ทั้งหมด

| Status | File | ครอบคลุม |
|--------|------|---------|
| ✅ | `admin_auth.md` | Admin login, JWT, role (owner/admin/staff), permission |
| ✅ | `agent_node_management.md` | สร้าง/แก้/ลบ sub-agent, tree operations |
| ✅ | `member_management.md` | CRUD สมาชิก, freeze, reset password, edit balance |
| ✅ | `round_management.md` | เปิด/ปิด/ออกผลรอบ (manual), validation ก่อน settle |
| ✅ | `deposit_withdraw_admin.md` | Approve/reject, manual adjust, RKAUTO integration |
| 🚧 | `banner_cms.md` | Upload R2, variants (sm/md/lg), reorder, agent_node_id scoping |
| ✅ | `bank_account_settings.md` | agent_bank_accounts, PromptPay QR |
| ✅ | `system_settings.md` | Settings table (key-value), per-node override |
| ✅ | `reports_analytics.md` | Win/loss, downline report, สูตรเคลียสายงาน |
| 🚧 | `audit_log.md` | Admin action log |
| 🚧 | `cloudflare_integration.md` | Auto create zone ตอน create agent |

**Legend:** ✅ done · 🚧 partial · ⏳ not started

---

## ✍️ Template (ทุกไฟล์ต้องมีโครงนี้)

```markdown
# [ชื่อฟังก์ชัน]

> Last updated: YYYY-MM-DD
> Related code: `internal/handler/xxx.go:LINE`, `internal/service/xxx.go:LINE`
> Related migrations: `migrations/NNN_xxx.sql`

## 🎯 Purpose
[ฟังก์ชันนี้ทำอะไร ทำไมต้องมี — 1-3 บรรทัด]

## 📋 Rules (กฏเงื่อนไข)
1. เงื่อนไขข้อ 1 (validation, auth requirement, etc.)
2. เงื่อนไขข้อ 2

## 🔄 Flow
[request → validation → DB → response]

## 🌐 API Endpoints
- `POST /api/v1/xxx` — [purpose]
- `GET /api/v1/xxx/:id` — [purpose]

## ⚠️ Edge Cases
- ถ้า X → ทำ Y
- ห้าม Z

## 🔗 Source of Truth (file:line)
- Handler: `internal/handler/xxx.go:123`
- Service: `internal/service/xxx.go:45`
- Model: `internal/model/xxx.go:10`
- Migration: `migrations/NNN_xxx.sql`

## 📝 Change Log
- YYYY-MM-DD: [สิ่งที่เปลี่ยน] (commit abc123)
```

---

## 🔒 Convention

1. **ภาษา:** ไทยเป็นหลัก, ศัพท์เทคนิค/โค้ด/enum ใช้ภาษาอังกฤษ
2. **ความยาว:** ไม่เกิน ~200 บรรทัดต่อไฟล์ — ถ้ายาวเกิน → split
3. **ห้ามลอก comment ในโค้ดมาวาง** — rule = "ทำไม + เงื่อนไข", โค้ด = "ยังไง"
4. **file:line ต้อง up-to-date** — ถ้าย้ายโค้ด ต้องอัพเดท reference
5. **Change log** — เขียนย่อๆ 1 บรรทัด + commit hash
6. **API contract** ที่ frontend เรียก → ลิงก์ไปที่ `lotto-standalone-admin-web/docs/rules/api_client.md`
