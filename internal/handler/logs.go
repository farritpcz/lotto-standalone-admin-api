package handler

// =============================================================================
// Log History — timeline ดูประวัติรายการฝาก/ถอน/เดิมพัน
//
// หน้าที่ไฟล์นี้: types + shared helpers
//
// Handlers แยกเป็นไฟล์ละหนึ่ง:
//   logs_deposit.go  → GetDepositLogs
//   logs_withdraw.go → GetWithdrawLogs
//   logs_bet.go      → GetBetLogs
//
// รวมข้อมูลจาก 3 แหล่ง:
// 1. deposit_requests / withdraw_requests / bets → "สร้างคำขอ" + สถานะเปลี่ยน
// 2. activity_logs (audit middleware) → action ที่แอดมินทำ + ใคร
// 3. transactions → ผลกระทบทางการเงิน (amount, balance ก่อน/หลัง)
// =============================================================================

import (
	"fmt"
	"strings"
	"time"
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

// activityRow — struct ใช้ภายใน เพื่อดึง activity_logs
type activityRow struct {
	AdminID    int64     `json:"admin_id"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	StatusCode int       `json:"status_code"`
	CreatedAt  time.Time `json:"created_at"`
}

// txnRow — struct ใช้ภายใน เพื่อดึง transactions
type txnRow struct {
	Type          string    `json:"type"`
	Amount        float64   `json:"amount"`
	BalanceBefore float64   `json:"balance_before"`
	BalanceAfter  float64   `json:"balance_after"`
	ReferenceType string    `json:"reference_type"`
	Note          string    `json:"note"`
	CreatedAt     time.Time `json:"created_at"`
}

// resolveAdminNames ดึงชื่อ admin จาก ID list (dedupe ก่อน query)
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

// contains — strings.Contains shorthand (ใช้บ่อยเช็ค path)
func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}

// extractAdminIDs — ดึง adminID ออกจาก activityRow list (ก่อน resolveAdminNames)
func extractAdminIDs(rows []activityRow) []int64 {
	ids := make([]int64, 0, len(rows))
	for _, a := range rows {
		ids = append(ids, a.AdminID)
	}
	return ids
}
