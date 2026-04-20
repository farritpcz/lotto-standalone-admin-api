// Package handler — dashboard admin handlers.
// Split from stubs.go on 2026-04-20.
package handler

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

// =============================================================================
// Dashboard
// =============================================================================

func (h *Handler) GetDashboard(c *gin.Context) {
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope ตามสายงาน

	// ⚠️ Redis cache 60 วินาที — เฉพาะ admin (node ไม่ cache เพราะ scope ต่างกัน)
	ctx := c.Request.Context()
	cacheKey := "admin:dashboard:" + time.Now().Format("2006-01-02")

	if h.Redis != nil && !scope.IsNode {
		cached, err := h.Redis.Get(ctx, cacheKey).Result()
		if err == nil {
			c.Data(200, "application/json", []byte(cached))
			return
		}
	}

	var stats struct {
		TotalMembers  int64   `json:"total_members"`
		ActiveMembers int64   `json:"active_members"`
		TotalBets     int64   `json:"total_bets_today"`
		TotalAmount   float64 `json:"total_amount_today"`
		TotalWin      float64 `json:"total_win_today"`
		OpenRounds    int64   `json:"open_rounds"`
	}

	todayStart := time.Now().Truncate(24 * time.Hour)
	todayEnd := todayStart.Add(24 * time.Hour)

	// ⭐ Members — scope ตาม node
	memberQ := h.DB.Model(&model.Member{})
	memberQ = scope.ScopeByNodeID(memberQ, "agent_node_id")
	memberQ.Count(&stats.TotalMembers)

	memberActiveQ := h.DB.Model(&model.Member{}).Where("status = ?", "active")
	memberActiveQ = scope.ScopeByNodeID(memberActiveQ, "agent_node_id")
	memberActiveQ.Count(&stats.ActiveMembers)

	// ⭐ Bets — scope ตาม member_id
	betQ := h.DB.Model(&model.Bet{}).Where("created_at >= ? AND created_at < ?", todayStart, todayEnd)
	betQ = scope.ScopeByMemberID(betQ, "member_id")
	betQ.Count(&stats.TotalBets)

	betAmtQ := h.DB.Model(&model.Bet{}).Where("created_at >= ? AND created_at < ?", todayStart, todayEnd)
	betAmtQ = scope.ScopeByMemberID(betAmtQ, "member_id")
	betAmtQ.Select("COALESCE(SUM(amount), 0)").Scan(&stats.TotalAmount)

	betWinQ := h.DB.Model(&model.Bet{}).Where("created_at >= ? AND created_at < ? AND status = ?", todayStart, todayEnd, "won")
	betWinQ = scope.ScopeByMemberID(betWinQ, "member_id")
	betWinQ.Select("COALESCE(SUM(win_amount), 0)").Scan(&stats.TotalWin)

	h.DB.Model(&model.LotteryRound{}).Where("status = ?", "open").Count(&stats.OpenRounds)

	// Cache ใน Redis 60 วินาที (เฉพาะ admin)
	if h.Redis != nil && !scope.IsNode {
		jsonBytes, _ := json.Marshal(gin.H{"success": true, "data": stats})
		h.Redis.Set(ctx, cacheKey, string(jsonBytes), 60*time.Second)
	}

	ok(c, stats)
}

// GetDashboardV2 — dashboard ใหม่ครบทุก section (แบบเจริญดี88)
// GET /api/v1/dashboard/v2?from=2026-04-01&to=2026-04-02
//
// Presets ที่ frontend ส่งมา:
//   - วันนี้: from=today, to=today
//   - เมื่อวาน: from=yesterday, to=yesterday
//   - อาทิตย์นี้: from=2026-03-31, to=2026-04-06
//   - เดือนนี้: from=2026-04-01, to=2026-04-30
//   - ต้นเดือน: from=2026-04-01, to=2026-04-15
//   - ท้ายเดือน: from=2026-04-16, to=2026-04-30
func (h *Handler) GetDashboardV2(c *gin.Context) {
	// ─── parse from/to params ───
	now := time.Now()
	todayStart := now.Truncate(24 * time.Hour)
	todayEnd := todayStart.Add(24 * time.Hour)

	// default = เดือนนี้
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	rangeFrom := monthStart
	rangeTo := monthStart.AddDate(0, 1, 0)

	if fromStr := c.Query("from"); fromStr != "" {
		if parsed, err := time.Parse("2006-01-02", fromStr); err == nil {
			rangeFrom = parsed
		}
	}
	if toStr := c.Query("to"); toStr != "" {
		if parsed, err := time.Parse("2006-01-02", toStr); err == nil {
			rangeTo = parsed.Add(24 * time.Hour) // inclusive end date
		}
	}

	// สำหรับ % เปรียบเทียบ: ช่วงเวลาเดียวกันก่อนหน้า
	rangeDuration := rangeTo.Sub(rangeFrom)
	prevFrom := rangeFrom.Add(-rangeDuration)
	prevTo := rangeFrom

	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope ตามสายงาน

	// ─── Redis cache 30s (เฉพาะ admin — node ไม่ cache เพราะ scope ต่างกัน) ───
	ctx := c.Request.Context()
	cacheKey := "admin:dashboardV2:" + rangeFrom.Format("20060102") + ":" + rangeTo.Format("20060102")
	if h.Redis != nil && !scope.IsNode {
		if cached, err := h.Redis.Get(ctx, cacheKey).Result(); err == nil {
			c.Data(200, "application/json", []byte(cached))
			return
		}
	}

	// ⭐ helper: สร้าง WHERE clause สำหรับ member scope (ใช้ใน raw SQL)
	memberFilter := ""        // สำหรับ transactions (member_id)
	betMemberFilter := ""     // สำหรับ bets (b.member_id)
	memberModelFilter := ""   // สำหรับ members model (agent_node_id)
	depositMemberFilter := "" // สำหรับ deposit_requests (member_id)
	if scope.IsNode {
		mIDs := scope.MemberIDsForSQL()
		idStr := ""
		for i, id := range mIDs {
			if i > 0 {
				idStr += ","
			}
			idStr += strconv.FormatInt(id, 10)
		}
		memberFilter = " AND member_id IN (" + idStr + ")"
		betMemberFilter = " AND b.member_id IN (" + idStr + ")"
		memberModelFilter = " AND agent_node_id IN (" + func() string {
			s := ""
			for i, id := range scope.NodeIDs {
				if i > 0 {
					s += ","
				}
				s += strconv.FormatInt(id, 10)
			}
			return s
		}() + ")"
		depositMemberFilter = memberFilter
	}

	// ═══ 1. Summary: เดือนนี้ vs เดือนก่อน ═══
	type MonthSummary struct {
		DepositsThisMonth    float64 `json:"deposits_this_month"`
		DepositsLastMonth    float64 `json:"deposits_last_month"`
		WithdrawalsThisMonth float64 `json:"withdrawals_this_month"`
		WithdrawalsLastMonth float64 `json:"withdrawals_last_month"`
		ProfitThisMonth      float64 `json:"profit_this_month"`
		ProfitLastMonth      float64 `json:"profit_last_month"`
		NewMembersThisMonth  int64   `json:"new_members_this_month"`
		NewMembersLastMonth  int64   `json:"new_members_last_month"`
	}
	var summary MonthSummary

	// ฝาก — ⭐ scope ด้วย memberFilter
	h.DB.Raw("SELECT COALESCE(SUM(ABS(amount)),0) FROM transactions WHERE type IN ('deposit','admin_credit') AND created_at >= ? AND created_at < ?"+memberFilter, rangeFrom, rangeTo).Scan(&summary.DepositsThisMonth)
	h.DB.Raw("SELECT COALESCE(SUM(ABS(amount)),0) FROM transactions WHERE type IN ('deposit','admin_credit') AND created_at >= ? AND created_at < ?"+memberFilter, prevFrom, prevTo).Scan(&summary.DepositsLastMonth)
	// ถอน
	h.DB.Raw("SELECT COALESCE(SUM(ABS(amount)),0) FROM transactions WHERE type IN ('withdraw','admin_debit') AND created_at >= ? AND created_at < ?"+memberFilter, rangeFrom, rangeTo).Scan(&summary.WithdrawalsThisMonth)
	h.DB.Raw("SELECT COALESCE(SUM(ABS(amount)),0) FROM transactions WHERE type IN ('withdraw','admin_debit') AND created_at >= ? AND created_at < ?"+memberFilter, prevFrom, prevTo).Scan(&summary.WithdrawalsLastMonth)
	// กำไร = ยอดแทง - ยอดจ่าย
	h.DB.Raw("SELECT COALESCE(SUM(amount),0) - COALESCE(SUM(CASE WHEN status='won' THEN win_amount ELSE 0 END),0) FROM bets WHERE created_at >= ? AND created_at < ?"+memberFilter, rangeFrom, rangeTo).Scan(&summary.ProfitThisMonth)
	h.DB.Raw("SELECT COALESCE(SUM(amount),0) - COALESCE(SUM(CASE WHEN status='won' THEN win_amount ELSE 0 END),0) FROM bets WHERE created_at >= ? AND created_at < ?"+memberFilter, prevFrom, prevTo).Scan(&summary.ProfitLastMonth)
	// สมาชิกใหม่
	h.DB.Raw("SELECT COUNT(*) FROM members WHERE created_at >= ? AND created_at < ?"+memberModelFilter, rangeFrom, rangeTo).Scan(&summary.NewMembersThisMonth)
	h.DB.Raw("SELECT COUNT(*) FROM members WHERE created_at >= ? AND created_at < ?"+memberModelFilter, prevFrom, prevTo).Scan(&summary.NewMembersLastMonth)

	// ═══ 2. Chart: ฝาก/ถอน 30 วันย้อนหลัง ═══
	type ChartPoint struct {
		Date        string  `json:"date"`
		Deposits    float64 `json:"deposits"`
		Withdrawals float64 `json:"withdrawals"`
	}
	var chartData []ChartPoint
	thirtyDaysAgo := todayStart.AddDate(0, 0, -30)
	h.DB.Raw(`
		SELECT DATE(created_at) as date,
			COALESCE(SUM(CASE WHEN type IN ('deposit','admin_credit') THEN ABS(amount) ELSE 0 END),0) as deposits,
			COALESCE(SUM(CASE WHEN type IN ('withdraw','admin_debit') THEN ABS(amount) ELSE 0 END),0) as withdrawals
		FROM transactions WHERE created_at >= ?`+memberFilter+`
		GROUP BY DATE(created_at) ORDER BY date
	`, thirtyDaysAgo).Scan(&chartData)

	// ═══ 3. Top 10 สมาชิกแทงเยอะสุด ═══
	type TopBettor struct {
		MemberID int64   `json:"member_id"`
		Username string  `json:"username"`
		TotalBet float64 `json:"total_bet"`
		TotalWin float64 `json:"total_win"`
		Profit   float64 `json:"profit"`
	}
	var topBettors []TopBettor
	h.DB.Raw(`
		SELECT b.member_id, m.username, SUM(b.amount) as total_bet,
			COALESCE(SUM(CASE WHEN b.status='won' THEN b.win_amount ELSE 0 END),0) as total_win,
			SUM(b.amount) - COALESCE(SUM(CASE WHEN b.status='won' THEN b.win_amount ELSE 0 END),0) as profit
		FROM bets b LEFT JOIN members m ON m.id = b.member_id
		WHERE b.created_at >= ? AND b.created_at < ?`+betMemberFilter+`
		GROUP BY b.member_id, m.username ORDER BY total_bet DESC LIMIT 10
	`, rangeFrom, rangeTo).Scan(&topBettors)

	// ═══ 4. Top 10 สมาชิกยอดฝาก/ถอนสูงสุด ═══
	type TopDepositor struct {
		MemberID      int64   `json:"member_id"`
		Username      string  `json:"username"`
		TotalDeposit  float64 `json:"total_deposit"`
		TotalWithdraw float64 `json:"total_withdraw"`
	}
	var topDepositors []TopDepositor
	h.DB.Raw(`
		SELECT t.member_id, m.username,
			COALESCE(SUM(CASE WHEN t.type IN ('deposit','admin_credit') THEN ABS(t.amount) ELSE 0 END),0) as total_deposit,
			COALESCE(SUM(CASE WHEN t.type IN ('withdraw','admin_debit') THEN ABS(t.amount) ELSE 0 END),0) as total_withdraw
		FROM transactions t LEFT JOIN members m ON m.id = t.member_id
		WHERE t.created_at >= ? AND t.created_at < ?`+memberFilter+`
		GROUP BY t.member_id, m.username ORDER BY total_deposit DESC LIMIT 10
	`, rangeFrom, rangeTo).Scan(&topDepositors)

	// ═══ 5. ธุรกรรมล่าสุด (5 ฝาก + 5 ถอน) ═══
	type RecentTx struct {
		ID        int64   `json:"id"`
		MemberID  int64   `json:"member_id"`
		Username  string  `json:"username"`
		Amount    float64 `json:"amount"`
		Status    string  `json:"status"`
		BankCode  string  `json:"bank_code"`
		CreatedAt string  `json:"created_at"`
	}
	var recentDeposits, recentWithdrawals []RecentTx
	h.DB.Raw(`SELECT d.id, d.member_id, m.username, d.amount, d.status, '' as bank_code, d.created_at
		FROM deposit_requests d LEFT JOIN members m ON m.id = d.member_id
		WHERE 1=1` + depositMemberFilter + `
		ORDER BY d.created_at DESC LIMIT 5`).Scan(&recentDeposits)
	h.DB.Raw(`SELECT w.id, w.member_id, m.username, w.amount, w.status, w.bank_code, w.created_at
		FROM withdraw_requests w LEFT JOIN members m ON m.id = w.member_id
		WHERE 1=1` + depositMemberFilter + `
		ORDER BY w.created_at DESC LIMIT 5`).Scan(&recentWithdrawals)

	// ═══ 6. ติดตามสมาชิกรายวัน ═══
	type MemberTracking struct {
		DirectSignups   int64 `json:"direct_signups"`
		ReferralSignups int64 `json:"referral_signups"`
		DepositedToday  int64 `json:"deposited_today"`
	}
	var tracking MemberTracking
	h.DB.Raw("SELECT COUNT(*) FROM members WHERE created_at >= ? AND created_at < ? AND referred_by IS NULL"+memberModelFilter, todayStart, todayEnd).Scan(&tracking.DirectSignups)
	h.DB.Raw("SELECT COUNT(*) FROM members WHERE created_at >= ? AND created_at < ? AND referred_by IS NOT NULL"+memberModelFilter, todayStart, todayEnd).Scan(&tracking.ReferralSignups)
	h.DB.Raw("SELECT COUNT(DISTINCT member_id) FROM deposit_requests WHERE status='approved' AND created_at >= ? AND created_at < ?"+depositMemberFilter, todayStart, todayEnd).Scan(&tracking.DepositedToday)

	// ═══ 7. Credit Stats ═══
	type CreditStats struct {
		CreditAdded       float64 `json:"credit_added"`
		CreditDeducted    float64 `json:"credit_deducted"`
		DepositCount      int64   `json:"deposit_count"`
		CommissionTotal   float64 `json:"commission_total"`
		CancelledDeposit  float64 `json:"cancelled_deposits"`
		CancelledWithdraw float64 `json:"cancelled_withdrawals"`
	}
	var credits CreditStats
	h.DB.Raw("SELECT COALESCE(SUM(ABS(amount)),0) FROM transactions WHERE type='admin_credit' AND created_at >= ? AND created_at < ?"+memberFilter, rangeFrom, rangeTo).Scan(&credits.CreditAdded)
	h.DB.Raw("SELECT COALESCE(SUM(ABS(amount)),0) FROM transactions WHERE type='admin_debit' AND created_at >= ? AND created_at < ?"+memberFilter, rangeFrom, rangeTo).Scan(&credits.CreditDeducted)
	h.DB.Raw("SELECT COUNT(*) FROM deposit_requests WHERE status='approved' AND created_at >= ? AND created_at < ?"+depositMemberFilter, rangeFrom, rangeTo).Scan(&credits.DepositCount)
	h.DB.Raw("SELECT COALESCE(SUM(amount),0) FROM referral_commissions WHERE created_at >= ? AND created_at < ?"+memberFilter, rangeFrom, rangeTo).Scan(&credits.CommissionTotal)
	h.DB.Raw("SELECT COALESCE(SUM(amount),0) FROM deposit_requests WHERE status='cancelled' AND created_at >= ? AND created_at < ?"+depositMemberFilter, rangeFrom, rangeTo).Scan(&credits.CancelledDeposit)
	h.DB.Raw("SELECT COALESCE(SUM(amount),0) FROM withdraw_requests WHERE status='rejected' AND created_at >= ? AND created_at < ?"+depositMemberFilter, rangeFrom, rangeTo).Scan(&credits.CancelledWithdraw)

	// ═══ 8. บัญชีธนาคาร ═══
	type BankAccount struct {
		ID          int64   `json:"id"`
		BankCode    string  `json:"bank_code"`
		BankName    string  `json:"bank_name"`
		AccountNo   string  `json:"account_number"`
		AccountName string  `json:"account_name"`
		Balance     float64 `json:"balance"`
	}
	var bankAccounts []BankAccount
	h.DB.Table("agent_bank_accounts").Scan(&bankAccounts)

	// ═══ Build response ═══
	resp := gin.H{
		"summary":            summary,
		"chart_data":         chartData,
		"top_bettors":        topBettors,
		"top_depositors":     topDepositors,
		"recent_deposits":    recentDeposits,
		"recent_withdrawals": recentWithdrawals,
		"member_tracking":    tracking,
		"credit_stats":       credits,
		"bank_accounts":      bankAccounts,
	}

	// Cache 60s (เฉพาะ admin)
	if h.Redis != nil && !scope.IsNode {
		jsonBytes, _ := json.Marshal(gin.H{"success": true, "data": resp})
		h.Redis.Set(ctx, cacheKey, string(jsonBytes), 60*time.Second)
	}

	ok(c, resp)
}
