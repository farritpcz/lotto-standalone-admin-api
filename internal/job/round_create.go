// Package job — round_create.go
// Auto-create lottery rounds ตามกำหนดการ
//
// ⭐ สร้างรอบหวยอัตโนมัติ ล่วงหน้า 7 วัน:
//   - หวยไทย: วันที่ 1, 16 ของเดือน (เปิด 06:00, ปิด 15:30)
//   - หวยลาว: วันจันทร์, พุธ, ศุกร์ (เปิด 06:00, ปิด 20:00)
//   - หวยฮานอย: ทุกวัน (เปิด 06:00, ปิด 18:00)
//   - หวยหุ้นไทย: จ-ศ 2 รอบ (เช้า 09:00-12:00, บ่าย 13:00-16:00)
//   - หวยหุ้นต่างประเทศ: จ-ศ 2 รอบ (เช้า/บ่าย)
//
// ⭐ ไม่สร้างยี่กี — ยี่กีสร้างใน member-api yeekee_cron.go
//
// ⭐ ไม่สร้างซ้ำ — เช็ค round_number ก่อนสร้าง (UNIQUE constraint)
//
// ความสัมพันธ์:
// - ใช้ lotto-core/lottery: GenerateRoundNumber(), GetThaiLotteryDates(), etc.
// - share DB กับ member-api (#3)
// - lottery_types table: ID + code ที่ต้อง query
package job

import (
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"
)

// roundSchedule กำหนดการสร้างรอบสำหรับแต่ละประเภทหวย
type roundSchedule struct {
	LotteryCode string       // code ใน lottery_types table เช่น "THAI"
	OpenHour    int          // ชั่วโมงเปิดรับ (เช่น 6 = 06:00)
	OpenMin     int          // นาทีเปิดรับ
	CloseHour   int          // ชั่วโมงปิดรับ
	CloseMin    int          // นาทีปิดรับ
	ShouldRun   func(time.Time) bool // ฟังก์ชันเช็คว่าวันนี้ต้องสร้างรอบหรือไม่
	Suffix      string       // suffix สำหรับ round_number (เช่น "-AM", "-PM", "")
}

// ─── Schedule definitions ─────────────────────────────────

// หวยไทย: วันที่ 1, 16 ของเดือน
func isThaiLotteryDay(d time.Time) bool {
	return d.Day() == 1 || d.Day() == 16
}

// จ-ศ (weekday)
func isWeekday(d time.Time) bool {
	wd := d.Weekday()
	return wd >= time.Monday && wd <= time.Friday
}

// หวยลาว: จ, พ, ศ
func isLaoDay(d time.Time) bool {
	wd := d.Weekday()
	return wd == time.Monday || wd == time.Wednesday || wd == time.Friday
}

// ทุกวัน
func isEveryDay(_ time.Time) bool { return true }

// ─── Schedule config ──────────────────────────────────────
// กำหนดการสำหรับหวยแต่ละประเภท — เพิ่ม/ลบได้ง่าย
var defaultSchedules = []roundSchedule{
	// หวยไทย: 1, 16 ของเดือน | เปิด 06:00 ปิด 15:30
	{LotteryCode: "THAI", OpenHour: 6, OpenMin: 0, CloseHour: 15, CloseMin: 30, ShouldRun: isThaiLotteryDay},

	// หวยลาว: จ, พ, ศ | เปิด 06:00 ปิด 20:00
	{LotteryCode: "LAO", OpenHour: 6, OpenMin: 0, CloseHour: 20, CloseMin: 0, ShouldRun: isLaoDay},

	// หวยฮานอย: ทุกวัน | เปิด 06:00 ปิด 18:00
	{LotteryCode: "HANOI", OpenHour: 6, OpenMin: 0, CloseHour: 18, CloseMin: 0, ShouldRun: isEveryDay},

	// หวยหุ้นไทย เช้า: จ-ศ | เปิด 09:00 ปิด 12:00
	{LotteryCode: "STOCK_TH", OpenHour: 9, OpenMin: 0, CloseHour: 12, CloseMin: 0, ShouldRun: isWeekday, Suffix: "-AM"},
	// หวยหุ้นไทย บ่าย: จ-ศ | เปิด 13:00 ปิด 16:00
	{LotteryCode: "STOCK_TH", OpenHour: 13, OpenMin: 0, CloseHour: 16, CloseMin: 0, ShouldRun: isWeekday, Suffix: "-PM"},

	// หวยหุ้นต่างประเทศ เช้า: จ-ศ
	{LotteryCode: "STOCK_FOREIGN", OpenHour: 9, OpenMin: 0, CloseHour: 12, CloseMin: 0, ShouldRun: isWeekday, Suffix: "-AM"},
	// หวยหุ้นต่างประเทศ บ่าย: จ-ศ
	{LotteryCode: "STOCK_FOREIGN", OpenHour: 13, OpenMin: 0, CloseHour: 16, CloseMin: 0, ShouldRun: isWeekday, Suffix: "-PM"},

	// หวยมาเลย์: ทุกวัน | เปิด 06:00 ปิด 18:30
	{LotteryCode: "MALAY", OpenHour: 6, OpenMin: 0, CloseHour: 18, CloseMin: 30, ShouldRun: isEveryDay},
}

// StartRoundCreationJob เริ่ม cron job สร้างรอบหวยอัตโนมัติ
//
// ทำงานทุก 1 ชั่วโมง:
//   - ดูล่วงหน้า 7 วัน
//   - สร้างรอบที่ยังไม่มีใน DB (เช็คจาก round_number)
//   - สถานะเริ่มต้น = "upcoming"
//
// เรียกจาก cmd/server/main.go ตอน startup
func StartRoundCreationJob(db *gorm.DB) {
	log.Println("📅 Round creation job started (check every 1h, look-ahead 7 days)")

	go func() {
		// รอบแรก — run ทันที
		createUpcomingRounds(db)

		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for range ticker.C {
			createUpcomingRounds(db)
		}
	}()
}

// createUpcomingRounds สร้างรอบหวยล่วงหน้า 7 วัน
func createUpcomingRounds(db *gorm.DB) {
	now := time.Now()
	lookAheadDays := 7

	// ─── ดึง lottery_types ทั้งหมดจาก DB (เอา ID + code) ──────
	type LotteryTypeRow struct {
		ID   int64
		Code string
	}
	var ltRows []LotteryTypeRow
	db.Table("lottery_types").Select("id, code").Where("status = ?", "active").Find(&ltRows)

	// สร้าง map code → ID
	codeToID := make(map[string]int64)
	for _, lt := range ltRows {
		codeToID[lt.Code] = lt.ID
	}

	totalCreated := 0

	// ─── วนแต่ละวัน (วันนี้ + 7 วันข้างหน้า) ──────────────────
	for dayOffset := 0; dayOffset <= lookAheadDays; dayOffset++ {
		date := now.AddDate(0, 0, dayOffset)
		dateOnly := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())

		// ─── วนแต่ละ schedule ──────────────────────────────────
		for _, sched := range defaultSchedules {
			// เช็คว่าวันนี้ต้องสร้างรอบหรือไม่
			if !sched.ShouldRun(dateOnly) {
				continue
			}

			// ดึง lottery_type_id จาก code
			ltID, exists := codeToID[sched.LotteryCode]
			if !exists {
				continue // lottery type ไม่มีใน DB → ข้าม
			}

			// สร้าง round_number: "20260401" หรือ "20260401-AM"
			roundNumber := dateOnly.Format("20060102") + sched.Suffix

			// เช็คว่ามีรอบนี้อยู่แล้วหรือไม่ (ป้องกันสร้างซ้ำ)
			var count int64
			db.Table("lottery_rounds").
				Where("lottery_type_id = ? AND round_number = ?", ltID, roundNumber).
				Count(&count)
			if count > 0 {
				continue // มีแล้ว → ข้าม
			}

			// สร้างเวลาเปิด/ปิด
			openTime := time.Date(dateOnly.Year(), dateOnly.Month(), dateOnly.Day(),
				sched.OpenHour, sched.OpenMin, 0, 0, dateOnly.Location())
			closeTime := time.Date(dateOnly.Year(), dateOnly.Month(), dateOnly.Day(),
				sched.CloseHour, sched.CloseMin, 0, 0, dateOnly.Location())

			// Insert round ด้วย raw SQL (ใช้ struct ไม่สะดวกเพราะ model อยู่คนละ package)
			result := db.Exec(`
				INSERT INTO lottery_rounds (lottery_type_id, round_number, round_date, open_time, close_time, status, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, 'upcoming', NOW(), NOW())
			`, ltID, roundNumber, dateOnly, openTime, closeTime)

			if result.Error != nil {
				log.Printf("⚠️ Failed to create round %s for %s: %v", roundNumber, sched.LotteryCode, result.Error)
			} else if result.RowsAffected > 0 {
				totalCreated++
			}
		}
	}

	if totalCreated > 0 {
		log.Printf("📅 Created %d new round(s) (look-ahead %d days)", totalCreated, lookAheadDays)
	}
}

// AddCustomSchedule เพิ่ม schedule ใหม่สำหรับหวยประเภทอื่น
//
// ใช้ตอนต้องการเพิ่มหวยใหม่โดยไม่ต้องแก้โค้ด defaultSchedules
// เช่น: AddCustomSchedule("BAAC", 6, 0, 15, 0, isThaiLotteryDay)
//
// ⚠️ ต้องเรียกก่อน StartRoundCreationJob()
func AddCustomSchedule(code string, openH, openM, closeH, closeM int, shouldRun func(time.Time) bool) {
	defaultSchedules = append(defaultSchedules, roundSchedule{
		LotteryCode: code,
		OpenHour:    openH, OpenMin: openM,
		CloseHour:   closeH, CloseMin: closeM,
		ShouldRun:   shouldRun,
	})
	log.Printf("📅 Added custom schedule for %s", code)
}

// formatRoundNumber สร้าง round_number จากวันที่ + suffix
// ตัวอย่าง: "20260401", "20260401-AM"
func formatRoundNumber(date time.Time, suffix string) string {
	return fmt.Sprintf("%s%s", date.Format("20060102"), suffix)
}
