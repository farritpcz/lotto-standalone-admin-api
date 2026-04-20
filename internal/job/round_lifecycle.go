// =============================================================================
// Package job — round_lifecycle.go
//
// Unified Round Lifecycle — สร้าง + เปิด + ปิดรอบอัตโนมัติ
//
// ⭐ Jobs ที่ run:
//   1. Auto-Create: สร้างรอบล่วงหน้า 30 วัน (ทุก 1 ชั่วโมง) — อ่าน schedule จาก DB
//   2. Auto-Transition: upcoming→open→closed ตามเวลา (ทุก 30 วินาที)
//
// ⭐ ไม่จัดการยี่กี — ยี่กีจัดการใน member-api/job/yeekee_cron.go
//
// ⭐ Schedule source of truth (since migration 025):
//   - lottery_types.schedule_config JSON
//   - { "day_type": "daily|weekday|thai_gov", "open_time": "HH:MM", "close_time": "HH:MM" }
//   - NULL = ไม่ auto-create (ต้องสร้าง manual)
//
// ความสัมพันธ์:
//   - ใช้ service.RoundService สำหรับ batch open/close
//   - share DB กับ member-api (#3)
//   - lottery_types table: ID + code + status + schedule_config
//   - lottery_rounds table: สร้าง + อัพเดทสถานะ
//
// Rule file: docs/rules/round_management.md
// =============================================================================
package job

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"

	"github.com/farritpcz/lotto-standalone-admin-api/internal/service"
)

// =============================================================================
// Schedule Config — โครงสร้าง JSON ใน lottery_types.schedule_config
// =============================================================================

// ScheduleConfig ตรงกับ JSON ใน lottery_types.schedule_config
// อ่านครั้งเดียวตอน cron tick, cache ในหน่วยความจำระหว่าง loop
type ScheduleConfig struct {
	DayType   string `json:"day_type"`   // "daily" | "weekday" | "thai_gov"
	OpenTime  string `json:"open_time"`  // "HH:MM" 24h
	CloseTime string `json:"close_time"` // "HH:MM" 24h — ถ้า < open_time = ปิดวันถัดไป
}

// ─── Day-type predicates ─────────────────────────────────────
// หวยไทย: วันที่ 1, 16 ของเดือน
func isThaiLotteryDay(d time.Time) bool { return d.Day() == 1 || d.Day() == 16 }

// จันทร์-ศุกร์ (weekday) — ใช้สำหรับหวยหุ้น
func isWeekday(d time.Time) bool {
	wd := d.Weekday()
	return wd >= time.Monday && wd <= time.Friday
}

// ทุกวัน — ใช้สำหรับหวยลาว, ฮานอย, มาเลย์
func isEveryDay(_ time.Time) bool { return true }

// shouldRunForDayType เช็คว่าวันนี้ต้องสร้างรอบตาม day_type หรือไม่
func shouldRunForDayType(dayType string, d time.Time) bool {
	switch dayType {
	case "thai_gov":
		return isThaiLotteryDay(d)
	case "weekday":
		return isWeekday(d)
	case "daily":
		return isEveryDay(d)
	default:
		return false // unknown day_type → ไม่สร้าง (กันพลาด)
	}
}

// parseHHMM แปลง "HH:MM" → (hour, minute) สองค่า
// return (-1, -1) ถ้า format ผิด
func parseHHMM(hhmm string) (int, int) {
	var h, m int
	_, err := fmt.Sscanf(hhmm, "%d:%d", &h, &m)
	if err != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return -1, -1
	}
	return h, m
}

// =============================================================================
// StartRoundLifecycleJob — unified cron ที่ทำทั้งสร้าง + transition
// =============================================================================

// StartRoundLifecycleJob เริ่ม cron jobs สำหรับ:
//   - Auto-create: สร้างรอบล่วงหน้า 30 วัน (ทุก 1 ชั่วโมง)
//   - Auto-transition: upcoming→open, open→closed (ทุก 30 วินาที)
//
// เรียกจาก cmd/server/main.go ตอน startup
func StartRoundLifecycleJob(svc *service.RoundService) {
	log.Println("🔄 Round lifecycle job started (create=1h, transition=30s, window=30d)")

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
// createUpcomingRounds — สร้างรอบหวยล่วงหน้า (อ่าน schedule จาก DB)
// =============================================================================

// lookAheadDays — จำนวนวันที่สร้างล่วงหน้า
// ⭐ 30 วัน ตามที่ตกลง (เดิม 7 วัน) — รองรับให้ admin เห็นรอบหวยไทยได้ทั้งรอบถัดไป
const lookAheadDays = 30

// lotteryTypeRow ข้อมูลที่อ่านจาก lottery_types สำหรับ cron นี้
type lotteryTypeRow struct {
	ID             int64
	Code           string
	ScheduleConfig *string // nullable JSON string
}

// createUpcomingRounds ดูล่วงหน้า 30 วัน แล้วสร้างรอบที่ยังไม่มี
//
// Flow:
//  1. ดึง lottery_types ที่ active + มี schedule_config (YEEKEE มี NULL → ข้าม)
//  2. Parse JSON schedule_config ต่อ type
//  3. วนแต่ละวัน (0-30 วันข้างหน้า)
//  4. เช็ค shouldRunForDayType → สร้าง round_number → เช็คซ้ำ → INSERT
func createUpcomingRounds(db *gorm.DB) {
	now := time.Now()

	// ─── 1) ดึง lottery_types + schedule_config ─────────────────
	var rows []lotteryTypeRow
	err := db.Table("lottery_types").
		Select("id, code, schedule_config").
		Where("status = ? AND schedule_config IS NOT NULL", "active").
		Find(&rows).Error
	if err != nil {
		log.Printf("⚠️  createUpcomingRounds: failed to load lottery_types: %v", err)
		return
	}

	if len(rows) == 0 {
		return // ไม่มี type ที่ active + มี schedule → ข้าม (เช่น DB empty หลัง migrate)
	}

	// ─── 2) Parse JSON → struct ─────────────────────────────────
	type parsedSchedule struct {
		ID        int64
		Code      string
		DayType   string
		OpenHour  int
		OpenMin   int
		CloseHour int
		CloseMin  int
	}
	schedules := make([]parsedSchedule, 0, len(rows))
	for _, row := range rows {
		if row.ScheduleConfig == nil || *row.ScheduleConfig == "" {
			continue
		}
		var cfg ScheduleConfig
		if err := json.Unmarshal([]byte(*row.ScheduleConfig), &cfg); err != nil {
			log.Printf("⚠️  invalid schedule_config for %s: %v", row.Code, err)
			continue
		}
		oh, om := parseHHMM(cfg.OpenTime)
		ch, cm := parseHHMM(cfg.CloseTime)
		if oh < 0 || ch < 0 {
			log.Printf("⚠️  invalid time format for %s: open=%s close=%s", row.Code, cfg.OpenTime, cfg.CloseTime)
			continue
		}
		schedules = append(schedules, parsedSchedule{
			ID: row.ID, Code: row.Code, DayType: cfg.DayType,
			OpenHour: oh, OpenMin: om, CloseHour: ch, CloseMin: cm,
		})
	}

	totalCreated := 0

	// ─── 3) วนแต่ละวัน (วันนี้ → 30 วันข้างหน้า) ────────────────
	for dayOffset := 0; dayOffset <= lookAheadDays; dayOffset++ {
		date := now.AddDate(0, 0, dayOffset)
		dateOnly := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())

		// ─── 4) วนแต่ละ schedule ─────────────────────────────────
		for _, s := range schedules {
			if !shouldRunForDayType(s.DayType, dateOnly) {
				continue
			}

			// round_number = YYYYMMDD (เช่น "20260404")
			roundNumber := dateOnly.Format("20060102")

			// เช็คว่ามีอยู่แล้วหรือยัง (unique: lottery_type_id + round_number)
			var existCount int64
			db.Table("lottery_rounds").
				Where("lottery_type_id = ? AND round_number = ?", s.ID, roundNumber).
				Count(&existCount)
			if existCount > 0 {
				continue // มีอยู่แล้ว → ข้าม
			}

			// คำนวณ open_time / close_time
			openTime := time.Date(dateOnly.Year(), dateOnly.Month(), dateOnly.Day(),
				s.OpenHour, s.OpenMin, 0, 0, dateOnly.Location())
			closeTime := time.Date(dateOnly.Year(), dateOnly.Month(), dateOnly.Day(),
				s.CloseHour, s.CloseMin, 0, 0, dateOnly.Location())

			// หุ้นที่ปิดข้ามวัน (เช่น ดาวโจนส์ 20:30-03:00)
			if closeTime.Before(openTime) {
				closeTime = closeTime.AddDate(0, 0, 1)
			}

			// INSERT — agent_node_id=NULL (global, ตามกฏ multi_agent_scoping)
			result := db.Exec(`INSERT INTO lottery_rounds
				(lottery_type_id, round_number, round_date, open_time, close_time, status, result_top3, result_top2, result_bottom2, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, 'upcoming', '', '', '', ?, ?)`,
				s.ID, roundNumber, dateOnly, openTime, closeTime, now, now)

			if result.Error != nil {
				// ส่วนใหญ่ duplicate key race → ไม่ log
				continue
			}
			totalCreated++
		}
	}

	if totalCreated > 0 {
		log.Printf("📅 Auto-created %d rounds (look-ahead %d days, %d schedules)", totalCreated, lookAheadDays, len(schedules))
	}
}

// =============================================================================
// GetDefaultSchedules — expose สำหรับ API (admin UI แสดงตาราง schedule)
// =============================================================================

// ScheduleInfo ข้อมูล schedule สำหรับแสดงใน admin UI
type ScheduleInfo struct {
	LotteryCode string `json:"lottery_code"`
	LotteryName string `json:"lottery_name"`
	OpenTime    string `json:"open_time"`  // "HH:MM"
	CloseTime   string `json:"close_time"` // "HH:MM"
	DayType     string `json:"day_type"`   // "daily" | "weekday" | "thai_gov"
}

// GetDefaultSchedules คืนตาราง schedule ทั้งหมด (อ่านจาก DB)
// ⭐ เปลี่ยนจากเดิม: อ่านจาก DB แทน hardcode
// ถ้า DB ว่าง → คืน empty slice
func GetDefaultSchedules(db *gorm.DB) []ScheduleInfo {
	type row struct {
		Code           string
		Name           string
		ScheduleConfig *string
	}
	var rows []row
	err := db.Table("lottery_types").
		Select("code, name, schedule_config").
		Where("status = ? AND schedule_config IS NOT NULL", "active").
		Order("sort_order ASC").
		Find(&rows).Error
	if err != nil {
		log.Printf("⚠️  GetDefaultSchedules: %v", err)
		return []ScheduleInfo{}
	}

	result := make([]ScheduleInfo, 0, len(rows))
	for _, r := range rows {
		if r.ScheduleConfig == nil {
			continue
		}
		var cfg ScheduleConfig
		if err := json.Unmarshal([]byte(*r.ScheduleConfig), &cfg); err != nil {
			continue
		}
		result = append(result, ScheduleInfo{
			LotteryCode: r.Code,
			LotteryName: r.Name,
			OpenTime:    cfg.OpenTime,
			CloseTime:   cfg.CloseTime,
			DayType:     cfg.DayType,
		})
	}
	return result
}
