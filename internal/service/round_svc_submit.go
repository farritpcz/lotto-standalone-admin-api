// =============================================================================
// SubmitResult — กรอกผลรางวัล + settle bets + จ่ายเงิน
// แยกจาก round_svc.go (flow ยาว ~120 LOC)
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

// SubmitResult กรอกผลรางวัลและ settle ทุก bet ในรอบ
//
// Flow ทั้งหมด:
//  1. Validate round status (ต้องเป็น closed หรือ open)
//  2. บันทึกผลลง lottery_rounds
//  3. ดึง pending bets ทั้งหมด
//  4. แปลง bets → lotto-core format
//  5. เรียก payout.SettleRound() คำนวณแพ้ชนะ
//  6. อัพเดท bets (status, win_amount, settled_at)
//  7. จ่ายเงินคนชนะ (atomic balance update)
//  8. สร้าง transaction records
//
// ⭐ Commission คำนวณโดย caller (handler) เรียก job.CalculateCommissions()
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

	if len(dbBets) == 0 {
		return &SettleResult{RoundID: roundID}, nil
	}

	// ─── Step 4: แปลง DB bets → lotto-core format ──────────────
	// lotto-core ใช้ types.Bet struct แยกจาก model.Bet ของ GORM
	coreBets := toCoreBets(dbBets)

	// ─── Step 5: เรียก lotto-core SettleRound() ─────────────────
	// คำนวณ: แต่ละ bet ถูกหรือไม่? ถ้าถูกได้เท่าไร?
	settleOutput := payout.SettleRound(payout.SettleRoundInput{
		Bets:   coreBets,
		Result: result,
	})

	// ─── Step 6: สร้าง map betID → BetResult (lookup เร็วตอน update) ────
	resultMap := make(map[int64]coreTypes.BetResult, len(settleOutput.BetResults))
	for _, r := range settleOutput.BetResults {
		resultMap[r.BetID] = r
	}

	// ─── Step 7: อัพเดท bets ───────────────────────────────────
	for _, bet := range dbBets {
		r, exists := resultMap[bet.ID]
		if !exists {
			continue
		}
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

// toCoreBets แปลง []model.Bet (GORM) → []coreTypes.Bet (lotto-core)
func toCoreBets(dbBets []model.Bet) []coreTypes.Bet {
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
	return coreBets
}
