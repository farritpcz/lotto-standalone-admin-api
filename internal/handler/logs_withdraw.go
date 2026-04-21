package handler

// =============================================================================
// GetWithdrawLogs — timeline ของคำขอถอนเงิน
// แยกจาก logs.go (ตาม pattern 1-handler-per-file)
// =============================================================================

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// GetWithdrawLogs ดึง timeline ของ withdraw request
// GET /api/v1/withdrawals/:id/logs
func (h *Handler) GetWithdrawLogs(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if id == 0 {
		fail(c, 400, "invalid id")
		return
	}

	// ── 1) ดึงข้อมูล request ────────────────────────────────────────
	var req struct {
		ID           int64      `json:"id"`
		MemberID     int64      `json:"member_id"`
		Amount       float64    `json:"amount"`
		Status       string     `json:"status"`
		RejectReason string     `json:"reject_reason"`
		TransferMode string     `json:"transfer_mode"`
		CreatedAt    time.Time  `json:"created_at"`
		ApprovedAt   *time.Time `json:"approved_at"`
		ApprovedBy   *int64     `json:"approved_by"`
	}
	if err := h.DB.Table("withdraw_requests").Where("id = ?", id).First(&req).Error; err != nil {
		fail(c, 404, "ไม่พบรายการถอนเงิน")
		return
	}

	var memberName string
	h.DB.Table("members").Select("username").Where("id = ?", req.MemberID).Row().Scan(&memberName)

	var logs []LogEntry

	// ── 2) สร้างคำขอ ────────────────────────────────────────────────
	logs = append(logs, LogEntry{
		Timestamp:   req.CreatedAt,
		Action:      "สร้างคำขอถอนเงิน",
		Description: fmt.Sprintf("฿%.2f (หักเครดิตแล้ว)", req.Amount),
		Actor:       memberName,
		Type:        "create",
	})

	// ── 3) Activity logs ────────────────────────────────────────────
	var activities []activityRow
	pathPattern := fmt.Sprintf("%%/withdrawals/%d/%%", id)
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
		case contains(a.Path, "/approve"):
			entry.Action = "อนุมัติ"
			entry.Type = "approve"
			modeLabel := "โอนมือ"
			if req.TransferMode == "auto" {
				modeLabel = "โอนอัตโนมัติ"
			}
			entry.Description = fmt.Sprintf("โอนเงิน ฿%.2f (%s)", req.Amount, modeLabel)
		case contains(a.Path, "/reject"):
			entry.Action = "ปฏิเสธ"
			entry.Type = "reject"
			if req.RejectReason != "" {
				entry.Description = req.RejectReason
			}
		default:
			continue
		}
		logs = append(logs, entry)
	}

	// ── 4) Transactions ─────────────────────────────────────────────
	var txns []txnRow
	h.DB.Table("transactions").
		Where("reference_id = ? AND reference_type LIKE 'withdraw%'", id).
		Order("created_at ASC").
		Find(&txns)

	for _, t := range txns {
		entry := LogEntry{
			Timestamp: t.CreatedAt,
			Amount:    t.Amount,
		}
		switch t.ReferenceType {
		case "withdraw_reject":
			entry.Action = "คืนเครดิต"
			entry.Type = "refund"
			entry.Description = fmt.Sprintf("คืน ฿%.2f (ก่อน ฿%.2f → หลัง ฿%.2f)", t.Amount, t.BalanceBefore, t.BalanceAfter)
			// อัพเดท reject entry ด้วย
			for i := range logs {
				if logs[i].Type == "reject" {
					logs[i].Description = fmt.Sprintf("ปฏิเสธ + คืนเงิน ฿%.2f", t.Amount)
					break
				}
			}
		case "withdraw_reject_no_refund":
			entry.Action = "ปฏิเสธ (ไม่คืนเครดิต)"
			entry.Type = "reject"
			entry.Description = "ไม่คืนเครดิต (ทุจริต)"
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
