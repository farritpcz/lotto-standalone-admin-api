// Package handler — reports admin handlers.
// Split from stubs.go on 2026-04-20.
package handler

import (
	"time"

	"github.com/gin-gonic/gin"

	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

func (h *Handler) ListAllTransactions(c *gin.Context) {
	page, perPage := pageParams(c)
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope ตามสายงาน
	var txns []model.Transaction
	var total int64
	query := h.DB.Model(&model.Transaction{})
	query = scope.ScopeByMemberID(query, "member_id") // ⭐ node เห็นเฉพาะ transactions ของ members ในสาย
	if t := c.Query("type"); t != "" {
		query = query.Where("type = ?", t)
	}
	if m := c.Query("member_id"); m != "" {
		query = query.Where("member_id = ?", m)
	}
	query.Count(&total)
	query.Order("created_at DESC").Offset((page - 1) * perPage).Limit(perPage).Find(&txns)
	paginated(c, txns, total, page, perPage)
}

// =============================================================================
// Reports
// =============================================================================

func (h *Handler) GetSummaryReport(c *gin.Context) {
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope
	var result struct {
		TotalBets   int64   `json:"total_bets"`
		TotalAmount float64 `json:"total_amount"`
		TotalWin    float64 `json:"total_win"`
		Profit      float64 `json:"profit"`
	}
	dateFrom := c.DefaultQuery("from", time.Now().AddDate(0, 0, -7).Format("2006-01-02"))
	dateTo := c.DefaultQuery("to", time.Now().Format("2006-01-02"))

	q1 := h.DB.Model(&model.Bet{}).Where("DATE(created_at) BETWEEN ? AND ?", dateFrom, dateTo)
	q1 = scope.ScopeByMemberID(q1, "member_id") // ⭐
	q1.Count(&result.TotalBets)

	q2 := h.DB.Model(&model.Bet{}).Where("DATE(created_at) BETWEEN ? AND ?", dateFrom, dateTo)
	q2 = scope.ScopeByMemberID(q2, "member_id") // ⭐
	q2.Select("COALESCE(SUM(amount), 0)").Scan(&result.TotalAmount)

	q3 := h.DB.Model(&model.Bet{}).Where("DATE(created_at) BETWEEN ? AND ? AND status = ?", dateFrom, dateTo, "won")
	q3 = scope.ScopeByMemberID(q3, "member_id") // ⭐
	q3.Select("COALESCE(SUM(win_amount), 0)").Scan(&result.TotalWin)

	result.Profit = result.TotalAmount - result.TotalWin
	ok(c, result)
}

func (h *Handler) GetProfitReport(c *gin.Context) {
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope
	dateFrom := c.DefaultQuery("from", time.Now().AddDate(0, 0, -30).Format("2006-01-02"))
	dateTo := c.DefaultQuery("to", time.Now().Format("2006-01-02"))

	type DailyProfit struct {
		Date        string  `json:"date"`
		TotalBets   int64   `json:"total_bets"`
		TotalAmount float64 `json:"total_amount"`
		TotalWin    float64 `json:"total_win"`
		Profit      float64 `json:"profit"`
	}
	var daily []DailyProfit
	q := h.DB.Model(&model.Bet{}).
		Select("DATE(created_at) as date, COUNT(*) as total_bets, COALESCE(SUM(amount), 0) as total_amount, COALESCE(SUM(CASE WHEN status='won' THEN win_amount ELSE 0 END), 0) as total_win, COALESCE(SUM(amount), 0) - COALESCE(SUM(CASE WHEN status='won' THEN win_amount ELSE 0 END), 0) as profit").
		Where("DATE(created_at) BETWEEN ? AND ?", dateFrom, dateTo)
	q = scope.ScopeByMemberID(q, "member_id") // ⭐
	q.
		Group("DATE(created_at)").
		Order("date ASC").
		Scan(&daily)

	ok(c, daily)
}
