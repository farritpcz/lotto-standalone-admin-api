// Package handler — yeekee admin handlers.
// Split from stubs.go on 2026-04-20.
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
// ⭐ Yeekee Monitoring — ดูรอบ + สถิติยี่กี real-time
// =============================================================================

// ListYeekeeRounds แสดงรายการรอบยี่กี (paginated + filter)
// GET /api/v1/yeekee/rounds?status=shooting&date=2026-04-02&page=1&per_page=20
func (h *Handler) ListYeekeeRounds(c *gin.Context) {
	page, perPage := pageParams(c)
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope
	var rounds []model.YeekeeRound
	var total int64

	query := h.DB.Model(&model.YeekeeRound{})

	// ⭐ scope: ยี่กีเว็บใครเว็บมัน
	if scope.IsNode {
		query = query.Where("agent_node_id = ?", scope.NodeID)
	} else if aidStr := c.Query("agent_node_id"); aidStr != "" {
		aid, _ := strconv.ParseInt(aidStr, 10, 64)
		if aid > 0 {
			query = query.Where("agent_node_id = ?", aid)
		}
	}

	// Filter by status
	if s := c.Query("status"); s != "" {
		query = query.Where("status = ?", s)
	}

	// Filter by date (ใช้ start_time ของรอบ)
	if d := c.Query("date"); d != "" {
		query = query.Where("DATE(start_time) = ?", d)
	} else {
		today := time.Now().Format("2006-01-02")
		query = query.Where("DATE(start_time) = ?", today)
	}

	query.Count(&total)
	query.
		Preload("LotteryRound").
		Order("start_time ASC").
		Offset((page - 1) * perPage).
		Limit(perPage).
		Find(&rounds)

	paginated(c, rounds, total, page, perPage)
}

// GetYeekeeRoundDetail ดูรอบยี่กีรอบเดียว + shoots + bet summary
// GET /api/v1/yeekee/rounds/:id
func (h *Handler) GetYeekeeRoundDetail(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	var round model.YeekeeRound
	if err := h.DB.Preload("LotteryRound").First(&round, id).Error; err != nil {
		fail(c, 404, "yeekee round not found")
		return
	}

	// ดึง shoots (ไม่ paginate — แสดงทั้งหมดเพราะแต่ละรอบไม่มาก)
	var shoots []model.YeekeeShoot
	h.DB.Where("yeekee_round_id = ?", id).Preload("Member").Order("shot_at ASC").Find(&shoots)

	// Bet summary — จำนวน bets + ยอดแทง + ยอดจ่าย ของรอบนี้
	var betSummary struct {
		TotalBets   int64   `json:"total_bets"`
		TotalAmount float64 `json:"total_amount"`
		TotalPayout float64 `json:"total_payout"`
		WinnerCount int64   `json:"winner_count"`
	}
	h.DB.Model(&model.Bet{}).
		Where("lottery_round_id = ?", round.LotteryRoundID).
		Select("COUNT(*) as total_bets, COALESCE(SUM(amount), 0) as total_amount, COALESCE(SUM(win_amount), 0) as total_payout").
		Scan(&betSummary)
	h.DB.Model(&model.Bet{}).
		Where("lottery_round_id = ? AND status = ?", round.LotteryRoundID, "won").
		Count(&betSummary.WinnerCount)

	// ⭐ ดึง bets ทั้งหมดของรอบนี้ (พร้อม member + bet_type)
	var bets []model.Bet
	h.DB.Where("lottery_round_id = ?", round.LotteryRoundID).
		Preload("Member").Preload("BetType").
		Order("created_at DESC").
		Find(&bets)

	ok(c, gin.H{
		"round":       round,
		"shoots":      shoots,
		"bets":        bets,
		"bet_summary": betSummary,
	})
}

// ListYeekeeShoots ดูเลขยิงในรอบ (paginated)
// GET /api/v1/yeekee/rounds/:id/shoots?page=1&per_page=50
func (h *Handler) ListYeekeeShoots(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	page, perPage := pageParams(c)

	var shoots []model.YeekeeShoot
	var total int64

	query := h.DB.Model(&model.YeekeeShoot{}).Where("yeekee_round_id = ?", id)
	query.Count(&total)
	query.
		Preload("Member").
		Order("shot_at ASC").
		Offset((page - 1) * perPage).
		Limit(perPage).
		Find(&shoots)

	paginated(c, shoots, total, page, perPage)
}

// GetYeekeeStats สถิติยี่กีวันนี้
// GET /api/v1/yeekee/stats
func (h *Handler) GetYeekeeStats(c *gin.Context) {
	today := time.Now().Format("2006-01-02")

	var stats struct {
		TotalRounds    int64   `json:"total_rounds"`
		WaitingCount   int64   `json:"waiting_count"`
		ShootingCount  int64   `json:"shooting_count"`
		ResultedCount  int64   `json:"resulted_count"`
		MissedCount    int64   `json:"missed_count"`
		TotalShoots    int64   `json:"total_shoots"`
		TotalBets      int64   `json:"total_bets"`
		TotalBetAmount float64 `json:"total_bet_amount"`
		TotalPayout    float64 `json:"total_payout"`
		Profit         float64 `json:"profit"`
	}

	// ⭐ scope: ยี่กีเว็บใครเว็บมัน
	scope := mw.GetNodeScope(c, h.DB)
	baseQuery := h.DB.Model(&model.YeekeeRound{}).Where("DATE(start_time) = ?", today)
	if scope.IsNode {
		baseQuery = baseQuery.Where("agent_node_id = ?", scope.NodeID)
	} else if aidStr := c.Query("agent_node_id"); aidStr != "" {
		aid, _ := strconv.ParseInt(aidStr, 10, 64)
		if aid > 0 {
			baseQuery = baseQuery.Where("agent_node_id = ?", aid)
		}
	}

	// นับรอบตาม status
	baseQuery.Session(&gorm.Session{}).Count(&stats.TotalRounds)
	baseQuery.Session(&gorm.Session{}).Where("status = ?", "waiting").Count(&stats.WaitingCount)
	baseQuery.Session(&gorm.Session{}).Where("status = ?", "shooting").Count(&stats.ShootingCount)
	baseQuery.Session(&gorm.Session{}).Where("status = ?", "resulted").Count(&stats.ResultedCount)
	baseQuery.Session(&gorm.Session{}).Where("status = ?", "missed").Count(&stats.MissedCount)

	// นับ shoots วันนี้ (filter agent ผ่าน yeekee_rounds join)
	shootQuery := h.DB.Model(&model.YeekeeShoot{}).
		Joins("JOIN yeekee_rounds ON yeekee_shoots.yeekee_round_id = yeekee_rounds.id").
		Where("DATE(yeekee_rounds.start_time) = ?", today)
	if aidStr := c.Query("agent_node_id"); aidStr != "" {
		aid, _ := strconv.ParseInt(aidStr, 10, 64)
		if aid > 0 {
			shootQuery = shootQuery.Where("yeekee_rounds.agent_node_id = ?", aid)
		}
	}
	shootQuery.Count(&stats.TotalShoots)

	// สถิติ bets — ดึงเฉพาะ bets ของรอบยี่กีวันนี้
	var yeekeeRoundIDs []int64
	pluckQuery := h.DB.Model(&model.YeekeeRound{}).Where("DATE(start_time) = ?", today)
	if aidStr := c.Query("agent_node_id"); aidStr != "" {
		aid, _ := strconv.ParseInt(aidStr, 10, 64)
		if aid > 0 {
			pluckQuery = pluckQuery.Where("agent_node_id = ?", aid)
		}
	}
	pluckQuery.Pluck("lottery_round_id", &yeekeeRoundIDs)

	if len(yeekeeRoundIDs) > 0 {
		h.DB.Model(&model.Bet{}).Where("lottery_round_id IN ?", yeekeeRoundIDs).Count(&stats.TotalBets)
		h.DB.Model(&model.Bet{}).Where("lottery_round_id IN ?", yeekeeRoundIDs).
			Select("COALESCE(SUM(amount), 0)").Scan(&stats.TotalBetAmount)
		h.DB.Model(&model.Bet{}).Where("lottery_round_id IN ? AND status = ?", yeekeeRoundIDs, "won").
			Select("COALESCE(SUM(win_amount), 0)").Scan(&stats.TotalPayout)
		stats.Profit = stats.TotalBetAmount - stats.TotalPayout
	}

	ok(c, stats)
}

// =============================================================================
// Yeekee Agent Config — เปิด/ปิดยี่กี per agent
// =============================================================================

// GetYeekeeAgentConfig ดูว่า agent ไหนเปิดยี่กีอยู่
// GET /api/v1/yeekee/config
//
// Response: array ของ { agent_node_id, agent_name, lottery_type_id, enabled }
func (h *Handler) GetYeekeeAgentConfig(c *gin.Context) {
	// ดึง YEEKEE lottery type ID
	var yeekeeTypeID int64
	h.DB.Table("lottery_types").Select("id").Where("code = ?", "YEEKEE").Scan(&yeekeeTypeID)
	if yeekeeTypeID == 0 {
		fail(c, 404, "YEEKEE lottery type not found")
		return
	}

	// ดึง root nodes ทั้งหมด + สถานะยี่กี (LEFT JOIN agent_lottery_config)
	// ⭐ ย้ายจาก agents → agent_nodes (root node = role='admin', parent_id IS NULL)
	type AgentConfig struct {
		AgentNodeID int64  `json:"agent_node_id"`
		AgentName   string `json:"agent_name"`
		AgentCode   string `json:"agent_code"`
		Enabled     bool   `json:"enabled"`
	}

	var configs []AgentConfig
	h.DB.Raw(`
		SELECT
			n.id AS agent_node_id, n.name AS agent_name, n.code AS agent_code,
			COALESCE(alc.enabled, 0) AS enabled
		FROM agent_nodes n
		LEFT JOIN agent_lottery_config alc
			ON alc.agent_node_id = n.id AND alc.lottery_type_id = ?
		WHERE n.role = 'admin' AND n.parent_id IS NULL AND n.status = 'active'
		ORDER BY n.id
	`, yeekeeTypeID).Scan(&configs)

	ok(c, gin.H{
		"lottery_type_id": yeekeeTypeID,
		"agents":          configs,
	})
}

// SetYeekeeAgentConfig เปิด/ปิดยี่กี สำหรับ root node
// POST /api/v1/yeekee/config
// Body: { "agent_node_id": 1, "enabled": true }
//
// ⭐ ใช้ UPSERT — ถ้ายังไม่มี row ใน agent_lottery_config → สร้าง, ถ้ามีแล้ว → update
func (h *Handler) SetYeekeeAgentConfig(c *gin.Context) {
	var req struct {
		AgentNodeID int64 `json:"agent_node_id" binding:"required"`
		Enabled     bool  `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// ดึง YEEKEE lottery type ID
	var yeekeeTypeID int64
	h.DB.Table("lottery_types").Select("id").Where("code = ?", "YEEKEE").Scan(&yeekeeTypeID)
	if yeekeeTypeID == 0 {
		fail(c, 404, "YEEKEE lottery type not found")
		return
	}

	// เช็คว่า root node มีจริง
	var nodeExists int64
	h.DB.Table("agent_nodes").Where("id = ? AND role = 'admin' AND status = ?", req.AgentNodeID, "active").Count(&nodeExists)
	if nodeExists == 0 {
		fail(c, 404, "agent node not found")
		return
	}

	// UPSERT: INSERT ... ON DUPLICATE KEY UPDATE
	enabledInt := 0
	if req.Enabled {
		enabledInt = 1
	}

	h.DB.Exec(`
		INSERT INTO agent_lottery_config (agent_node_id, lottery_type_id, enabled, created_at)
		VALUES (?, ?, ?, NOW())
		ON DUPLICATE KEY UPDATE enabled = ?
	`, req.AgentNodeID, yeekeeTypeID, enabledInt, enabledInt)

	action := "ปิด"
	if req.Enabled {
		action = "เปิด"
	}
	log.Printf("🎯 Yeekee %s for root node %d", action, req.AgentNodeID)

	ok(c, gin.H{
		"agent_node_id":   req.AgentNodeID,
		"lottery_type_id": yeekeeTypeID,
		"enabled":         req.Enabled,
		"message":         action + "ยี่กีสำเร็จ",
	})
}

// =============================================================================
// ManualSettleYeekeeRound — แอดมินกดออกผลยี่กี manual (รอบที่ missed)
// POST /api/v1/yeekee/rounds/:id/settle
//
// ใช้เมื่อรอบยี่กีถูก mark เป็น "missed" (server ปิดระหว่างรอบ)
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
