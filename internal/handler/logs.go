package handler

// =============================================================================
// Log History — timeline ดูประวัติรายการฝาก/ถอน
//
// รวมข้อมูลจาก 3 แหล่ง:
// 1. deposit_requests / withdraw_requests → "สร้างคำขอ" + สถานะเปลี่ยน
// 2. activity_logs (audit middleware) → action ที่แอดมินทำ + ใคร
// 3. transactions → ผลกระทบทางการเงิน (จำนวนเงิน, balance ก่อน/หลัง)
// =============================================================================

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// LogEntry แต่ละ entry ใน timeline
type LogEntry struct {
	Timestamp   time.Time `json:"timestamp"`
	Action      string    `json:"action"`
	Description string    `json:"description"`
	Actor       string    `json:"actor"`
	Type        string    `json:"type"` // create, approve, reject, cancel, bonus, refund
	Amount      float64   `json:"amount,omitempty"`
}

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
	// ดึง audit log ที่ path ตรงกับ /deposits/:id/xxx
	var activities []struct {
		AdminID    int64     `json:"admin_id"`
		Method     string    `json:"method"`
		Path       string    `json:"path"`
		StatusCode int       `json:"status_code"`
		CreatedAt  time.Time `json:"created_at"`
	}
	pathPattern := fmt.Sprintf("%%/deposits/%d/%%", id)
	h.DB.Table("activity_logs").
		Where("path LIKE ? AND status_code = 200", pathPattern).
		Order("created_at ASC").
		Find(&activities)

	// ดึงชื่อ admin ทั้งหมดที่เกี่ยวข้อง
	var adminIDs []int64
	for _, a := range activities {
		adminIDs = append(adminIDs, a.AdminID)
	}
	adminNames := h.resolveAdminNames(adminIDs)

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
	var txns []struct {
		Type          string    `json:"type"`
		Amount        float64   `json:"amount"`
		BalanceBefore float64   `json:"balance_before"`
		BalanceAfter  float64   `json:"balance_after"`
		ReferenceType string    `json:"reference_type"`
		Note          string    `json:"note"`
		CreatedAt     time.Time `json:"created_at"`
	}
	h.DB.Table("transactions").
		Where("reference_id = ? AND reference_type LIKE 'deposit%'", id).
		Or("reference_id = ? AND reference_type = 'first_deposit'", id).
		Order("created_at ASC").
		Find(&txns)

	for _, t := range txns {
		// ข้าม deposit transaction หลัก (ซ้ำกับ approve ข้างบน)
		if t.ReferenceType == "deposit_request" {
			// เพิ่ม description ให้ approve entry ที่มีอยู่แล้ว
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

	// เรียงตามเวลา
	sort.Slice(logs, func(i, j int) bool {
		return logs[i].Timestamp.Before(logs[j].Timestamp)
	})

	ok(c, logs)
}

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

	// ดึงชื่อสมาชิก
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
	var activities []struct {
		AdminID    int64     `json:"admin_id"`
		Method     string    `json:"method"`
		Path       string    `json:"path"`
		StatusCode int       `json:"status_code"`
		CreatedAt  time.Time `json:"created_at"`
	}
	pathPattern := fmt.Sprintf("%%/withdrawals/%d/%%", id)
	h.DB.Table("activity_logs").
		Where("path LIKE ? AND status_code = 200", pathPattern).
		Order("created_at ASC").
		Find(&activities)

	var wAdminIDs []int64
	for _, a := range activities {
		wAdminIDs = append(wAdminIDs, a.AdminID)
	}
	adminNames := h.resolveAdminNames(wAdminIDs)

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
	var txns []struct {
		Type          string    `json:"type"`
		Amount        float64   `json:"amount"`
		BalanceBefore float64   `json:"balance_before"`
		BalanceAfter  float64   `json:"balance_after"`
		ReferenceType string    `json:"reference_type"`
		Note          string    `json:"note"`
		CreatedAt     time.Time `json:"created_at"`
	}
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

	// ดึงชื่อสมาชิก
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
	var activities []struct {
		AdminID    int64     `json:"admin_id"`
		Method     string    `json:"method"`
		Path       string    `json:"path"`
		StatusCode int       `json:"status_code"`
		CreatedAt  time.Time `json:"created_at"`
	}
	pathPattern := fmt.Sprintf("%%/bets/%d/%%", id)
	h.DB.Table("activity_logs").
		Where("path LIKE ? AND status_code = 200", pathPattern).
		Order("created_at ASC").
		Find(&activities)

	var adminIDs []int64
	for _, a := range activities {
		adminIDs = append(adminIDs, a.AdminID)
	}
	adminNames := h.resolveAdminNames(adminIDs)

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
	var txns []struct {
		Type          string    `json:"type"`
		Amount        float64   `json:"amount"`
		BalanceBefore float64   `json:"balance_before"`
		BalanceAfter  float64   `json:"balance_after"`
		ReferenceType string    `json:"reference_type"`
		CreatedAt     time.Time `json:"created_at"`
	}
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

// ── helpers ─────────────────────────────────────────────────────────

// resolveAdminNames ดึงชื่อ admin จาก ID list
func (h *Handler) resolveAdminNames(adminIDs []int64) map[int64]string {
	names := make(map[int64]string)
	if len(adminIDs) == 0 {
		return names
	}

	// deduplicate
	unique := make(map[int64]bool)
	var ids []int64
	for _, id := range adminIDs {
		if !unique[id] {
			unique[id] = true
			ids = append(ids, id)
		}
	}

	var admins []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	h.DB.Table("admins").Select("id, name").Where("id IN ?", ids).Find(&admins)
	for _, a := range admins {
		if a.Name != "" {
			names[a.ID] = a.Name
		} else {
			names[a.ID] = fmt.Sprintf("Admin #%d", a.ID)
		}
	}
	return names
}

// contains — strings.Contains shorthand
func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
