// Package handler — yeekee admin handlers (monitoring + agent config).
// Manual settle moved to yeekee_settle_handler.go (2026-04-21).
package handler

import (
	"log"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

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
