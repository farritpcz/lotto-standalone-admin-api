// =============================================================================
// VoidRound — ยกเลิกรอบ + refund ทุก bet + reverse wins (ถ้า resulted แล้ว)
// แยกจาก round_svc.go (flow ยาว ~95 LOC + เสี่ยง)
// =============================================================================
package service

import (
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"

	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

// VoidRound ยกเลิกรอบหวย + คืนเงินทุก bet
//
// ⚠️ นี่คือ operation ที่รุนแรง — ใช้เมื่อ:
//   - กรอกผลผิด (ต้องยกเลิกรอบที่ resulted แล้ว)
//   - ระบบมีปัญหา (ต้องยกเลิกรอบที่ open/closed)
//   - เหตุฉุกเฉิน (admin ตัดสินใจยกเลิก)
//
// Flow:
//  1. Validate: ได้เฉพาะ open / closed / resulted
//  2. ถ้า resulted → reverse payouts (หักเงินรางวัลคืนจากคนที่ถูก)
//  3. ทุก bet → status='refunded' + คืนเงินเดิมพัน
//  4. สร้าง transaction "refund" สำหรับทุก bet
//  5. Round status → 'voided'
//  6. บันทึก reason ใน reject_reason field (reuse)
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
	if round.Status == "resulted" {
		s.reverseWins(roundID, reason, now, &round, vr)
	}

	// ─── Step 3: refund ทุก bet (pending + won + lost) ──────────
	s.refundAllBets(roundID, reason, now, &round, vr)

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

// reverseWins หักเงินรางวัลคืนจากคนที่ถูก (เฉพาะรอบ resulted)
// สร้าง transaction type "void_win" (amount ติดลบ)
func (s *RoundService) reverseWins(roundID int64, reason string, now time.Time, round *model.LotteryRound, vr *VoidResult) {
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

// refundAllBets คืนเงินเดิมพันให้สมาชิกทุกคนในรอบ + อัพเดท bet status = refunded
func (s *RoundService) refundAllBets(roundID int64, reason string, now time.Time, round *model.LotteryRound, vr *VoidResult) {
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
}
