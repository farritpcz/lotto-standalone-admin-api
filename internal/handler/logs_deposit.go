package handler

// =============================================================================
// GetDepositLogs — timeline ของคำขอฝากเงิน
// แยกจาก logs.go (ตาม pattern 1-handler-per-file)
// =============================================================================

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// GetDepositLogs ดึง timeline ของ deposit request
// GET /api/v1/deposits/:id/logs
func (h *Handler) GetDepositLogs(c *gin.Context) {
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
		CreatedAt    time.Time  `json:"created_at"`
		ApprovedAt   *time.Time `json:"approved_at"`
		ApprovedBy   *int64     `json:"approved_by"`
	}
	if err := h.DB.Table("deposit_requests").Where("id = ?", id).First(&req).Error; err != nil {
		fail(c, 404, "ไม่พบรายการฝากเงิน")
		return
	}

	// ดึงชื่อสมาชิก
	var memberName string
	h.DB.Table("members").Select("username").Where("id = ?", req.MemberID).Row().Scan(&memberName)

	var logs []LogEntry

	// ── 2) สร้างคำขอ ────────────────────────────────────────────────
	logs = append(logs, LogEntry{
		Timestamp:   req.CreatedAt,
		Action:      "สร้างคำขอฝากเงิน",
		Description: fmt.Sprintf("฿%.2f", req.Amount),
		Actor:       memberName,
		Type:        "create",
	})

	// ── 3) Activity logs — action ที่แอดมินทำ ──────────────────────
	var activities []activityRow
	pathPattern := fmt.Sprintf("%%/deposits/%d/%%", id)
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
			entry.Description = fmt.Sprintf("เพิ่มเครดิต ฿%.2f", req.Amount)
		case contains(a.Path, "/reject"):
			entry.Action = "ปฏิเสธ"
			entry.Type = "reject"
			if req.RejectReason != "" {
				entry.Description = req.RejectReason
			}
		case contains(a.Path, "/cancel"):
			entry.Action = "ยกเลิก"
			entry.Type = "cancel"
			if req.RejectReason != "" {
				entry.Description = req.RejectReason
			}
		default:
			continue
		}
		logs = append(logs, entry)
	}

	// ── 4) Transactions — ผลกระทบทางการเงิน ────────────────────────
	var txns []txnRow
	h.DB.Table("transactions").
		Where("reference_id = ? AND reference_type LIKE 'deposit%'", id).
		Or("reference_id = ? AND reference_type = 'first_deposit'", id).
		Order("created_at ASC").
		Find(&txns)

	for _, t := range txns {
		// ข้าม deposit transaction หลัก (ซ้ำกับ approve ข้างบน)
		if t.ReferenceType == "deposit_request" {
			// เติม description ให้ approve entry ที่มีอยู่แล้ว
			for i := range logs {
				if logs[i].Type == "approve" && logs[i].Amount == 0 {
					logs[i].Amount = t.Amount
					logs[i].Description = fmt.Sprintf("เพิ่มเครดิต (ก่อน ฿%.2f → หลัง ฿%.2f)", t.BalanceBefore, t.BalanceAfter)
					break
				}
			}
			continue
		}
		entry := LogEntry{
			Timestamp: t.CreatedAt,
			Amount:    t.Amount,
		}
		switch t.ReferenceType {
		case "first_deposit":
			entry.Action = "โบนัสฝากครั้งแรก"
			entry.Type = "bonus"
			entry.Description = fmt.Sprintf("฿%.2f (ก่อน ฿%.2f → หลัง ฿%.2f)", t.Amount, t.BalanceBefore, t.BalanceAfter)
		case "deposit_cancel":
			entry.Action = "หักเครดิตคืน"
			entry.Type = "cancel"
			entry.Amount = t.Amount
			entry.Description = fmt.Sprintf("หัก ฿%.2f (ก่อน ฿%.2f → หลัง ฿%.2f)", -t.Amount, t.BalanceBefore, t.BalanceAfter)
		case "deposit_cancel_no_refund":
			entry.Action = "ยกเลิก (ไม่หักเครดิต)"
			entry.Type = "cancel"
			entry.Description = "ไม่หักเครดิตคืน"
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
