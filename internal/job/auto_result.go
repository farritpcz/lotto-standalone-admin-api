// Package job — auto_result.go
// Auto-result job สำหรับหวยหุ้น — ดึงผลจากดัชนีตลาดหุ้น + settle bets อัตโนมัติ
//
// ⭐ วิธีคำนวณผลหวยหุ้น:
//   ดัชนี SET Index ปิดตลาด = 1,432.56
//   → เอาทุกตัวเลข (ไม่รวมจุด) = "143256"
//   → 3 ตัวบน = 3 ตัวท้าย = "256"
//   → 2 ตัวบน = 2 ตัวท้าย = "56"
//   → 2 ตัวล่าง = ใช้ทศนิยม 2 หลัก = "56" (หรือจาก index อื่น)
//
// ⭐ ระบบ configurable:
//   - ตั้งค่า URL + parser ผ่าน settings table หรือ env
//   - รองรับหลายตลาด (SET, Dow Jones, Nikkei, etc.)
//   - Admin สามารถ override ผลได้เสมอ (ผ่าน SubmitResult)
//
// ⭐ Flow:
//   1. Cron ทุก 5 นาที: หารอบที่ closed + เลยเวลาปิด
//   2. ดึงดัชนีจาก API/URL ที่ตั้งค่าไว้
//   3. คำนวณผล (3 ตัวบน, 2 ตัวบน, 2 ตัวล่าง)
//   4. บันทึกผล + เรียก SettleRound + commission
//
// ความสัมพันธ์:
// - ใช้ lotto-core: payout.SettleRound()
// - share DB กับ member-api (#3)
// - SubmitResult handler ยังใช้ได้ (admin manual override)
package job

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/farritpcz/lotto-core/payout"
	coreTypes "github.com/farritpcz/lotto-core/types"
)

// ─── Stock market source configuration ────────────────────
// แต่ละ lottery code map กับ URL + parser ที่ใช้ดึงผล
//
// ⚠️ URL เหล่านี้เป็นตัวอย่าง — ต้องตั้งค่าจริงใน settings table
// หรือ env variable STOCK_API_URL_{CODE}
type stockSource struct {
	LotteryCode string // "STOCK_TH", "STOCK_FOREIGN"
	FetchURL    string // URL สำหรับดึงข้อมูล
	// Parser: รับ response body → คืน index number string เช่น "1432.56"
	// ถ้าไม่ set → ใช้ defaultParser (หา pattern ตัวเลข+จุดทศนิยม)
}

// StartAutoResultJob เริ่ม cron job สำหรับดึงผลหวยหุ้นอัตโนมัติ
//
// ตรวจสอบทุก 5 นาที:
//   1. หารอบหุ้นที่ปิดรับแทงแล้ว (status = "closed")
//   2. เช็คว่าเลยเวลาออกผลแล้วหรือยัง (close_time + 30 นาที buffer)
//   3. ดึงผลจากดัชนีตลาด
//   4. คำนวณ 3 ตัว / 2 ตัว จากดัชนี
//   5. บันทึกผล + settle bets + commission
//
// เรียกจาก cmd/server/main.go ตอน startup
func StartAutoResultJob(db *gorm.DB) {
	log.Println("📈 Auto-result job started (check stock results every 5min)")

	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		for range ticker.C {
			processStockResults(db)
		}
	}()
}

// processStockResults หารอบหุ้นที่ closed + ดึงผลอัตโนมัติ
func processStockResults(db *gorm.DB) {
	now := time.Now()

	// ─── หารอบที่ closed + เป็นหวยหุ้น + เลยเวลาปิด 30 นาที ──
	// buffer 30 นาทีหลังปิดตลาด เพื่อรอให้ index final
	type ClosedRound struct {
		ID            int64
		LotteryTypeID int64
		RoundNumber   string
		CloseTime     time.Time
		LotteryCode   string
	}

	var rounds []ClosedRound
	db.Table("lottery_rounds").
		Select("lottery_rounds.id, lottery_rounds.lottery_type_id, lottery_rounds.round_number, lottery_rounds.close_time, lottery_types.code as lottery_code").
		Joins("JOIN lottery_types ON lottery_types.id = lottery_rounds.lottery_type_id").
		Where("lottery_rounds.status = ?", "closed").
		Where("lottery_types.code IN ?", []string{"STOCK_TH", "STOCK_FOREIGN"}).
		Where("lottery_rounds.close_time < ?", now.Add(-30*time.Minute)). // เลย 30 นาทีแล้ว
		Find(&rounds)

	if len(rounds) == 0 {
		return
	}

	for _, round := range rounds {
		log.Printf("📈 Auto-result: checking %s round %s (ID: %d)", round.LotteryCode, round.RoundNumber, round.ID)

		// ─── ดึงผลดัชนี ─────────────────────────────────────
		// ลองดึงจาก settings table ก่อน: stock_api_url_STOCK_TH
		var apiURL string
		db.Table("settings").Select("value").
			Where("`key` = ?", fmt.Sprintf("stock_api_url_%s", round.LotteryCode)).
			Scan(&apiURL)

		if apiURL == "" {
			// ⚠️ ไม่มี URL ตั้งค่า → ข้ามรอบนี้ (admin ต้องกรอกเอง)
			log.Printf("⚠️ No stock API URL configured for %s — admin must submit result manually", round.LotteryCode)
			log.Printf("   Set key 'stock_api_url_%s' in settings table", round.LotteryCode)
			continue
		}

		// ─── Fetch จาก API ──────────────────────────────────
		indexValue, err := fetchStockIndex(apiURL)
		if err != nil {
			log.Printf("⚠️ Failed to fetch stock index for %s: %v", round.LotteryCode, err)
			continue
		}

		// ─── คำนวณผลหวย จากดัชนี ────────────────────────────
		top3, top2, bottom2 := calculateStockResult(indexValue)
		if top3 == "" {
			log.Printf("⚠️ Failed to parse stock index '%s' for %s", indexValue, round.LotteryCode)
			continue
		}

		log.Printf("📊 Stock result: %s → top3=%s, top2=%s, bottom2=%s", indexValue, top3, top2, bottom2)

		// ─── บันทึกผล + settle ──────────────────────────────
		settleStockRound(db, round.ID, round.LotteryTypeID, round.RoundNumber, top3, top2, bottom2)
	}
}

// fetchStockIndex ดึงดัชนีตลาดจาก URL ที่ตั้งค่าไว้
//
// รองรับ 2 format ของ response:
//   1. JSON: {"index": "1432.56"} หรือ {"value": "1432.56"} หรือ {"close": "1432.56"}
//   2. Plain text: "1432.56"
//
// Returns: ค่าดัชนีเป็น string เช่น "1432.56"
func fetchStockIndex(apiURL string) (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body failed: %w", err)
	}

	bodyStr := strings.TrimSpace(string(body))

	// ─── ลอง parse เป็น JSON ก่อน ──────────────────────────
	var jsonData map[string]interface{}
	if err := json.Unmarshal([]byte(bodyStr), &jsonData); err == nil {
		// หา field ที่เป็นตัวเลข: index, value, close, last, price
		for _, key := range []string{"index", "value", "close", "last", "price", "data"} {
			if v, ok := jsonData[key]; ok {
				return fmt.Sprintf("%v", v), nil
			}
		}
		return "", fmt.Errorf("JSON response has no recognized field (index/value/close/last/price)")
	}

	// ─── ถ้าไม่ใช่ JSON → ใช้เป็น plain text ──────────────
	// หา pattern ตัวเลข+จุดทศนิยม เช่น "1,432.56" หรือ "1432.56"
	re := regexp.MustCompile(`[\d,]+\.\d+`)
	match := re.FindString(bodyStr)
	if match != "" {
		return match, nil
	}

	return "", fmt.Errorf("cannot parse response: %s", bodyStr[:min(100, len(bodyStr))])
}

// calculateStockResult คำนวณผลหวยจากดัชนีตลาด
//
// วิธีคำนวณ (มาตรฐานเว็บหวยไทย):
//   ดัชนี "1,432.56"
//   → ลบ comma + จุด → "143256"
//   → 3 ตัวบน (top3) = 3 ตัวท้าย = "256"
//   → 2 ตัวบน (top2) = 2 ตัวท้ายของ top3 = "56"
//   → 2 ตัวล่าง (bottom2) = ตัวเลขหลังจุดทศนิยม 2 ตัว = "56"
//
// ถ้า parse ไม่ได้ → return empty strings
func calculateStockResult(indexValue string) (top3, top2, bottom2 string) {
	// ลบ comma
	clean := strings.ReplaceAll(indexValue, ",", "")

	// แยกส่วนจำนวนเต็ม + ทศนิยม
	parts := strings.Split(clean, ".")
	if len(parts) < 2 {
		return "", "", ""
	}

	integerPart := parts[0] // เช่น "1432"
	decimalPart := parts[1] // เช่น "56"

	// ⚠️ ป้องกัน input ไม่ถูกต้อง (จุดทศนิยมแต่ไม่มีตัวเลข)
	if len(integerPart) == 0 || len(decimalPart) == 0 {
		return "", "", ""
	}

	// ─── 2 ตัวล่าง = ทศนิยม 2 ตัว ────────────────────────
	// pad ด้วย 0 ถ้าทศนิยมมีตัวเดียว
	if len(decimalPart) < 2 {
		decimalPart = decimalPart + "0"
	}
	bottom2 = decimalPart[:2]

	// ─── รวมตัวเลขทั้งหมด (ไม่มีจุด) ──────────────────────
	allDigits := integerPart + decimalPart[:2] // เช่น "143256"

	if len(allDigits) < 3 {
		return "", "", ""
	}

	// ─── 3 ตัวบน = 3 ตัวท้ายของ allDigits ──────────────────
	top3 = allDigits[len(allDigits)-3:]

	// ─── 2 ตัวบน = 2 ตัวท้ายของ top3 ──────────────────────
	top2 = top3[1:]

	return top3, top2, bottom2
}

// settleStockRound บันทึกผล + settle bets สำหรับรอบหุ้น
//
// ใช้ logic เดียวกับ SubmitResult handler แต่เรียกจาก cron
func settleStockRound(db *gorm.DB, roundID, lotteryTypeID int64, roundNumber, top3, top2, bottom2 string) {
	now := time.Now()

	// ─── 1. บันทึกผลลง DB ──────────────────────────────────
	db.Table("lottery_rounds").Where("id = ?", roundID).Updates(map[string]interface{}{
		"result_top3":    top3,
		"result_top2":    top2,
		"result_bottom2": bottom2,
		"status":         "resulted",
		"resulted_at":    &now,
	})

	// ─── 2. ดึง pending bets ────────────────────────────────
	type BetRow struct {
		ID             int64
		MemberID       int64
		LotteryRoundID int64
		Number         string
		Amount         float64
		Rate           float64
		BetTypeCode    string
	}

	var bets []BetRow
	db.Table("bets").
		Select("bets.id, bets.member_id, bets.lottery_round_id, bets.number, bets.amount, bets.rate, bet_types.code as bet_type_code").
		Joins("JOIN bet_types ON bet_types.id = bets.bet_type_id").
		Where("bets.lottery_round_id = ? AND bets.status = ?", roundID, "pending").
		Find(&bets)

	if len(bets) == 0 {
		log.Printf("ℹ️ No pending bets for stock round %s — result saved, no settlement needed", roundNumber)
		return
	}

	// ─── 3. แปลง → lotto-core types ────────────────────────
	coreBets := make([]coreTypes.Bet, 0, len(bets))
	for _, b := range bets {
		coreBets = append(coreBets, coreTypes.Bet{
			ID:       b.ID,
			MemberID: b.MemberID,
			RoundID:  b.LotteryRoundID,
			BetType:  coreTypes.BetType(b.BetTypeCode),
			Number:   b.Number,
			Amount:   b.Amount,
			Rate:     b.Rate,
			Status:   coreTypes.BetStatusPending,
		})
	}

	// ─── 4. SettleRound ────────────────────────────────────
	result := coreTypes.RoundResult{Top3: top3, Top2: top2, Bottom2: bottom2}
	output := payout.SettleRound(payout.SettleRoundInput{
		RoundID: roundID,
		Result:  result,
		Bets:    coreBets,
	})

	log.Printf("💰 Stock settle: round=%s, bets=%d, winners=%d, win=%.2f, profit=%.2f",
		roundNumber, len(bets), output.TotalWinners, output.TotalWinAmount, output.Profit)

	// ─── 5. อัพเดท bets + จ่ายเงิน (DB transaction) ────────
	tx := db.Begin()

	// อัพเดท bets
	resultMap := make(map[int64]coreTypes.BetResult)
	for _, r := range output.BetResults {
		resultMap[r.BetID] = r
	}
	for _, bet := range bets {
		r, ok := resultMap[bet.ID]
		if !ok {
			continue
		}
		tx.Table("bets").Where("id = ?", bet.ID).Updates(map[string]interface{}{
			"status":     string(r.Status),
			"win_amount": r.WinAmount,
			"settled_at": &now,
		})
	}

	// จ่ายเงินคนชนะ (group by member)
	winByMember := payout.GroupWinnersByMember(coreBets, output.BetResults)
	for memberID, totalWin := range winByMember {
		tx.Table("members").Where("id = ?", memberID).
			Update("balance", gorm.Expr("balance + ?", totalWin))

		// สร้าง win transaction
		tx.Exec(`
			INSERT INTO transactions (member_id, type, amount, reference_id, reference_type, note, created_at)
			VALUES (?, 'win', ?, ?, 'lottery_round', ?, NOW())
		`, memberID, totalWin, roundID, fmt.Sprintf("เงินรางวัลหวยหุ้นรอบ %s", roundNumber))
	}

	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		log.Printf("❌ Failed to settle stock round %s: %v", roundNumber, err)
		return
	}

	// ─── 6. Commission ─────────────────────────────────────
	go CalculateCommissions(db, roundID, 1)

	log.Printf("✅ Stock auto-result complete: %s → %s (winners: %d, payout: %.2f)",
		roundNumber, top3, output.TotalWinners, output.TotalWinAmount)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
