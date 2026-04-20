// Package handler — bets admin handlers.
// Split from stubs.go on 2026-04-20.
package handler

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

// =============================================================================
// Bets + Transactions (read-only)
// =============================================================================

// ListAllBets แสดงรายการเดิมพันแบบ bill-level (group by batch_id)
// GET /api/v1/bets
//
// ⭐ แต่ละ row = 1 บิล (batch_id) → แสดงสรุป: จำนวนเลข, ยอดแทงรวม, ยอดชนะรวม, กำไร/ขาดทุน
// สถานะบิล: pending (มีเลขรอผล), won (กำไร), lost (ขาดทุน), cancelled (ยกเลิกทั้งหมด)
func (h *Handler) ListAllBets(c *gin.Context) {
	page, perPage := pageParams(c)

	type BillRow struct {
		BatchID        string  `json:"batch_id"`
		MemberID       int64   `json:"member_id"`
		Username       string  `json:"username"`
		LotteryRoundID int64   `json:"lottery_round_id"`
		BetCount       int     `json:"bet_count"`
		Numbers        string  `json:"numbers"`
		TotalAmount    float64 `json:"total_amount"`
		TotalWin       float64 `json:"total_win"`
		PendingCount   int     `json:"pending_count"`
		WonCount       int     `json:"won_count"`
		LostCount      int     `json:"lost_count"`
		CancelledCount int     `json:"cancelled_count"`
		CreatedAt      string  `json:"created_at"`
	}

	// ── WHERE conditions ─────────────────────────────────────
	where := "1=1"
	args := []interface{}{}

	if s := c.Query("status"); s != "" {
		// filter bill-level status
		switch s {
		case "pending":
			where += " AND pending_count > 0"
		case "won":
			where += " AND pending_count = 0 AND total_win > total_amount AND cancelled_count < bet_count"
		case "lost":
			where += " AND pending_count = 0 AND total_win <= total_amount AND cancelled_count < bet_count"
		case "cancelled":
			where += " AND cancelled_count = bet_count"
		}
	}

	// ⭐ scope ตามสายงาน — node เห็นเฉพาะ bets ของ members ในสาย
	scope := mw.GetNodeScope(c, h.DB)

	// sub-conditions on bets table
	betWhere := "1=1"
	betArgs := []interface{}{}

	// ⭐ node user: filter เฉพาะ members ในสาย
	if scope.IsNode {
		mIDs := scope.MemberIDsForSQL()
		betWhere += " AND b.member_id IN (?)"
		betArgs = append(betArgs, mIDs)
	}

	if q := c.Query("q"); q != "" {
		like := "%" + q + "%"
		betWhere += " AND (b.number LIKE ? OR m.username LIKE ?)"
		betArgs = append(betArgs, like, like)
	}
	if df := c.Query("date_from"); df != "" {
		betWhere += " AND b.created_at >= ?"
		betArgs = append(betArgs, df+" 00:00:00")
	}
	if dt := c.Query("date_to"); dt != "" {
		betWhere += " AND b.created_at <= ?"
		betArgs = append(betArgs, dt+" 23:59:59")
	}
	if lt := c.Query("lottery_type_id"); lt != "" {
		betWhere += " AND b.lottery_round_id IN (SELECT id FROM lottery_rounds WHERE lottery_type_id = ?)"
		betArgs = append(betArgs, lt)
	}

	// ── Main query: group by batch_id ────────────────────────
	baseSQL := `
		SELECT
			b.batch_id,
			b.member_id,
			m.username,
			b.lottery_round_id,
			COUNT(*) as bet_count,
			GROUP_CONCAT(b.number ORDER BY b.id SEPARATOR ', ') as numbers,
			SUM(b.amount) as total_amount,
			SUM(b.win_amount) as total_win,
			SUM(CASE WHEN b.status = 'pending' THEN 1 ELSE 0 END) as pending_count,
			SUM(CASE WHEN b.status = 'won' THEN 1 ELSE 0 END) as won_count,
			SUM(CASE WHEN b.status = 'lost' THEN 1 ELSE 0 END) as lost_count,
			SUM(CASE WHEN b.status = 'cancelled' THEN 1 ELSE 0 END) as cancelled_count,
			MIN(b.created_at) as created_at
		FROM bets b
		LEFT JOIN members m ON m.id = b.member_id
		WHERE ` + betWhere + `
		GROUP BY b.batch_id, b.member_id, m.username, b.lottery_round_id
	`

	// Count total bills (wrap in subquery)
	var total int64
	countSQL := "SELECT COUNT(*) FROM (" + baseSQL + " HAVING " + where + ") AS bills"
	allArgs := append(betArgs, args...)
	h.DB.Raw(countSQL, allArgs...).Scan(&total)

	// Paginated results
	dataSQL := baseSQL + " HAVING " + where + " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	allArgs = append(allArgs, perPage, (page-1)*perPage)

	var rows []BillRow
	h.DB.Raw(dataSQL, allArgs...).Scan(&rows)

	paginated(c, rows, total, page, perPage)
}

// GetBillDetail ดึงทุก bets ในบิลเดียวกัน (batch_id)
// GET /api/v1/bets/bill/:batchId
func (h *Handler) GetBillDetail(c *gin.Context) {
	batchID := c.Param("batchId")
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope
	if batchID == "" {
		fail(c, 400, "invalid batch_id")
		return
	}

	query := h.DB.Model(&model.Bet{}).
		Preload("Member").Preload("BetType").Preload("LotteryRound").
		Where("batch_id = ?", batchID)
	query = scope.ScopeByMemberID(query, "member_id") // ⭐
	var bets []model.Bet
	query.Order("id ASC").Find(&bets)

	if len(bets) == 0 {
		fail(c, 404, "ไม่พบบิล")
		return
	}

	// สรุปยอดรวม
	var totalAmount, totalWin float64
	for _, b := range bets {
		totalAmount += b.Amount
		totalWin += b.WinAmount
	}

	ok(c, gin.H{
		"batch_id":     batchID,
		"bets":         bets,
		"count":        len(bets),
		"total_amount": totalAmount,
		"total_win":    totalWin,
	})
}

// CancelBill ยกเลิกทั้งบิล (ทุก bet ใน batch_id เดียวกัน)
// PUT /api/v1/bets/bill/:batchId/cancel
// Body: { "refund": true/false, "reason": "เหตุผล" }
func (h *Handler) CancelBill(c *gin.Context) {
	batchID := c.Param("batchId")
	adminID := middleware.GetAdminID(c)
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope

	var req struct {
		Refund bool   `json:"refund"`
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// ดึงทุก bet ในบิลที่ยังไม่ถูกยกเลิก
	var bets []model.Bet
	query := h.DB.Where("batch_id = ? AND status != 'cancelled'", batchID)
	query = scope.ScopeByMemberID(query, "member_id") // ⭐
	query.Find(&bets)
	if len(bets) == 0 {
		fail(c, 400, "ไม่มีรายการที่สามารถยกเลิกได้")
		return
	}

	tx := h.DB.Begin()
	now := time.Now()

	var totalRefund float64
	var totalDebit float64

	for _, bet := range bets {
		// อัพเดทสถานะ
		tx.Model(&model.Bet{}).Where("id = ?", bet.ID).Updates(map[string]interface{}{
			"status":        "cancelled",
			"cancelled_at":  now,
			"cancelled_by":  adminID,
			"cancel_reason": req.Reason,
		})

		if req.Refund {
			switch bet.Status {
			case "pending", "lost":
				totalRefund += bet.Amount
			case "won":
				totalDebit += bet.WinAmount
				totalRefund += bet.Amount
			}
		}
	}

	memberID := bets[0].MemberID

	if req.Refund {
		// หัก win_amount (ถ้ามี bet ที่ชนะ)
		if totalDebit > 0 {
			debitResult := tx.Exec("UPDATE members SET balance = balance - ? WHERE id = ? AND balance >= ?", totalDebit, memberID, totalDebit)
			if debitResult.RowsAffected == 0 {
				tx.Rollback()
				fail(c, 400, "สมาชิกมียอดเงินไม่เพียงพอสำหรับหักรางวัลคืน")
				return
			}
			var bal float64
			tx.Table("members").Select("balance").Where("id = ?", memberID).Row().Scan(&bal)
			tx.Exec(`INSERT INTO transactions (agent_node_id, member_id, type, amount, balance_before, balance_after, reference_type, note, created_at)
				VALUES (1, ?, 'admin_debit', ?, ?, ?, 'bill_void_debit', ?, ?)`,
				memberID, -totalDebit, bal+totalDebit, bal, "หักรางวัลคืน (void bill "+batchID[:8]+"): "+req.Reason, now)
		}

		// คืนเงินเดิมพัน
		if totalRefund > 0 {
			tx.Exec("UPDATE members SET balance = balance + ? WHERE id = ?", totalRefund, memberID)
			var bal float64
			tx.Table("members").Select("balance").Where("id = ?", memberID).Row().Scan(&bal)
			tx.Exec(`INSERT INTO transactions (agent_node_id, member_id, type, amount, balance_before, balance_after, reference_type, note, created_at)
				VALUES (1, ?, 'refund', ?, ?, ?, 'bill_cancel', ?, ?)`,
				memberID, totalRefund, bal-totalRefund, bal, "คืนเงินบิล "+batchID[:8]+": "+req.Reason, now)
		}
	} else {
		var balance float64
		tx.Table("members").Select("balance").Where("id = ?", memberID).Row().Scan(&balance)
		tx.Exec(`INSERT INTO transactions (agent_node_id, member_id, type, amount, balance_before, balance_after, reference_type, note, created_at)
			VALUES (1, ?, 'admin_debit', 0, ?, ?, 'bill_cancel_no_refund', ?, ?)`,
			memberID, balance, balance, "ยกเลิกบิล "+batchID[:8]+" (ไม่คืนเครดิต): "+req.Reason, now)
	}

	tx.Commit()
	ok(c, gin.H{"batch_id": batchID, "cancelled_count": len(bets), "refund": req.Refund, "total_refund": totalRefund})
}

// CancelBet ยกเลิก/void รายการเดิมพันเดี่ยว (ไม่ได้ใช้จาก frontend แล้ว แต่เก็บไว้เป็น API)
// PUT /api/v1/bets/:id/cancel
// Body: { "refund": true/false, "reason": "เหตุผล" }
//
// ⭐ Logic:
// - pending: refund = คืน bet.amount
// - won: void = หัก win_amount ที่จ่ายไปแล้ว (ถ้า refund=true) หรือแค่ mark cancelled
// - lost: void = คืน bet.amount (ถ้า refund=true)
// - cancelled: ไม่ทำซ้ำ
func (h *Handler) CancelBet(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	adminID := middleware.GetAdminID(c)
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope

	var req struct {
		Refund bool   `json:"refund"`
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// ดึงข้อมูล bet
	var bet model.Bet
	if err := h.DB.First(&bet, id).Error; err != nil {
		fail(c, 404, "ไม่พบรายการเดิมพัน")
		return
	}
	if !scope.HasMember(bet.MemberID) {
		fail(c, 403, "ไม่มีสิทธิ์")
		return
	} // ⭐ scope
	if bet.Status == "cancelled" {
		fail(c, 400, "รายการนี้ถูกยกเลิกไปแล้ว")
		return
	}

	tx := h.DB.Begin()
	now := time.Now()

	// อัพเดทสถานะ bet → cancelled
	tx.Model(&model.Bet{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":        "cancelled",
		"cancelled_at":  now,
		"cancelled_by":  adminID,
		"cancel_reason": req.Reason,
	})

	if req.Refund {
		// คำนวณจำนวนเงินที่ต้องจัดการ
		var refundAmount float64
		var debitAmount float64

		switch bet.Status {
		case "pending":
			// คืน bet amount
			refundAmount = bet.Amount
		case "won":
			// หัก win_amount ที่จ่ายไป + คืน bet amount
			debitAmount = bet.WinAmount
			refundAmount = bet.Amount
		case "lost":
			// คืน bet amount
			refundAmount = bet.Amount
		}

		// หัก win_amount (ถ้า won)
		if debitAmount > 0 {
			debitResult := tx.Exec("UPDATE members SET balance = balance - ? WHERE id = ? AND balance >= ?", debitAmount, bet.MemberID, debitAmount)
			if debitResult.RowsAffected == 0 {
				tx.Rollback()
				fail(c, 400, "สมาชิกมียอดเงินไม่เพียงพอสำหรับหักคืนรางวัล")
				return
			}

			var balAfterDebit float64
			tx.Table("members").Select("balance").Where("id = ?", bet.MemberID).Row().Scan(&balAfterDebit)

			tx.Exec(`INSERT INTO transactions (agent_node_id, member_id, type, amount, balance_before, balance_after, reference_id, reference_type, note, created_at)
				VALUES (1, ?, 'admin_debit', ?, ?, ?, ?, 'bet_void_debit', ?, ?)`,
				bet.MemberID, -debitAmount, balAfterDebit+debitAmount, balAfterDebit, id,
				"หักรางวัลคืน (void bet #"+strconv.FormatInt(id, 10)+")", now)
		}

		// คืนเงินเดิมพัน
		if refundAmount > 0 {
			tx.Exec("UPDATE members SET balance = balance + ? WHERE id = ?", refundAmount, bet.MemberID)

			var balAfter float64
			tx.Table("members").Select("balance").Where("id = ?", bet.MemberID).Row().Scan(&balAfter)

			tx.Exec(`INSERT INTO transactions (agent_node_id, member_id, type, amount, balance_before, balance_after, reference_id, reference_type, note, created_at)
				VALUES (1, ?, 'refund', ?, ?, ?, ?, 'bet_cancel', ?, ?)`,
				bet.MemberID, refundAmount, balAfter-refundAmount, balAfter, id,
				"คืนเงินเดิมพัน #"+strconv.FormatInt(id, 10)+": "+req.Reason, now)
		}
	} else {
		// ไม่คืนเครดิต — บันทึก audit trail
		var balance float64
		tx.Table("members").Select("balance").Where("id = ?", bet.MemberID).Row().Scan(&balance)

		tx.Exec(`INSERT INTO transactions (agent_node_id, member_id, type, amount, balance_before, balance_after, reference_id, reference_type, note, created_at)
			VALUES (1, ?, 'admin_debit', 0, ?, ?, ?, 'bet_cancel_no_refund', ?, ?)`,
			bet.MemberID, balance, balance, id,
			"ยกเลิกเดิมพัน #"+strconv.FormatInt(id, 10)+" (ไม่คืนเครดิต): "+req.Reason, now)
	}

	tx.Commit()
	ok(c, gin.H{"id": id, "status": "cancelled", "refund": req.Refund, "reason": req.Reason, "previous_status": bet.Status})
}
