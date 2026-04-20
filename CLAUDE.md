# Claude instructions for `lotto-standalone-admin-api`

## 🔒 Rule Files (BLOCKING — ต้องทำทุกครั้ง)

โปรเจคนี้มี **`docs/rules/*.md`** เป็น **source of truth** ของกฏ/เงื่อนไขแต่ละฟังก์ชัน
(admin auth, agent management, member management, round control, banner CMS, ฯลฯ)

**กฏเหล็ก:**

1. **ก่อนแก้ logic ใด** → อ่าน rule file ที่เกี่ยวข้องก่อน (`docs/rules/<topic>.md`)
   - ถ้ายังไม่มีไฟล์นั้น → **สร้างใหม่** พร้อมกับการแก้โค้ดครั้งแรก
2. **เมื่อแก้ logic เสร็จ** → **ต้องอัพเดท rule file ในคอมมิตเดียวกัน**
   - อัพเดท: เงื่อนไข, flow, edge cases, source-of-truth (file:line), change log
3. **ถ้า rule file ขัดกับโค้ด** → ถือว่า rule ถูก, โค้ดต้องแก้ตาม rule
   (ยกเว้นผู้ใช้สั่งเปลี่ยน rule → อัพเดท rule ก่อน แล้วค่อยแก้โค้ด)
4. **Index:** อ่าน `docs/rules/README.md` เพื่อดู rule files ทั้งหมดใน repo นี้
5. **Cross-repo:** กฏฝั่ง shared logic อยู่ที่ `lotto-core/docs/rules/` อ่านประกอบด้วย

**ห้ามแก้โค้ดโดยไม่อัพเดท rule file** — ถ้าทำผิดกฏนี้ ถือว่างานยังไม่เสร็จ
