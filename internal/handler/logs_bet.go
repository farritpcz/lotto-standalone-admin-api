package handler

// =============================================================================
// GetBetLogs — timeline ของรายการเดิมพัน
// แยกจาก logs.go (ตาม pattern 1-handler-per-file)
// =============================================================================

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// GetBetLogs ดึง timeline ของรายการเดิมพัน
// GET /api/v1/bets/:id/logs
func (h *Handler) GetBetLogs(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if id == 0 {
		fail(c, 400, "invalid id")
		return
	}

	// ── 1) ดึงข้อมูล bet ─────────────────────────────────────────────
	var bet struct {
		ID           int64      `json:"id"`
		MemberID     int64      `json:"member_id"`
		Number       string     `json:"number"`
		Amount       float64    `json:"amount"`
		Rate         float64    `json:"rate"`
		Status       string     `json:"status"`
		WinAmount    float64    `json:"win_amount"`
		CancelReason string     `json:"cancel_reason"`
		SettledAt    *time.Time `json:"settled_at"`
		CancelledAt  *time.Time `json:"cancelled_at"`
		CreatedAt    time.Time  `json:"created_at"`
		BetTypeName  string     `json:"bet_type_name"`
	}
	err := h.DB.Table("bets b").
		Select("b.*, bt.name as bet_type_name").
		Joins("LEFT JOIN bet_types bt ON bt.id = b.bet_type_id").
		Where("b.id = ?", id).First(&bet).Error
	if err != nil {
		fail(c, 404, "ไม่พบรายการเดิมพัน")
		return
	}

	var memberName string
	h.DB.Table("members").Select("username").Where("id = ?", bet.MemberID).Row().Scan(&memberName)

	var logs []LogEntry

	// ── 2) สร้างบิล ──────────────────────────────────────────────────
	logs = append(logs, LogEntry{
		Timestamp:   bet.CreatedAt,
		Action:      "สร้างบิลเดิมพัน",
		Description: fmt.Sprintf("เลข %s (%s) ฿%.2f x%.2f", bet.Number, bet.BetTypeName, bet.Amount, bet.Rate),
		Actor:       memberName,
		Type:        "create",
		Amount:      bet.Amount,
	})

	// ── 3) ออกผล (ถ้า settled) ────────────────────────────────────────
	if bet.SettledAt != nil {
		entry := LogEntry{
			Timestamp: *bet.SettledAt,
			Type:      "approve",
		}
		if bet.Status == "won" || (bet.Status == "cancelled" && bet.WinAmount > 0) {
			entry.Action = "ออกผล: ชนะ"
			entry.Description = fmt.Sprintf("รางวัล ฿%.2f", bet.WinAmount)
			entry.Amount = bet.WinAmount
		} else {
			entry.Action = "ออกผล: แพ้"
			entry.Description = "ไม่ถูกรางวัล"
		}
		logs = append(logs, entry)
	}

	// ── 4) Activity logs — admin cancel/void ──────────────────────────
	var activities []activityRow
	pathPattern := fmt.Sprintf("%%/bets/%d/%%", id)
	h.DB.Table("activity_logs").
		Where("path LIKE ? AND status_code = 200", pathPattern).
		Order("created_at ASC").
		Find(&activities)

	adminNames := h.resolveAdminNames(extractAdminIDs(activities))

	for _, a := range activities {
		entry := LogEntry{
			Timestamp: a.CreatedAt,
			Actor:     adminNames[a.AdminID],
		}
		switch {
		case contains(a.Path, "/cancel"):
			entry.Action = "ยกเลิก"
			entry.Type = "cancel"
			if bet.CancelReason != "" {
				entry.Description = bet.CancelReason
			}
		default:
			continue
		}
		logs = append(logs, entry)
	}

	// ── 5) Transactions — ผลกระทบทางการเงิน ──────────────────────────
	var txns []txnRow
	h.DB.Table("transactions").
		Where("reference_id = ? AND reference_type LIKE 'bet%'", id).
		Order("created_at ASC").
		Find(&txns)

	for _, t := range txns {
		entry := LogEntry{
			Timestamp: t.CreatedAt,
			Amount:    t.Amount,
		}
		switch t.ReferenceType {
		case "bet_cancel":
			entry.Action = "คืนเครดิต"
			entry.Type = "refund"
			entry.Description = fmt.Sprintf("คืน ฿%.2f (ก่อน ฿%.2f → หลัง ฿%.2f)", t.Amount, t.BalanceBefore, t.BalanceAfter)
		case "bet_void_debit":
			entry.Action = "หักรางวัลคืน"
			entry.Type = "cancel"
			entry.Description = fmt.Sprintf("หัก ฿%.2f (ก่อน ฿%.2f → หลัง ฿%.2f)", -t.Amount, t.BalanceBefore, t.BalanceAfter)
		case "bet_cancel_no_refund":
			entry.Action = "ยกเลิก (ไม่คืนเครดิต)"
			entry.Type = "cancel"
			entry.Description = "ไม่คืนเครดิต"
		default:
			continue
		}
		logs = append(logs, entry)
	}

	sort.Slice(logs, func(i, j int) bool {
		return logs[i].Timestamp.Before(logs[j].Timestamp)
	})

	ok(c, logs)
}
