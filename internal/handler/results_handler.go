// Package handler — results admin handlers.
// Split from stubs.go on 2026-04-20.
package handler

import (
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/farritpcz/lotto-core/payout"
	coreTypes "github.com/farritpcz/lotto-core/types"

	"github.com/farritpcz/lotto-standalone-admin-api/internal/job"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

// =============================================================================
// Results — ⭐ กรอกผลรางวัล (trigger payout ผ่าน lotto-core)
// =============================================================================

// PreviewResult ดูตัวอย่างก่อนกรอกผล — ใครจะถูก จ่ายเท่าไร
// POST /api/v1/results/:roundId/preview
// Body: { "top3": "999", "top2": "99", "bottom2": "56" }
// ⭐ ไม่บันทึกอะไร แค่คำนวณแล้วส่งกลับ
func (h *Handler) PreviewResult(c *gin.Context) {
	roundID, _ := strconv.ParseInt(c.Param("roundId"), 10, 64)
	var req struct {
		Top3    string `json:"top3" binding:"required"`
		Top2    string `json:"top2" binding:"required"`
		Bottom2 string `json:"bottom2" binding:"required"`
		Front3  string `json:"front3"`  // 3 ตัวหน้า (optional)
		Bottom3 string `json:"bottom3"` // 3 ตัวล่าง (optional, comma-separated)
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// ดึง round
	var round model.LotteryRound
	if err := h.DB.First(&round, roundID).Error; err != nil {
		fail(c, 404, "round not found")
		return
	}

	// ดึง bets ทั้งหมดของรอบ (pending หรือ won/lost ถ้าออกผลไปแล้ว)
	var bets []model.Bet
	if round.Status == "resulted" {
		// รอบ resulted แล้ว → ดึงทุก status
		h.DB.Where("lottery_round_id = ?", roundID).
			Preload("BetType").Preload("Member").Find(&bets)
	} else {
		// รอบยังไม่ resulted → ดึงเฉพาะ pending
		h.DB.Where("lottery_round_id = ? AND status = ?", roundID, "pending").
			Preload("BetType").Preload("Member").Find(&bets)
	}

	// คำนวณว่าใครถูก
	type WinnerInfo struct {
		BetID    int64   `json:"bet_id"`
		MemberID int64   `json:"member_id"`
		Username string  `json:"username"`
		Number   string  `json:"number"`
		BetType  string  `json:"bet_type"`
		Amount   float64 `json:"amount"`
		Rate     float64 `json:"rate"`
		Payout   float64 `json:"payout"`
	}

	var winners []WinnerInfo
	totalPayout := 0.0
	totalBets := len(bets)
	totalAmount := 0.0

	for _, bet := range bets {
		totalAmount += bet.Amount
		betTypeCode := ""
		betTypeName := ""
		if bet.BetType != nil {
			betTypeCode = bet.BetType.Code
			betTypeName = bet.BetType.Name
		}

		isWin := false
		switch betTypeCode {
		case "3TOP":
			isWin = bet.Number == req.Top3
		case "3TOD":
			// โต๊ด: เลขตรงกันทุกตัว (ไม่สนลำดับ)
			isWin = sortString(bet.Number) == sortString(req.Top3)
		case "2TOP":
			isWin = bet.Number == req.Top2
		case "2BOTTOM":
			isWin = bet.Number == req.Bottom2
		case "3FRONT":
			isWin = req.Front3 != "" && bet.Number == req.Front3
		case "3BOTTOM":
			if req.Bottom3 != "" {
				for _, b3 := range strings.Split(req.Bottom3, ",") {
					if strings.TrimSpace(b3) == bet.Number {
						isWin = true
						break
					}
				}
			}
		case "4TOP":
			isWin = req.Front3 != "" && len(req.Front3) >= 1 && len(req.Top3) >= 3 && bet.Number == (req.Front3[len(req.Front3)-1:] + req.Top3)[:4]
		case "4TOD":
			if req.Front3 != "" && len(req.Top3) >= 3 {
				full4 := (req.Front3[len(req.Front3)-1:] + req.Top3)[:4]
				isWin = sortString(bet.Number) == sortString(full4)
			}
		case "RUN_TOP":
			isWin = containsDigit(req.Top3, bet.Number) || containsDigit(req.Top2, bet.Number)
		case "RUN_BOT":
			isWin = containsDigit(req.Bottom2, bet.Number)
		}

		if isWin {
			payout := bet.Amount * bet.Rate
			username := ""
			if bet.Member != nil {
				username = bet.Member.Username
			}
			winners = append(winners, WinnerInfo{
				BetID: bet.ID, MemberID: bet.MemberID, Username: username,
				Number: bet.Number, BetType: betTypeName,
				Amount: bet.Amount, Rate: bet.Rate, Payout: payout,
			})
			totalPayout += payout
		}
	}

	ok(c, gin.H{
		"round_id":     roundID,
		"round_number": round.RoundNumber,
		"result":       gin.H{"top3": req.Top3, "top2": req.Top2, "bottom2": req.Bottom2, "front3": req.Front3, "bottom3": req.Bottom3},
		"total_bets":   totalBets,
		"total_amount": totalAmount,
		"winners":      winners,
		"winner_count": len(winners),
		"total_payout": totalPayout,
		"profit":       totalAmount - totalPayout,
	})
}

// helper: sort string chars
func sortString(s string) string {
	r := []byte(s)
	for i := 0; i < len(r); i++ {
		for j := i + 1; j < len(r); j++ {
			if r[i] > r[j] {
				r[i], r[j] = r[j], r[i]
			}
		}
	}
	return string(r)
}

// helper: check if number contains digit
func containsDigit(result string, digit string) bool {
	for _, c := range result {
		if string(c) == digit {
			return true
		}
	}
	return false
}

// SubmitResult กรอกผลรางวัล
// POST /api/v1/results/:roundId
// Body: { "top3": "847", "top2": "47", "bottom2": "56" }
//
// ⭐ Flow หลังกรอกผล:
//  1. บันทึกผลลง lottery_rounds
//  2. ดึง bets ทั้งหมดของรอบ (status = pending)
//  3. lotto-core payout.SettleRound() → เทียบผลทุก bet
//  4. อัพเดท bets: won/lost + win_amount
//  5. จ่ายเงินคนชนะ: members.balance += win_amount
//  6. สร้าง transactions สำหรับคนชนะ
//
// SubmitResult กรอกผลรางวัล + settle bets ทั้งรอบ
//
// Flow:
//  1. รับผลจาก admin (top3, top2, bottom2, front3?, bottom3?)
//  2. บันทึกผลลง lottery_rounds
//  3. ดึง bets ทั้งหมดของรอบ (status=pending) + preload BetType
//  4. แปลง DB bets → lotto-core types.Bet
//  5. เรียก lotto-core payout.SettleRound() → ได้ผลทุก bet (won/lost + winAmount)
//  6. อัพเดท bets ใน DB + จ่ายเงินคนชนะ (atomic per member)
//  7. คำนวณ commission ให้ referrers (goroutine)
//
// ⭐ ใช้ lotto-core payout เต็ม — รองรับ 3TOP, 3TOD, 3FRONT, 3BOTTOM,
//
//	4TOP, 4TOD, 2TOP, 2BOTTOM, RUN_TOP, RUN_BOT
func (h *Handler) SubmitResult(c *gin.Context) {
	roundID, _ := strconv.ParseInt(c.Param("roundId"), 10, 64)

	// ─── Step 1: รับผลจาก admin ────────────────────────────────
	// top3, top2, bottom2 บังคับกรอก
	// front3, bottom3 optional (ใช้กับหวยไทยที่มี 3 ตัวหน้า / 3 ตัวล่าง)
	var req struct {
		Top3    string `json:"top3" binding:"required"`    // 3 ตัวบน เช่น "847"
		Top2    string `json:"top2" binding:"required"`    // 2 ตัวบน เช่น "47"
		Bottom2 string `json:"bottom2" binding:"required"` // 2 ตัวล่าง เช่น "56"
		Front3  string `json:"front3"`                     // 3 ตัวหน้า เช่น "491" (optional)
		Bottom3 string `json:"bottom3"`                    // 3 ตัวล่าง เช่น "123,456" (optional, comma-separated)
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// ─── Step 2: ดึง round → เช็คว่ายังไม่มีผล ────────────────
	var round model.LotteryRound
	if err := h.DB.First(&round, roundID).Error; err != nil {
		fail(c, 404, "round not found")
		return
	}
	if round.Status == "resulted" {
		fail(c, 400, "round already has result")
		return
	}

	// ─── Step 3: บันทึกผลลง DB ────────────────────────────────
	now := time.Now()
	updates := map[string]interface{}{
		"result_top3":    req.Top3,
		"result_top2":    req.Top2,
		"result_bottom2": req.Bottom2,
		"status":         "resulted",
		"resulted_at":    &now,
	}
	// เพิ่ม front3/bottom3 ถ้ามี
	if req.Front3 != "" {
		updates["result_front3"] = req.Front3
	}
	if req.Bottom3 != "" {
		updates["result_bottom3"] = req.Bottom3
	}
	h.DB.Model(&round).Updates(updates)

	// ─── Step 4: ดึง bets ทั้งหมดของรอบ (pending) ──────────────
	var dbBets []model.Bet
	h.DB.Where("lottery_round_id = ? AND status = ?", roundID, "pending").
		Preload("BetType").Find(&dbBets)

	if len(dbBets) == 0 {
		ok(c, gin.H{
			"round_id":   roundID,
			"result":     gin.H{"top3": req.Top3, "top2": req.Top2, "bottom2": req.Bottom2, "front3": req.Front3, "bottom3": req.Bottom3},
			"total_bets": 0, "settled": 0, "total_win": 0,
		})
		return
	}

	// ─── Step 5: แปลง DB bets → lotto-core types.Bet ──────────
	// lotto-core ใช้ struct types.Bet สำหรับ matching (ไม่ใช่ DB model)
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
			BetType:  coreTypes.BetType(betTypeCode), // แปลง string → coreTypes.BetType
			Number:   b.Number,
			Amount:   b.Amount,
			Rate:     b.Rate,
			Status:   coreTypes.BetStatusPending, // pending → จะถูก settle
		})
	}

	// ─── Step 6: เรียก lotto-core payout.SettleRound() ─────────
	// สร้าง RoundResult จากผลที่ admin กรอก
	roundResult := coreTypes.RoundResult{
		Top3:    req.Top3,
		Top2:    req.Top2,
		Bottom2: req.Bottom2,
		Front3:  req.Front3,
		Bottom3: req.Bottom3,
	}
	// SettleRound จะ Match ทุก bet → คำนวณ won/lost + winAmount
	settleOutput := payout.SettleRound(payout.SettleRoundInput{
		Bets:   coreBets,
		Result: roundResult,
	})

	// ─── Step 7: อัพเดท bets + จ่ายเงิน ───────────────────────
	// สร้าง map betID → BetResult เพื่อ lookup เร็ว
	resultMap := make(map[int64]coreTypes.BetResult, len(settleOutput.BetResults))
	for _, r := range settleOutput.BetResults {
		resultMap[r.BetID] = r
	}

	// อัพเดททีละ bet
	for _, bet := range dbBets {
		r, exists := resultMap[bet.ID]
		if !exists {
			continue // bet นี้ถูก skip (อาจเป็น settled แล้ว)
		}

		// อัพเดท status + win_amount
		h.DB.Model(&bet).Updates(map[string]interface{}{
			"status":     string(r.Status), // "won" หรือ "lost"
			"win_amount": r.WinAmount,
			"settled_at": &now,
		})
	}

	// จ่ายเงินคนชนะ — group by member เพื่อ update ทีเดียวต่อคน
	// ใช้ lotto-core GroupWinnersByMember() → map[memberID]totalWin
	winByMember := payout.GroupWinnersByMember(coreBets, settleOutput.BetResults)
	for memberID, totalMemberWin := range winByMember {
		// ใช้ SQL expression เพื่อ atomic increment (ไม่มี race condition)
		h.DB.Model(&model.Member{}).Where("id = ?", memberID).
			Update("balance", gorm.Expr("balance + ?", totalMemberWin))

		// บันทึก transaction สำหรับเงินรางวัล
		h.DB.Create(&model.Transaction{
			MemberID:      memberID,
			Type:          "win",
			Amount:        totalMemberWin,
			ReferenceID:   &roundID,
			ReferenceType: "lottery_round",
			Note:          "เงินรางวัลรอบ " + round.RoundNumber,
			CreatedAt:     now,
		})
	}

	// ─── Step 8: คำนวณ commission ให้ referrers ─────────────────
	// Run ใน goroutine แยก → ไม่ block response
	go job.CalculateCommissions(h.DB, roundID, 1 /* agentID = 1 สำหรับ standalone */)

	// ─── Step 9: คำนวณกำไรสายงาน (Downline Profit Sharing) ────
	// คำนวณส่วนแบ่งกำไร/ขาดทุนให้ทุก node ในสายงาน
	// ดู: job/commission_job.go → CalculateDownlineProfits()
	go job.CalculateDownlineProfits(h.DB, roundID, 1)

	// ─── Response ──────────────────────────────────────────────
	ok(c, gin.H{
		"round_id":      roundID,
		"result":        gin.H{"top3": req.Top3, "top2": req.Top2, "bottom2": req.Bottom2, "front3": req.Front3, "bottom3": req.Bottom3},
		"total_bets":    len(dbBets),
		"settled":       len(settleOutput.BetResults),
		"total_winners": settleOutput.TotalWinners,
		"total_win":     settleOutput.TotalWinAmount,
		"total_profit":  settleOutput.Profit,
	})
}

// strPtr helper — สร้าง *string จาก string
func (h *Handler) ListResults(c *gin.Context) {
	page, perPage := pageParams(c)
	var rounds []model.LotteryRound
	var total int64
	query := h.DB.Model(&model.LotteryRound{}).Where("status = ?", "resulted").Preload("LotteryType")
	if lt := c.Query("lottery_type_id"); lt != "" {
		query = query.Where("lottery_type_id = ?", lt)
	}
	query.Count(&total)
	query.Order("resulted_at DESC").Offset((page - 1) * perPage).Limit(perPage).Find(&rounds)
	paginated(c, rounds, total, page, perPage)
}
