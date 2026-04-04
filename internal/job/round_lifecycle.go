// =============================================================================
// Package job — round_lifecycle.go
//
// Unified Round Lifecycle — สร้าง + เปิด + ปิดรอบอัตโนมัติ
//
// ⭐ รวม round_create.go + round_cron.go เข้าด้วยกัน
//
// Jobs ที่ run:
//   1. Auto-Create: สร้างรอบล่วงหน้า 7 วัน (ทุก 1 ชั่วโมง)
//   2. Auto-Transition: upcoming→open→closed ตามเวลา (ทุก 30 วินาที)
//
// ⭐ ไม่จัดการยี่กี — ยี่กีจัดการใน member-api/job/yeekee_cron.go
//
// ความสัมพันธ์:
//   - ใช้ service.RoundService สำหรับ batch open/close
//   - share DB กับ member-api (#3)
//   - lottery_types table: ID + code + status
//   - lottery_rounds table: สร้าง + อัพเดทสถานะ
// =============================================================================
package job

import (
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"

	"github.com/farritpcz/lotto-standalone-admin-api/internal/service"
)

// =============================================================================
// Schedule Config — กำหนดการสร้างรอบอัตโนมัติ
// =============================================================================

// roundSchedule กำหนดการสร้างรอบสำหรับแต่ละประเภทหวย
//
// ⭐ ในอนาคตจะย้ายไปเก็บใน DB (admin ตั้งผ่าน UI)
// ตอนนี้ hardcode ไว้ก่อน — เพิ่ม/ลบได้ง่าย
type roundSchedule struct {
	LotteryCode string                // code ใน lottery_types table เช่น "THAI_GOV"
	OpenHour    int                   // ชั่วโมงเปิดรับ (0-23)
	OpenMin     int                   // นาทีเปิดรับ (0-59)
	CloseHour   int                   // ชั่วโมงปิดรับ (0-23)
	CloseMin    int                   // นาทีปิดรับ (0-59)
	ShouldRun   func(time.Time) bool  // ฟังก์ชันเช็คว่าวันนี้ต้องสร้างรอบหรือไม่
	Suffix      string                // suffix สำหรับ round_number (เช่น "-AM", "-PM", "")
}

// ─── วันที่ต้องสร้างรอบ ───────────────────────────────────────
// หวยไทย: วันที่ 1, 16 ของเดือน
func isThaiLotteryDay(d time.Time) bool { return d.Day() == 1 || d.Day() == 16 }

// จันทร์-ศุกร์ (weekday) — ใช้สำหรับหวยหุ้น
func isWeekday(d time.Time) bool {
	wd := d.Weekday()
	return wd >= time.Monday && wd <= time.Friday
}

// ทุกวัน — ใช้สำหรับหวยลาว, ฮานอย, มาเลย์
func isEveryDay(_ time.Time) bool { return true }

// ─── ตาราง schedule ทั้งหมด 39 ประเภท ────────────────────────
// ⭐ เวลาทั้งหมดเป็นเวลาไทย (Asia/Bangkok, UTC+7)
// ⭐ Yeekee ไม่อยู่ในตารางนี้ — จัดการใน member-api
var defaultSchedules = []roundSchedule{
	// ── กลุ่มหวยไทย (วันที่ 1, 16) ──────────────────────────────
	{LotteryCode: "THAI_GOV", OpenHour: 6, OpenMin: 0, CloseHour: 15, CloseMin: 30, ShouldRun: isThaiLotteryDay},
	{LotteryCode: "BAAC", OpenHour: 6, OpenMin: 0, CloseHour: 15, CloseMin: 30, ShouldRun: isThaiLotteryDay},
	{LotteryCode: "GSB", OpenHour: 6, OpenMin: 0, CloseHour: 15, CloseMin: 30, ShouldRun: isThaiLotteryDay},

	// ── กลุ่มหวยลาว (ทุกวัน) ────────────────────────────────────
	{LotteryCode: "LAO_VIP", OpenHour: 6, OpenMin: 0, CloseHour: 20, CloseMin: 0, ShouldRun: isEveryDay},
	{LotteryCode: "LAO_PATTANA", OpenHour: 6, OpenMin: 0, CloseHour: 20, CloseMin: 0, ShouldRun: isEveryDay},
	{LotteryCode: "LAO_STAR", OpenHour: 6, OpenMin: 0, CloseHour: 20, CloseMin: 0, ShouldRun: isEveryDay},
	{LotteryCode: "LAO_SAMAKKEE", OpenHour: 6, OpenMin: 0, CloseHour: 20, CloseMin: 0, ShouldRun: isEveryDay},
	{LotteryCode: "LAO_THAKHEK_VIP", OpenHour: 6, OpenMin: 0, CloseHour: 20, CloseMin: 0, ShouldRun: isEveryDay},

	// ── กลุ่มหวยฮานอย (ทุกวัน) ──────────────────────────────────
	{LotteryCode: "HANOI", OpenHour: 6, OpenMin: 0, CloseHour: 18, CloseMin: 0, ShouldRun: isEveryDay},
	{LotteryCode: "HANOI_VIP", OpenHour: 6, OpenMin: 0, CloseHour: 18, CloseMin: 0, ShouldRun: isEveryDay},
	{LotteryCode: "HANOI_PATTANA", OpenHour: 6, OpenMin: 0, CloseHour: 18, CloseMin: 0, ShouldRun: isEveryDay},

	// ── มาเลย์ (ทุกวัน) ─────────────────────────────────────────
	{LotteryCode: "MALAY", OpenHour: 6, OpenMin: 0, CloseHour: 18, CloseMin: 30, ShouldRun: isEveryDay},

	// ── กลุ่มหวยหุ้น (จ-ศ) ──────────────────────────────────────
	// VIP
	{LotteryCode: "STOCK_RUSSIA_VIP", OpenHour: 9, OpenMin: 0, CloseHour: 12, CloseMin: 0, ShouldRun: isWeekday},
	{LotteryCode: "STOCK_DJ_VIP", OpenHour: 20, OpenMin: 0, CloseHour: 23, CloseMin: 0, ShouldRun: isWeekday},
	{LotteryCode: "STOCK_HSI_VIP_AM", OpenHour: 9, OpenMin: 0, CloseHour: 12, CloseMin: 0, ShouldRun: isWeekday},
	{LotteryCode: "STOCK_TAIWAN_VIP", OpenHour: 9, OpenMin: 0, CloseHour: 13, CloseMin: 0, ShouldRun: isWeekday},
	{LotteryCode: "STOCK_KOREA_VIP", OpenHour: 9, OpenMin: 0, CloseHour: 13, CloseMin: 0, ShouldRun: isWeekday},
	{LotteryCode: "STOCK_HSI_VIP_PM", OpenHour: 13, OpenMin: 0, CloseHour: 16, CloseMin: 0, ShouldRun: isWeekday},
	// รอบเช้า
	{LotteryCode: "STOCK_NIKKEI_AM", OpenHour: 9, OpenMin: 0, CloseHour: 11, CloseMin: 30, ShouldRun: isWeekday},
	{LotteryCode: "STOCK_CHINA_AM", OpenHour: 9, OpenMin: 30, CloseHour: 11, CloseMin: 30, ShouldRun: isWeekday},
	{LotteryCode: "STOCK_HSI_AM", OpenHour: 9, OpenMin: 30, CloseHour: 12, CloseMin: 0, ShouldRun: isWeekday},
	{LotteryCode: "STOCK_TAIWAN", OpenHour: 9, OpenMin: 0, CloseHour: 13, CloseMin: 30, ShouldRun: isWeekday},
	// รอบบ่าย
	{LotteryCode: "STOCK_NIKKEI_PM", OpenHour: 12, OpenMin: 30, CloseHour: 15, CloseMin: 0, ShouldRun: isWeekday},
	{LotteryCode: "STOCK_KOREA", OpenHour: 9, OpenMin: 0, CloseHour: 15, CloseMin: 0, ShouldRun: isWeekday},
	{LotteryCode: "STOCK_CHINA_PM", OpenHour: 13, OpenMin: 0, CloseHour: 15, CloseMin: 0, ShouldRun: isWeekday},
	{LotteryCode: "STOCK_HSI_PM", OpenHour: 13, OpenMin: 0, CloseHour: 16, CloseMin: 0, ShouldRun: isWeekday},
	// รอบเย็น/ค่ำ
	{LotteryCode: "STOCK_TH_PM", OpenHour: 14, OpenMin: 30, CloseHour: 16, CloseMin: 30, ShouldRun: isWeekday},
	{LotteryCode: "STOCK_SINGAPORE", OpenHour: 9, OpenMin: 0, CloseHour: 17, CloseMin: 0, ShouldRun: isWeekday},
	{LotteryCode: "STOCK_INDIA", OpenHour: 10, OpenMin: 0, CloseHour: 16, CloseMin: 0, ShouldRun: isWeekday},
	{LotteryCode: "STOCK_UK", OpenHour: 14, OpenMin: 0, CloseHour: 21, CloseMin: 30, ShouldRun: isWeekday},
	{LotteryCode: "STOCK_GERMANY", OpenHour: 14, OpenMin: 0, CloseHour: 22, CloseMin: 0, ShouldRun: isWeekday},
	{LotteryCode: "STOCK_RUSSIA", OpenHour: 13, OpenMin: 0, CloseHour: 23, CloseMin: 0, ShouldRun: isWeekday},
	{LotteryCode: "STOCK_DJ", OpenHour: 20, OpenMin: 30, CloseHour: 3, CloseMin: 0, ShouldRun: isWeekday},
	// VIP เพิ่มเติม
	{LotteryCode: "STOCK_GERMANY_VIP", OpenHour: 14, OpenMin: 0, CloseHour: 22, CloseMin: 0, ShouldRun: isWeekday},
	{LotteryCode: "STOCK_UK_VIP", OpenHour: 14, OpenMin: 0, CloseHour: 21, CloseMin: 30, ShouldRun: isWeekday},
	{LotteryCode: "STOCK_NIKKEI_VIP_PM", OpenHour: 12, OpenMin: 30, CloseHour: 15, CloseMin: 0, ShouldRun: isWeekday},
	{LotteryCode: "STOCK_NIKKEI_VIP_AM", OpenHour: 9, OpenMin: 0, CloseHour: 11, CloseMin: 30, ShouldRun: isWeekday},
	{LotteryCode: "STOCK_CHINA_VIP_PM", OpenHour: 13, OpenMin: 0, CloseHour: 15, CloseMin: 0, ShouldRun: isWeekday},
	{LotteryCode: "STOCK_CHINA_VIP_AM", OpenHour: 9, OpenMin: 30, CloseHour: 11, CloseMin: 30, ShouldRun: isWeekday},
}

// =============================================================================
// StartRoundLifecycleJob — unified cron ที่ทำทั้งสร้าง + transition
// =============================================================================

// StartRoundLifecycleJob เริ่ม cron jobs สำหรับ:
//   - Auto-create: สร้างรอบล่วงหน้า 7 วัน (ทุก 1 ชั่วโมง)
//   - Auto-transition: upcoming→open, open→closed (ทุก 30 วินาที)
//
// เรียกจาก cmd/server/main.go ตอน startup
func StartRoundLifecycleJob(svc *service.RoundService) {
	log.Println("🔄 Round lifecycle job started (create=1h, transition=30s)")

	// ─── Job 1: Auto-Create (ทุก 1 ชั่วโมง) ─────────────────────
	go func() {
		// Run ทันทีเมื่อ startup
		createUpcomingRounds(svc.DB())

		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			createUpcomingRounds(svc.DB())
		}
	}()

	// ─── Job 2: Auto-Transition (ทุก 30 วินาที) ─────────────────
	go func() {
		// Run ทันทีเมื่อ startup
		svc.BatchOpenRounds()
		svc.BatchCloseRounds()

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			svc.BatchOpenRounds()
			svc.BatchCloseRounds()
		}
	}()
}

// =============================================================================
// createUpcomingRounds — สร้างรอบหวยล่วงหน้า 7 วัน
// =============================================================================

// createUpcomingRounds ดูล่วงหน้า 7 วัน แล้วสร้างรอบที่ยังไม่มี
//
// Flow:
//   1. ดึง lottery_types ทั้งหมด (active) → สร้าง map code→ID
//   2. วนแต่ละวัน (0-7 วันข้างหน้า)
//   3. วนแต่ละ schedule → เช็ค ShouldRun()
//   4. สร้าง round_number → เช็คซ้ำ → INSERT
func createUpcomingRounds(db *gorm.DB) {
	now := time.Now()
	lookAheadDays := 7

	// ─── ดึง lottery_types ทั้งหมดจาก DB ─────────────────────────
	type LotteryTypeRow struct {
		ID   int64
		Code string
	}
	var ltRows []LotteryTypeRow
	db.Table("lottery_types").Select("id, code").Where("status = ?", "active").Find(&ltRows)

	// สร้าง map code → ID สำหรับ lookup เร็ว
	codeToID := make(map[string]int64, len(ltRows))
	for _, lt := range ltRows {
		codeToID[lt.Code] = lt.ID
	}

	totalCreated := 0

	// ─── วนแต่ละวัน (วันนี้ → 7 วันข้างหน้า) ───────────────────
	for dayOffset := 0; dayOffset <= lookAheadDays; dayOffset++ {
		date := now.AddDate(0, 0, dayOffset)
		dateOnly := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())

		// ─── วนแต่ละ schedule ─────────────────────────────────────
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

			// สร้าง round_number เช่น "20260404" หรือ "20260404-AM"
			roundNumber := dateOnly.Format("20060102") + sched.Suffix

			// เช็คว่ามีอยู่แล้วหรือยัง (ป้องกันสร้างซ้ำ)
			var existCount int64
			db.Table("lottery_rounds").
				Where("lottery_type_id = ? AND round_number = ?", ltID, roundNumber).
				Count(&existCount)

			if existCount > 0 {
				continue // มีอยู่แล้ว → ข้าม
			}

			// คำนวณ open_time / close_time
			openTime := time.Date(dateOnly.Year(), dateOnly.Month(), dateOnly.Day(),
				sched.OpenHour, sched.OpenMin, 0, 0, dateOnly.Location())
			closeTime := time.Date(dateOnly.Year(), dateOnly.Month(), dateOnly.Day(),
				sched.CloseHour, sched.CloseMin, 0, 0, dateOnly.Location())

			// กรณีหุ้นที่ปิดหลังเที่ยงคืน (เช่น ดาวโจนส์ 20:30-03:00)
			if closeTime.Before(openTime) {
				closeTime = closeTime.AddDate(0, 0, 1) // ปิดวันถัดไป
			}

			// ─── INSERT round ─────────────────────────────────────
			result := db.Exec(`INSERT INTO lottery_rounds
				(lottery_type_id, round_number, round_date, open_time, close_time, status, result_top3, result_top2, result_bottom2, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, 'upcoming', '', '', '', ?, ?)`,
				ltID, roundNumber, dateOnly, openTime, closeTime, now, now)

			if result.Error != nil {
				// ส่วนใหญ่จะเป็น duplicate key → ไม่ต้อง log
				continue
			}
			totalCreated++
		}
	}

	if totalCreated > 0 {
		log.Printf("📅 Auto-created %d rounds (look-ahead %d days)", totalCreated, lookAheadDays)
	}
}

// =============================================================================
// GetDefaultSchedules — expose สำหรับ API (ใช้ดูตาราง schedule)
// =============================================================================

// ScheduleInfo ข้อมูล schedule สำหรับแสดงผลใน admin UI
type ScheduleInfo struct {
	LotteryCode string `json:"lottery_code"`
	OpenTime    string `json:"open_time"`    // "HH:MM"
	CloseTime   string `json:"close_time"`   // "HH:MM"
	DayType     string `json:"day_type"`     // "daily", "weekday", "thai_gov"
}

// GetDefaultSchedules คืนตาราง schedule ทั้งหมดสำหรับแสดงใน admin UI
func GetDefaultSchedules() []ScheduleInfo {
	result := make([]ScheduleInfo, 0, len(defaultSchedules))
	for _, s := range defaultSchedules {
		dayType := "daily"
		// ตรวจสอบ function pointer ไม่ได้ — ใช้ heuristic จาก code
		if s.LotteryCode == "THAI_GOV" || s.LotteryCode == "BAAC" || s.LotteryCode == "GSB" {
			dayType = "thai_gov"
		} else if len(s.LotteryCode) > 5 && s.LotteryCode[:5] == "STOCK" {
			dayType = "weekday"
		}
		result = append(result, ScheduleInfo{
			LotteryCode: s.LotteryCode,
			OpenTime:    fmt.Sprintf("%02d:%02d", s.OpenHour, s.OpenMin),
			CloseTime:   fmt.Sprintf("%02d:%02d", s.CloseHour, s.CloseMin),
			DayType:     dayType,
		})
	}
	return result
}
