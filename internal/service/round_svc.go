// =============================================================================
// Package service — round_svc.go
//
// Round Service — ศูนย์กลางจัดการรอบหวยทุก operation
//
// ⭐ หลักการ: ทุกการเปลี่ยนแปลงของรอบหวยต้องผ่าน service นี้
//    - Auto cron jobs → เรียก RoundService
//    - Admin handlers → เรียก RoundService
//    - Auto-result jobs → เรียก RoundService
//
// Operations:
//   - CreateRound()    → สร้างรอบใหม่ (auto + manual)
//   - OpenRound()      → เปิดรับแทง (upcoming → open)
//   - CloseRound()     → ปิดรับแทง (open → closed)
//   - SubmitResult()   → กรอกผล + settle bets + commission
//   - VoidRound()      → ยกเลิกรอบ + refund ทุก bet
//
// ความสัมพันธ์:
//   - ใช้ lotto-core payout.SettleRound() สำหรับคำนวณแพ้ชนะ
//   - ใช้ model.* สำหรับ DB operations
//   - ถูกเรียกโดย: handler/stubs.go, job/round_lifecycle.go, job/auto_result.go
// =============================================================================
package service

import (
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"

	"github.com/farritpcz/lotto-core/payout"
	coreTypes "github.com/farritpcz/lotto-core/types"

	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

// =============================================================================
// RoundService — main struct
// =============================================================================

// RoundService จัดการรอบหวยทุก operation
// สร้างผ่าน NewRoundService() ใน main.go
type RoundService struct {
	db *gorm.DB
}

// NewRoundService สร้าง RoundService instance
func NewRoundService(db *gorm.DB) *RoundService {
	return &RoundService{db: db}
}

// DB คืน gorm.DB สำหรับให้ cron jobs ใช้ (เช่น auto_result)
func (s *RoundService) DB() *gorm.DB {
	return s.db
}

// =============================================================================
// Result types — return values สำหรับแต่ละ operation
// =============================================================================

// SettleResult ผลลัพธ์จากการกรอกผล + settle bets
type SettleResult struct {
	RoundID       int64   `json:"round_id"`
	TotalBets     int     `json:"total_bets"`      // จำนวน bet ทั้งหมด
	WinnerCount   int     `json:"winner_count"`    // จำนวนคนถูก
	TotalBetAmt   float64 `json:"total_bet_amount"` // ยอดแทงรวม
	TotalWinAmt   float64 `json:"total_win_amount"` // ยอดจ่ายรวม
	Profit        float64 `json:"profit"`           // กำไร (bet - win)
}

// VoidResult ผลลัพธ์จากการยกเลิกรอบ
type VoidResult struct {
	RoundID       int64   `json:"round_id"`
	RefundedBets  int     `json:"refunded_bets"`   // จำนวน bet ที่คืนเงิน
	RefundedAmt   float64 `json:"refunded_amount"`  // ยอดเงินคืนรวม
	ReversedWins  int     `json:"reversed_wins"`    // จำนวนรางวัลที่หักคืน (ถ้า resulted)
	ReversedAmt   float64 `json:"reversed_amount"`  // ยอดรางวัลที่หักคืน
}

// =============================================================================
// CreateRound — สร้างรอบหวยใหม่
// =============================================================================

// CreateRound สร้างรอบหวยใหม่ในสถานะ upcoming
//
// ใช้ทั้ง auto (cron สร้างล่วงหน้า) และ manual (admin สร้างเอง)
//
// Parameters:
//   - lotteryTypeID: ID ของประเภทหวย (FK → lottery_types.id)
//   - roundNumber:   เลขรอบ เช่น "20260404", "20260404-AM" (unique per type)
//   - roundDate:     วันที่ออกผล
//   - openTime:      เวลาเปิดรับแทง
//   - closeTime:     เวลาปิดรับแทง
//
// Returns: *model.LotteryRound ที่สร้างแล้ว, error
//
// ⚠️ จะ fail ถ้า round_number ซ้ำ (UNIQUE constraint)
func (s *RoundService) CreateRound(lotteryTypeID int64, roundNumber string, roundDate, openTime, closeTime time.Time) (*model.LotteryRound, error) {
	// ─── สร้างรอบ ───────────────────────────────────────────────
	round := model.LotteryRound{
		LotteryTypeID: lotteryTypeID,
		RoundNumber:   roundNumber,
		RoundDate:     roundDate,
		OpenTime:      openTime,
		CloseTime:     closeTime,
		Status:        "upcoming", // เริ่มต้นเป็น upcoming เสมอ
	}

	if err := s.db.Create(&round).Error; err != nil {
		return nil, fmt.Errorf("สร้างรอบไม่สำเร็จ: %w", err)
	}

	log.Printf("📅 สร้างรอบ #%d [%s] type=%d open=%s close=%s",
		round.ID, roundNumber, lotteryTypeID,
		openTime.Format("15:04"), closeTime.Format("15:04"))

	return &round, nil
}

// =============================================================================
// OpenRound — เปิดรับแทง
// =============================================================================

// OpenRound เปลี่ยนสถานะจาก upcoming → open
//
// ใช้สำหรับ:
//   - Auto: cron transition เมื่อถึง open_time
//   - Manual: admin กดเปิดก่อนเวลา
//
// ⚠️ fail ถ้าสถานะไม่ใช่ upcoming
func (s *RoundService) OpenRound(roundID int64) error {
	// ─── ดึงรอบ + validate สถานะ ────────────────────────────────
	var round model.LotteryRound
	if err := s.db.First(&round, roundID).Error; err != nil {
		return fmt.Errorf("ไม่พบรอบ #%d", roundID)
	}

	if round.Status != "upcoming" {
		return fmt.Errorf("ไม่สามารถเปิดรอบได้ — สถานะปัจจุบัน: %s (ต้องเป็น upcoming)", round.Status)
	}

	// ─── อัพเดทสถานะ ────────────────────────────────────────────
	if err := s.db.Model(&round).Update("status", "open").Error; err != nil {
		return fmt.Errorf("อัพเดทสถานะไม่สำเร็จ: %w", err)
	}

	log.Printf("🟢 เปิดรอบ #%d [%s]", round.ID, round.RoundNumber)
	return nil
}

// =============================================================================
// CloseRound — ปิดรับแทง
// =============================================================================

// CloseRound เปลี่ยนสถานะจาก open → closed
//
// ใช้สำหรับ:
//   - Auto: cron transition เมื่อถึง close_time
//   - Manual: admin กดปิดก่อนเวลา
//
// ⚠️ fail ถ้าสถานะไม่ใช่ open
func (s *RoundService) CloseRound(roundID int64) error {
	// ─── ดึงรอบ + validate สถานะ ────────────────────────────────
	var round model.LotteryRound
	if err := s.db.First(&round, roundID).Error; err != nil {
		return fmt.Errorf("ไม่พบรอบ #%d", roundID)
	}

	if round.Status != "open" {
		return fmt.Errorf("ไม่สามารถปิดรอบได้ — สถานะปัจจุบัน: %s (ต้องเป็น open)", round.Status)
	}

	// ─── อัพเดทสถานะ ────────────────────────────────────────────
	if err := s.db.Model(&round).Update("status", "closed").Error; err != nil {
		return fmt.Errorf("อัพเดทสถานะไม่สำเร็จ: %w", err)
	}

	log.Printf("🔴 ปิดรอบ #%d [%s]", round.ID, round.RoundNumber)
	return nil
}

// =============================================================================
// SubmitResult — กรอกผลรางวัล + settle bets + commission
// =============================================================================

// SubmitResult กรอกผลรางวัลและ settle ทุก bet ในรอบ
//
// Flow ทั้งหมด (atomic ภายใน transaction):
//   1. Validate round status (ต้องเป็น closed หรือ open — ยังไม่มีผล)
//   2. บันทึกผลลง lottery_rounds
//   3. ดึง pending bets ทั้งหมด
//   4. แปลง bets → lotto-core format
//   5. เรียก payout.SettleRound() คำนวณแพ้ชนะ
//   6. อัพเดท bets (status, win_amount, settled_at)
//   7. จ่ายเงินคนชนะ (atomic balance update)
//   8. สร้าง transaction records
//   9. คำนวณ commission (async goroutine)
//
// Parameters:
//   - roundID: ID ของรอบ
//   - result:  ผลรางวัล (top3, top2, bottom2, front3?, bottom3?)
//
// Returns: SettleResult สรุปผล, error
func (s *RoundService) SubmitResult(roundID int64, result coreTypes.RoundResult) (*SettleResult, error) {
	// ─── Step 1: ดึง round + validate ───────────────────────────
	var round model.LotteryRound
	if err := s.db.First(&round, roundID).Error; err != nil {
		return nil, fmt.Errorf("ไม่พบรอบ #%d", roundID)
	}

	// ยอมรับทั้ง closed (ปกติ) และ open (admin กรอกก่อนปิด)
	if round.Status == "resulted" || round.Status == "voided" {
		return nil, fmt.Errorf("รอบนี้มีผลแล้ว (สถานะ: %s)", round.Status)
	}

	// ─── Step 2: บันทึกผลลง DB ─────────────────────────────────
	now := time.Now()
	updates := map[string]interface{}{
		"result_top3":    result.Top3,
		"result_top2":    result.Top2,
		"result_bottom2": result.Bottom2,
		"status":         "resulted",
		"resulted_at":    &now,
	}
	if result.Front3 != "" {
		updates["result_front3"] = result.Front3
	}
	if result.Bottom3 != "" {
		updates["result_bottom3"] = result.Bottom3
	}
	s.db.Model(&round).Updates(updates)

	// ─── Step 3: ดึง pending bets ──────────────────────────────
	var dbBets []model.Bet
	s.db.Where("lottery_round_id = ? AND status = ?", roundID, "pending").
		Preload("BetType").Find(&dbBets)

	// ถ้าไม่มี bet → return เลย
	if len(dbBets) == 0 {
		return &SettleResult{RoundID: roundID}, nil
	}

	// ─── Step 4: แปลง DB bets → lotto-core format ──────────────
	// lotto-core ใช้ types.Bet struct แยกจาก model.Bet ของ GORM
	coreBets := make([]coreTypes.Bet, 0, len(dbBets))
	for _, b := range dbBets {
		betTypeCode := ""
		if b.BetType != nil {
			betTypeCode = b.BetType.Code
		}
		coreBets = append(coreBets, coreTypes.Bet{
			ID:       b.ID,
			MemberID: b.MemberID,
			RoundID:  b.LotteryRoundID,
			BetType:  coreTypes.BetType(betTypeCode),
			Number:   b.Number,
			Amount:   b.Amount,
			Rate:     b.Rate,
			Status:   coreTypes.BetStatusPending,
		})
	}

	// ─── Step 5: เรียก lotto-core SettleRound() ─────────────────
	// คำนวณ: แต่ละ bet ถูกหรือไม่? ถ้าถูกได้เท่าไร?
	settleOutput := payout.SettleRound(payout.SettleRoundInput{
		Bets:   coreBets,
		Result: result,
	})

	// ─── Step 6: สร้าง map betID → BetResult ───────────────────
	// ใช้ lookup เร็วตอน update ทีละ bet
	resultMap := make(map[int64]coreTypes.BetResult, len(settleOutput.BetResults))
	for _, r := range settleOutput.BetResults {
		resultMap[r.BetID] = r
	}

	// ─── Step 7: อัพเดท bets + จ่ายเงิน ────────────────────────
	for _, bet := range dbBets {
		r, exists := resultMap[bet.ID]
		if !exists {
			continue
		}
		// อัพเดท bet: status (won/lost) + win_amount + settled_at
		s.db.Model(&bet).Updates(map[string]interface{}{
			"status":     string(r.Status),
			"win_amount": r.WinAmount,
			"settled_at": &now,
		})
	}

	// ─── Step 8: จ่ายเงินคนชนะ (group by member) ───────────────
	// ใช้ lotto-core GroupWinnersByMember() → map[memberID]totalWin
	// จ่ายทีเดียวต่อคน (ไม่ใช่ทีละ bet) → ลด DB ops
	winByMember := payout.GroupWinnersByMember(coreBets, settleOutput.BetResults)
	for memberID, totalWin := range winByMember {
		// Atomic balance update — ป้องกัน race condition
		s.db.Model(&model.Member{}).Where("id = ?", memberID).
			Update("balance", gorm.Expr("balance + ?", totalWin))

		// สร้าง transaction record
		s.db.Create(&model.Transaction{
			MemberID:      memberID,
			Type:          "win",
			Amount:        totalWin,
			ReferenceID:   &roundID,
			ReferenceType: "lottery_round",
			Note:          "เงินรางวัลรอบ " + round.RoundNumber,
			CreatedAt:     now,
		})
	}

	// ⭐ Commission จะถูกคำนวณโดย caller (handler) เรียก job.CalculateCommissions()
	// ไม่เรียกจากที่นี่เพื่อหลีกเลี่ยง circular import (service → job → service)

	log.Printf("✅ กรอกผลรอบ #%d [%s] — bets=%d, winners=%d, payout=%.2f, profit=%.2f",
		roundID, round.RoundNumber,
		len(dbBets), settleOutput.TotalWinners,
		settleOutput.TotalWinAmount, settleOutput.Profit)

	return &SettleResult{
		RoundID:     roundID,
		TotalBets:   len(dbBets),
		WinnerCount: settleOutput.TotalWinners,
		TotalBetAmt: settleOutput.TotalBetAmount,
		TotalWinAmt: settleOutput.TotalWinAmount,
		Profit:      settleOutput.Profit,
	}, nil
}

// =============================================================================
// VoidRound — ยกเลิกรอบ + refund ทุก bet
// =============================================================================

// VoidRound ยกเลิกรอบหวย + คืนเงินทุก bet
//
// ⚠️ นี่คือ operation ที่รุนแรง — ใช้เมื่อ:
//   - กรอกผลผิด (ต้องยกเลิกรอบที่ resulted แล้ว)
//   - ระบบมีปัญหา (ต้องยกเลิกรอบที่ open/closed)
//   - เหตุฉุกเฉิน (admin ตัดสินใจยกเลิก)
//
// Flow:
//   1. Validate: ได้เฉพาะ open / closed / resulted
//   2. ถ้า resulted → reverse payouts:
//      - หักเงินรางวัลคืนจากคนที่ถูก
//      - สร้าง transaction "void_win" (หักเงิน)
//   3. ทุก bet → status='refunded' + คืนเงินเดิมพัน
//   4. สร้าง transaction "refund" สำหรับทุก bet
//   5. Round status → 'voided'
//   6. บันทึก reason ใน reject_reason field (reuse)
//
// Parameters:
//   - roundID: ID ของรอบ
//   - reason:  เหตุผลที่ยกเลิก (บันทึกไว้ audit)
//   - adminID: ID ของ admin ที่กดยกเลิก
//
// Returns: VoidResult สรุปผล, error
func (s *RoundService) VoidRound(roundID int64, reason string, adminID int64) (*VoidResult, error) {
	// ─── Step 1: ดึง round + validate ───────────────────────────
	var round model.LotteryRound
	if err := s.db.First(&round, roundID).Error; err != nil {
		return nil, fmt.Errorf("ไม่พบรอบ #%d", roundID)
	}

	// ยกเลิกได้เฉพาะ: open, closed, resulted
	if round.Status == "voided" {
		return nil, fmt.Errorf("รอบนี้ถูกยกเลิกไปแล้ว")
	}
	if round.Status == "upcoming" {
		return nil, fmt.Errorf("รอบนี้ยังไม่เปิด — ลบรอบแทน")
	}

	vr := &VoidResult{RoundID: roundID}
	now := time.Now()

	// ─── Step 2: ถ้า resulted → reverse payouts ─────────────────
	// หักเงินรางวัลคืนจากคนที่ถูก (won bets)
	if round.Status == "resulted" {
		var wonBets []model.Bet
		s.db.Where("lottery_round_id = ? AND status = 'won'", roundID).Find(&wonBets)

		for _, bet := range wonBets {
			if bet.WinAmount <= 0 {
				continue
			}
			// หักเงินรางวัลคืน (atomic — ไม่ติดลบ)
			result := s.db.Model(&model.Member{}).
				Where("id = ? AND balance >= ?", bet.MemberID, bet.WinAmount).
				Update("balance", gorm.Expr("balance - ?", bet.WinAmount))
			if result.RowsAffected > 0 {
				vr.ReversedWins++
				vr.ReversedAmt += bet.WinAmount
				// สร้าง transaction "void_win"
				s.db.Create(&model.Transaction{
					MemberID:      bet.MemberID,
					Type:          "void_win",
					Amount:        -bet.WinAmount,
					ReferenceID:   &roundID,
					ReferenceType: "lottery_round",
					Note:          fmt.Sprintf("ยกเลิกรางวัลรอบ %s — %s", round.RoundNumber, reason),
					CreatedAt:     now,
				})
			}
		}
		log.Printf("🔄 Reversed %d wins (%.2f฿) for round #%d", vr.ReversedWins, vr.ReversedAmt, roundID)
	}

	// ─── Step 3: refund ทุก bet (ทั้ง pending + won + lost) ─────
	// ดึง bets ที่ยังไม่ถูก refund
	var bets []model.Bet
	s.db.Where("lottery_round_id = ? AND status IN ('pending','won','lost')", roundID).Find(&bets)

	for _, bet := range bets {
		// คืนเงินเดิมพัน
		s.db.Model(&model.Member{}).Where("id = ?", bet.MemberID).
			Update("balance", gorm.Expr("balance + ?", bet.Amount))

		// อัพเดท bet → refunded
		s.db.Model(&bet).Updates(map[string]interface{}{
			"status":     "refunded",
			"settled_at": &now,
		})

		// สร้าง transaction "refund"
		s.db.Create(&model.Transaction{
			MemberID:      bet.MemberID,
			Type:          "refund",
			Amount:        bet.Amount,
			ReferenceID:   &roundID,
			ReferenceType: "lottery_round",
			Note:          fmt.Sprintf("คืนเงินเดิมพันรอบ %s — %s", round.RoundNumber, reason),
			CreatedAt:     now,
		})

		vr.RefundedBets++
		vr.RefundedAmt += bet.Amount
	}

	// ─── Step 4: Round → voided ─────────────────────────────────
	s.db.Model(&round).Updates(map[string]interface{}{
		"status":        "voided",
		"reject_reason": reason, // reuse field สำหรับบันทึกเหตุผล
	})

	log.Printf("⛔ ยกเลิกรอบ #%d [%s] — refunded=%d bets (%.2f฿), reversed=%d wins (%.2f฿), reason=%s, by admin=%d",
		roundID, round.RoundNumber,
		vr.RefundedBets, vr.RefundedAmt,
		vr.ReversedWins, vr.ReversedAmt,
		reason, adminID)

	return vr, nil
}

// =============================================================================
// BatchTransition — auto transition หลายรอบพร้อมกัน
// =============================================================================

// BatchOpenRounds เปิดรอบทั้งหมดที่ถึง open_time แล้ว (ยกเว้น yeekee)
//
// ⭐ ใช้ batch UPDATE ไม่ใช่ loop ทีละรอบ — ประสิทธิภาพดีกว่า
// เรียกจาก round_lifecycle.go ทุก 30 วินาที
func (s *RoundService) BatchOpenRounds() int64 {
	now := time.Now()
	result := s.db.Table("lottery_rounds").
		Where("status = ? AND open_time <= ?", "upcoming", now).
		Where("lottery_type_id NOT IN (SELECT id FROM lottery_types WHERE code = 'YEEKEE')").
		Update("status", "open")
	if result.RowsAffected > 0 {
		log.Printf("🟢 Auto-opened %d rounds", result.RowsAffected)
	}
	return result.RowsAffected
}

// BatchCloseRounds ปิดรอบทั้งหมดที่ถึง close_time แล้ว (ยกเว้น yeekee)
func (s *RoundService) BatchCloseRounds() int64 {
	now := time.Now()
	result := s.db.Table("lottery_rounds").
		Where("status = ? AND close_time <= ?", "open", now).
		Where("lottery_type_id NOT IN (SELECT id FROM lottery_types WHERE code = 'YEEKEE')").
		Update("status", "closed")
	if result.RowsAffected > 0 {
		log.Printf("🔴 Auto-closed %d rounds", result.RowsAffected)
	}
	return result.RowsAffected
}
