// Package handler — yeekee_settle_handler.go
// Manual settle รอบยี่กี (admin กดออกผลด้วยมือเมื่อ cron พลาด)
// ประกอบด้วย:
//   - ManualSettleYeekeeRound (endpoint)
//   - settleYeekeeBets (core payout + wallet update)
//   - logManualSettle (audit trail)
//
// แยกจาก yeekee_handler.go (monitoring + config) เพื่อให้ไฟล์อยู่ใต้ soft limit
package handler

import (
	"encoding/json"
	"log"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/farritpcz/lotto-core/payout"
	coreTypes "github.com/farritpcz/lotto-core/types"
	"github.com/farritpcz/lotto-core/yeekee"

	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

// =============================================================================
// POST /api/v1/yeekee/rounds/:id/settle
// แอดมินกดปุ่มออกผลเอง → คำนวณผลจาก Hash Commitment + settle bets
// =============================================================================
func (h *Handler) ManualSettleYeekeeRound(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	// ดึงรอบยี่กี
	var yr model.YeekeeRound
	if err := h.DB.First(&yr, id).Error; err != nil {
		fail(c, 404, "ไม่พบรอบยี่กี")
		return
	}

	// ต้องเป็น missed เท่านั้น (ไม่ให้กด settle รอบที่ resulted แล้ว)
	if yr.Status != "missed" {
		fail(c, 400, "รอบนี้ status='"+yr.Status+"' — กดออกผลได้เฉพาะรอบที่ missed เท่านั้น")
		return
	}

	// อัพเดท → calculating
	h.DB.Model(&yr).Update("status", "calculating")

	// ดึงเลขยิง
	var shoots []model.YeekeeShoot
	h.DB.Where("yeekee_round_id = ?", yr.ID).Find(&shoots)

	now := time.Now()

	if len(shoots) == 0 {
		// ไม่มีคนยิง → mark resulted ไม่มีผล
		h.DB.Model(&yr).Updates(map[string]interface{}{"status": "resulted", "total_shoots": 0})
		h.DB.Model(&model.LotteryRound{}).Where("id = ?", yr.LotteryRoundID).
			Updates(map[string]interface{}{"status": "resulted", "resulted_at": &now})

		logManualSettle(h.DB, c, yr, "", "no_shoots")
		ok(c, gin.H{"message": "ออกผลสำเร็จ (ไม่มีเลขยิง)", "result_number": ""})
		return
	}

	// แปลง → lotto-core types
	coreShots := make([]coreTypes.YeekeeShoot, 0, len(shoots))
	for _, s := range shoots {
		coreShots = append(coreShots, coreTypes.YeekeeShoot{
			ID: s.ID, RoundID: s.YeekeeRoundID, MemberID: s.MemberID,
			Number: s.Number, ShotAt: s.ShotAt,
		})
	}

	// คำนวณผล (Hash Commitment)
	resultNumber, roundResult, err := yeekee.CalculateResultWithSeed(yr.ServerSeed, coreShots)
	if err != nil {
		// fallback legacy
		resultNumber, roundResult, err = yeekee.CalculateResult(coreShots)
		if err != nil {
			h.DB.Model(&yr).Update("status", "missed") // revert
			fail(c, 500, "คำนวณผลไม่สำเร็จ: "+err.Error())
			return
		}
	}

	// บันทึกผลใน yeekee_round
	h.DB.Model(&yr).Updates(map[string]interface{}{
		"status": "resulted", "result_number": resultNumber,
		"total_shoots": len(shoots), "total_sum": yeekee.GetShootSum(coreShots),
	})

	// บันทึกผลใน lottery_round
	h.DB.Model(&model.LotteryRound{}).Where("id = ?", yr.LotteryRoundID).Updates(map[string]interface{}{
		"status": "resulted", "result_top3": roundResult.Top3,
		"result_top2": roundResult.Top2, "result_bottom2": roundResult.Bottom2,
		"resulted_at": &now,
	})

	// Settlement — เทียบ bets + จ่ายเงิน
	// AIDEV-NOTE: YeekeeRound เก็บ agent_id (ไม่ใช่ node_id); lookup root node ของ agent นั้น
	var rootNodeID int64
	h.DB.Raw("SELECT id FROM agent_nodes WHERE agent_id = ? AND parent_id IS NULL LIMIT 1", yr.AgentID).Scan(&rootNodeID)
	if rootNodeID == 0 {
		rootNodeID = 1 // fallback
	}
	settleYeekeeBets(h.DB, yr.LotteryRoundID, rootNodeID, roundResult)

	// บันทึก action log
	logManualSettle(h.DB, c, yr, resultNumber, "settled")

	log.Printf("✅ Admin manual settle yeekee round %d: result=%s (top3=%s, top2=%s, bot2=%s)",
		yr.RoundNo, resultNumber, roundResult.Top3, roundResult.Top2, roundResult.Bottom2)

	ok(c, gin.H{
		"message":       "ออกผลสำเร็จ",
		"result_number": resultNumber,
		"top3":          roundResult.Top3,
		"top2":          roundResult.Top2,
		"bottom2":       roundResult.Bottom2,
		"total_shoots":  len(shoots),
	})
}

// settleYeekeeBets เทียบ bets กับผลยี่กี + จ่ายเงินรางวัล (เหมือน member-api cron)
func settleYeekeeBets(db *gorm.DB, lotteryRoundID int64, rootNodeID int64, roundResult coreTypes.RoundResult) {
	var bets []model.Bet
	db.Where("lottery_round_id = ? AND status = ?", lotteryRoundID, "pending").
		Preload("BetType").Find(&bets)

	if len(bets) == 0 {
		log.Printf("ℹ️ [manual-settle] No pending bets for round %d", lotteryRoundID)
		return
	}

	coreBets := make([]coreTypes.Bet, 0, len(bets))
	for _, b := range bets {
		betTypeCode := ""
		if b.BetType.Code != "" {
			betTypeCode = b.BetType.Code
		}
		coreBets = append(coreBets, coreTypes.Bet{
			ID: b.ID, MemberID: b.MemberID, RoundID: b.LotteryRoundID,
			BetType: coreTypes.BetType(betTypeCode), Number: b.Number,
			Amount: b.Amount, Rate: b.Rate, Status: coreTypes.BetStatusPending,
		})
	}

	settleOutput := payout.SettleRound(payout.SettleRoundInput{
		RoundID: lotteryRoundID, Result: roundResult, Bets: coreBets,
	})

	log.Printf("💰 [manual-settle] round=%d, rootNode=%d, bets=%d, winners=%d, win=%.2f",
		lotteryRoundID, rootNodeID, len(bets), settleOutput.TotalWinners, settleOutput.TotalWinAmount)

	tx := db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			log.Printf("❌ [manual-settle] Payout panic: %v", r)
		}
	}()

	now := time.Now()
	betResultMap := make(map[int64]coreTypes.BetResult)
	for _, br := range settleOutput.BetResults {
		betResultMap[br.BetID] = br
	}
	for _, b := range bets {
		br, ok := betResultMap[b.ID]
		if !ok {
			continue
		}
		newStatus := "lost"
		var winAmount float64
		if br.IsWin {
			newStatus = "won"
			winAmount = br.WinAmount
		}
		tx.Model(&model.Bet{}).Where("id = ?", b.ID).Updates(map[string]interface{}{
			"status": newStatus, "win_amount": winAmount, "settled_at": &now,
		})
	}

	memberPayouts := payout.GroupWinnersByMember(coreBets, settleOutput.BetResults)
	for memberID, totalWin := range memberPayouts {
		if totalWin <= 0 {
			continue
		}
		var member model.Member
		if err := tx.Select("id, balance").First(&member, memberID).Error; err != nil {
			continue
		}
		balanceBefore := member.Balance
		balanceAfter := balanceBefore + totalWin
		tx.Model(&model.Member{}).Where("id = ?", memberID).
			Update("balance", gorm.Expr("balance + ?", totalWin))

		roundID := lotteryRoundID
		winTx := model.Transaction{
			MemberID:      memberID,
			Type:          "win",
			Amount:        totalWin,
			BalanceBefore: balanceBefore,
			BalanceAfter:  balanceAfter,
			ReferenceID:   &roundID,
			ReferenceType: "lottery_round",
			CreatedAt:     now,
		}
		tx.Create(&winTx)
		// agent_node_id ไม่อยู่ใน admin model → set ตรงผ่าน SQL
		tx.Exec("UPDATE transactions SET agent_node_id = ? WHERE id = ?", rootNodeID, winTx.ID)
	}

	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		log.Printf("❌ [manual-settle] Failed to commit: %v", err)
		return
	}

	log.Printf("✅ [manual-settle] Payout complete: %d winners credited", len(memberPayouts))
}

// logManualSettle บันทึก admin action log สำหรับการออกผลยี่กี manual
func logManualSettle(db *gorm.DB, c *gin.Context, yr model.YeekeeRound, resultNumber string, outcome string) {
	aid := mw.GetAdminID(c)

	details, _ := json.Marshal(map[string]interface{}{
		"round_no":      yr.RoundNo,
		"agent_id":      yr.AgentID, // YeekeeRound เก็บ agent_id (ไม่มี agent_node_id)
		"result_number": resultNumber,
		"outcome":       outcome,
	})

	db.Create(&model.AdminActionLog{
		AdminID:    aid,
		Action:     "yeekee_manual_settle",
		TargetType: "yeekee_round",
		TargetID:   yr.ID,
		Details:    string(details),
		IP:         c.ClientIP(),
		CreatedAt:  time.Now(),
	})
}
