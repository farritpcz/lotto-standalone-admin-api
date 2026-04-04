// Package job — round_cron.go
// Auto-transition rounds ตามเวลา (open_time / close_time)
//
// ⭐ ทำงานทุก 30 วินาที:
//   1. upcoming → open: ถ้าถึง open_time แล้ว
//   2. open → closed: ถ้าเลย close_time แล้ว
//
// ลำดับ transition ที่ถูกต้อง: upcoming → open → closed → resulted
// - "resulted" ต้อง trigger จาก admin (SubmitResult) หรือ auto-result job
// - cron นี้จัดการแค่ upcoming↔open↔closed
//
// ⭐ ไม่รวมยี่กี — ยี่กีจัดการ transition เองใน member-api yeekee_cron.go
//
// ความสัมพันธ์:
// - share DB กับ member-api (#3)
// - lottery_types.code ใน DB: THAI, LAO, STOCK_TH, STOCK_FOREIGN, HANOI, MALAY, etc.
// - ยี่กี (YEEKEE, YEEKEE_5, YEEKEE_15, YEEKEE_VIP) ถูก exclude
package job

import (
	"log"
	"time"

	"gorm.io/gorm"
)

// yeekeeCodes — ประเภทหวยยี่กี ที่จัดการ transition เองใน member-api
// ไม่ต้องทำ auto-transition ที่นี่
var yeekeeCodes = []string{"YEEKEE", "YEEKEE_5", "YEEKEE_15", "YEEKEE_VIP"}

// StartRoundTransitionJob เริ่ม cron job สำหรับ auto-transition rounds
//
// ทำงานทุก 30 วินาที:
//   - upcoming → open: ถ้า now >= open_time
//   - open → closed: ถ้า now >= close_time
//
// เรียกจาก cmd/server/main.go ตอน startup
func StartRoundTransitionJob(db *gorm.DB) {
	log.Println("🔄 Round transition job started (check every 30s)")

	go func() {
		// รอบแรก — run ทันที
		transitionRounds(db)

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			transitionRounds(db)
		}
	}()
}

// transitionRounds ตรวจสอบ + เปลี่ยนสถานะรอบหวย
//
// Flow:
//   1. หารอบ status="upcoming" ที่ถึง open_time แล้ว → เปลี่ยนเป็น "open"
//   2. หารอบ status="open" ที่เลย close_time แล้ว → เปลี่ยนเป็น "closed"
//   3. ข้ามรอบยี่กี (จัดการใน member-api)
//
// ⭐ ใช้ batch update → ไม่ loop ทีละ row (ลด DB round trips)
func transitionRounds(db *gorm.DB) {
	now := time.Now()

	// ─── upcoming → open ───────────────────────────────────────
	// รอบที่ถึงเวลาเปิดรับแทงแล้ว แต่ยังเป็น upcoming
	// เงื่อนไข: status = "upcoming" AND open_time <= now
	// ข้าม: ยี่กี (จัดการเอง)
	result := db.Table("lottery_rounds").
		Joins("JOIN lottery_types ON lottery_types.id = lottery_rounds.lottery_type_id").
		Where("lottery_rounds.status = ?", "upcoming").
		Where("lottery_rounds.open_time <= ?", now).
		Where("lottery_types.code NOT IN ?", yeekeeCodes).
		Update("status", "open")

	if result.RowsAffected > 0 {
		log.Printf("🟢 Opened %d round(s) — upcoming → open", result.RowsAffected)
	}

	// ─── open → closed ─────────────────────────────────────────
	// รอบที่เลยเวลาปิดรับแทงแล้ว แต่ยังเป็น open
	// เงื่อนไข: status = "open" AND close_time <= now
	// ข้าม: ยี่กี (จัดการเอง)
	result = db.Table("lottery_rounds").
		Joins("JOIN lottery_types ON lottery_types.id = lottery_rounds.lottery_type_id").
		Where("lottery_rounds.status = ?", "open").
		Where("lottery_rounds.close_time <= ?", now).
		Where("lottery_types.code NOT IN ?", yeekeeCodes).
		Update("status", "closed")

	if result.RowsAffected > 0 {
		log.Printf("🔴 Closed %d round(s) — open → closed", result.RowsAffected)
	}
}
