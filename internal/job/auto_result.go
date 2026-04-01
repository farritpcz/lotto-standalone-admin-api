// Package job — auto_result.go
// Auto-result job สำหรับหวยหุ้น (ดึงผลจาก API ตลาดหุ้นอัตโนมัติ)
//
// ⭐ ใช้สำหรับหวยหุ้น (STOCK_TH, STOCK_FOREIGN) ที่ผลมาจากตลาดหุ้น
// หวยไทย/ลาว ยังต้อง admin กรอกเอง
// ยี่กีออกเองผ่าน cron ใน member-api (#3)
//
// ความสัมพันธ์:
// - ใช้ lotto-core: payout.SettleRound() ตอนได้ผลแล้ว
// - ทำงานร่วมกับ: standalone-member-api (#3) — share DB
// - provider-backoffice-api (#9) มี job คล้ายกัน
package job

import (
	"log"
	"time"

	"gorm.io/gorm"
)

// StartAutoResultJob เริ่ม cron job สำหรับดึงผลหวยหุ้นอัตโนมัติ
//
// ตรวจสอบทุก 5 นาที:
// 1. หารอบหุ้นที่ปิดรับแทงแล้ว (status = "closed")
// 2. เช็คว่าเลยเวลาออกผลแล้วหรือยัง
// 3. ดึงผลจาก API ตลาดหุ้น (TODO: integrate กับ stock API)
// 4. บันทึกผล + trigger payout
func StartAutoResultJob(db *gorm.DB) {
	log.Println("📈 Auto-result job started (check stock results every 5min)")

	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		for range ticker.C {
			checkAndFetchStockResults(db)
		}
	}()
}

// checkAndFetchStockResults ตรวจสอบ + ดึงผลหวยหุ้น
func checkAndFetchStockResults(db *gorm.DB) {
	// หารอบที่ closed + เป็นหวยหุ้น + เลยเวลาปิด
	type ClosedRound struct {
		ID            int64
		LotteryTypeID int64
		RoundNumber   string
		CloseTime     time.Time
	}

	var rounds []ClosedRound
	db.Table("lottery_rounds").
		Select("lottery_rounds.id, lottery_rounds.lottery_type_id, lottery_rounds.round_number, lottery_rounds.close_time").
		Joins("JOIN lottery_types ON lottery_types.id = lottery_rounds.lottery_type_id").
		Where("lottery_rounds.status = ? AND lottery_types.code IN ?", "closed", []string{"STOCK_TH", "STOCK_FOREIGN"}).
		Where("lottery_rounds.close_time < ?", time.Now()).
		Find(&rounds)

	if len(rounds) == 0 {
		return
	}

	for _, round := range rounds {
		log.Printf("📈 Checking stock result for round %s (ID: %d)", round.RoundNumber, round.ID)

		// TODO: ดึงผลจาก API ตลาดหุ้นจริง
		// ตัวอย่าง: fetchStockResult(round.LotteryTypeID, round.CloseTime)
		// สำหรับตอนนี้ — admin ต้องกรอกเองผ่าน admin panel

		// เมื่อได้ผลแล้ว:
		// 1. อัพเดท lottery_round: result_top3, result_top2, result_bottom2, status = "resulted"
		// 2. เรียก payout.SettleRound() → เทียบ bets → จ่ายเงิน
		// (ใช้ logic เดียวกับ handler SubmitResult)
	}
}

// TODO: fetchStockResult ดึงผลจาก API ตลาดหุ้น
// func fetchStockResult(lotteryTypeID int64, closeTime time.Time) (top3, top2, bottom2 string, err error) {
//     // เรียก API ตลาดหุ้น (SET Index, Dow Jones, etc.)
//     // ดึง closing price
//     // ตัดเลข 3 ตัว / 2 ตัว จาก index number
//     return "847", "47", "56", nil
// }
