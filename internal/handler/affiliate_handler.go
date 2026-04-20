// Package handler — affiliate admin handlers.
// Split from stubs.go on 2026-04-20.
package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

// =============================================================================
// Affiliate Settings — agent ตั้งค่า commission rate ต่อประเภทหวย + withdrawal conditions
//
// GET  /api/v1/admin/affiliate/settings → ดูค่าทั้งหมด (รวม default + per-lottery)
// POST /api/v1/admin/affiliate/settings → upsert: สร้างหรืออัพเดท
// GET  /api/v1/admin/affiliate/report   → รายงาน commission ทั้งหมด
// =============================================================================

func (h *Handler) GetAffiliateSettings(c *gin.Context) {
	// ⭐ ดึง scope — node เห็นเฉพาะ affiliate settings ของตัวเอง + ของระบบ (fallback)
	scope := mw.GetNodeScope(c, h.DB)

	var settings []model.AffiliateSettings
	query := h.DB.Preload("LotteryType").Where("status = ?", "active")
	// ⭐ scope: เว็บใครเว็บมัน — เห็นเฉพาะของตัวเอง
	if scope.IsNode {
		query = query.Where("agent_node_id = ?", scope.NodeID)
	} else {
		query = query.Where("agent_node_id = ?", scope.RootNodeID)
	}
	query.Order("lottery_type_id ASC").Find(&settings)
	ok(c, settings)
}

// UpsertAffiliateSetting สร้างหรืออัพเดท setting
// Body: { "lottery_type_id": null|1, "commission_rate": 0.8, "withdrawal_min": 10, "withdrawal_note": "..." }
func (h *Handler) UpsertAffiliateSetting(c *gin.Context) {
	// ⭐ ดึง scope — node สร้าง/แก้ได้เฉพาะ settings ของตัวเอง
	scope := mw.GetNodeScope(c, h.DB)

	var req struct {
		LotteryTypeID  *int64  `json:"lottery_type_id"` // nil = default
		CommissionRate float64 `json:"commission_rate" binding:"required,min=0,max=100"`
		WithdrawalMin  float64 `json:"withdrawal_min"`
		WithdrawalNote string  `json:"withdrawal_note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// ⭐ Upsert: หาทุก status (รวม inactive) — ป้องกัน duplicate
	// scope ตามสายงาน: node หา/สร้างเฉพาะ settings ของตัวเอง
	var existing model.AffiliateSettings
	query := h.DB
	if scope.IsNode {
		query = query.Where("agent_node_id = ?", scope.NodeID)
	} else {
		query = query.Where("agent_node_id = ?", scope.RootNodeID)
	}
	if req.LotteryTypeID == nil {
		query = query.Where("lottery_type_id IS NULL")
	} else {
		query = query.Where("lottery_type_id = ?", *req.LotteryTypeID)
	}

	if err := query.First(&existing).Error; err != nil {
		// ไม่มีเลย → สร้างใหม่
		setting := model.AffiliateSettings{
			LotteryTypeID:  req.LotteryTypeID,
			CommissionRate: req.CommissionRate,
			WithdrawalMin:  req.WithdrawalMin,
			WithdrawalNote: req.WithdrawalNote,
			Status:         "active",
		}
		if err := h.DB.Create(&setting).Error; err != nil {
			fail(c, 500, "failed to create")
			return
		}
		ok(c, setting)
		return
	}

	// มีอยู่แล้ว → อัพเดท (reactivate ถ้าเป็น inactive)
	updates := map[string]interface{}{
		"commission_rate": req.CommissionRate,
		"withdrawal_min":  req.WithdrawalMin,
		"withdrawal_note": req.WithdrawalNote,
		"status":          "active",
	}
	h.DB.Model(&existing).Updates(updates)
	h.DB.Preload("LotteryType").First(&existing, existing.ID)
	ok(c, existing)
}

func (h *Handler) DeleteAffiliateSetting(c *gin.Context) {
	// ⭐ ดึง scope — ป้องกัน node ลบ settings ของ node อื่น
	scope := mw.GetNodeScope(c, h.DB)

	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	// ⭐ scope ตามสายงาน: node ลบได้เฉพาะ settings ของตัวเอง
	query := h.DB.Model(&model.AffiliateSettings{}).Where("id = ?", id)
	if scope.IsNode {
		query = query.Where("agent_node_id = ?", scope.NodeID)
	}
	result := query.Update("status", "inactive")
	if result.RowsAffected == 0 {
		fail(c, 404, "ไม่พบ setting นี้หรือไม่มีสิทธิ์ลบ")
		return
	}
	ok(c, gin.H{"id": id, "status": "inactive"})
}

// GetAffiliateReport รายงาน commission ทั้งหมด (สำหรับ agent ดู)
func (h *Handler) GetAffiliateReport(c *gin.Context) {
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope
	type CommSummary struct {
		MemberID        int64   `json:"member_id"`
		Username        string  `json:"username"`
		TotalReferred   int64   `json:"total_referred"`
		TotalCommission float64 `json:"total_commission"`
		PendingComm     float64 `json:"pending_commission"`
	}
	var report []CommSummary
	query := h.DB.Table("referral_commissions rc").
		Select("rc.referrer_id as member_id, m.username, COUNT(DISTINCT rc.referred_id) as total_referred, COALESCE(SUM(rc.commission_amount), 0) as total_commission, COALESCE(SUM(CASE WHEN rc.status='pending' THEN rc.commission_amount ELSE 0 END), 0) as pending_commission").
		Joins("LEFT JOIN members m ON m.id = rc.referrer_id")
	// ⭐ node เห็นเฉพาะ commissions ของ members ในสาย
	if scope.IsNode {
		query = query.Where("rc.referrer_id IN ?", scope.MemberIDsForSQL())
	}
	query.Group("rc.referrer_id, m.username").
		Order("total_commission DESC").
		Scan(&report)

	ok(c, report)
}

// =============================================================================
// Share Templates — ข้อความสำเร็จรูปสำหรับแชร์ลิงก์เชิญ (admin จัดการ)
// =============================================================================

// ListShareTemplates ดึง templates ทั้งหมดของ agent
// ⭐ Node Scope: node เห็นเฉพาะ templates ของตัวเอง, admin เห็นของระบบกลาง
func (h *Handler) ListShareTemplates(c *gin.Context) {
	// ⭐ ดึง scope — ถ้าเป็น node จะ filter เฉพาะข้อมูลของ node นั้น
	scope := mw.GetNodeScope(c, h.DB)

	var templates []model.ShareTemplate
	// ⭐ scope ตามสายงาน: node เห็นเฉพาะของตัวเอง, admin เห็นของระบบกลาง
	query := h.DB.Model(&model.ShareTemplate{})
	if scope.IsNode {
		query = query.Where("agent_node_id = ?", scope.NodeID)
	} else {
		query = query.Where("agent_node_id = ?", scope.RootNodeID)
	}
	query.Order("sort_order ASC, id ASC").Find(&templates)
	ok(c, templates)
}

// CreateShareTemplate สร้าง template ใหม่
// Body: { "name": "...", "content": "สมัครเลย! {link}", "platform": "all", "sort_order": 0 }
func (h *Handler) CreateShareTemplate(c *gin.Context) {
	var req struct {
		Name      string `json:"name" binding:"required"`
		Content   string `json:"content" binding:"required"`
		Platform  string `json:"platform"`
		SortOrder int    `json:"sort_order"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	if req.Platform == "" {
		req.Platform = "all"
	}

	tmpl := model.ShareTemplate{
		Name:      req.Name,
		Content:   req.Content,
		Platform:  req.Platform,
		SortOrder: req.SortOrder,
		Status:    "active",
	}
	if err := h.DB.Create(&tmpl).Error; err != nil {
		fail(c, 500, "สร้าง template ไม่สำเร็จ")
		return
	}
	ok(c, tmpl)
}

// UpdateShareTemplate แก้ไข template
// ⭐ Node Scope: node แก้ได้เฉพาะ template ของตัวเอง
func (h *Handler) UpdateShareTemplate(c *gin.Context) {
	// ⭐ ดึง scope — ใช้ filter WHERE เพื่อป้องกัน node แก้ข้อมูลของ node อื่น
	scope := mw.GetNodeScope(c, h.DB)

	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	var req struct {
		Name      *string `json:"name"`
		Content   *string `json:"content"`
		Platform  *string `json:"platform"`
		SortOrder *int    `json:"sort_order"`
		Status    *string `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// ⭐ scope ตามสายงาน: node ต้องเป็นเจ้าของ template ถึงจะแก้ได้
	var tmpl model.ShareTemplate
	findQuery := h.DB.Where("id = ?", id)
	if scope.IsNode {
		findQuery = findQuery.Where("agent_node_id = ?", scope.NodeID)
	}
	if err := findQuery.First(&tmpl).Error; err != nil {
		fail(c, 404, "ไม่พบ template หรือไม่มีสิทธิ์แก้ไข")
		return
	}

	updates := make(map[string]interface{})
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Content != nil {
		updates["content"] = *req.Content
	}
	if req.Platform != nil {
		updates["platform"] = *req.Platform
	}
	if req.SortOrder != nil {
		updates["sort_order"] = *req.SortOrder
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}

	h.DB.Model(&tmpl).Updates(updates)
	h.DB.First(&tmpl, id)
	ok(c, tmpl)
}

// DeleteShareTemplate ลบ template (soft delete → status=inactive)
// ⭐ Node Scope: node ลบได้เฉพาะ template ของตัวเอง
func (h *Handler) DeleteShareTemplate(c *gin.Context) {
	// ⭐ ดึง scope — ป้องกัน node ลบข้อมูลของ node อื่น
	scope := mw.GetNodeScope(c, h.DB)

	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	// ⭐ scope ตามสายงาน: node ลบได้เฉพาะของตัวเอง
	query := h.DB.Model(&model.ShareTemplate{}).Where("id = ?", id)
	if scope.IsNode {
		query = query.Where("agent_node_id = ?", scope.NodeID)
	}
	result := query.Update("status", "inactive")
	if result.RowsAffected == 0 {
		fail(c, 404, "ไม่พบ template หรือไม่มีสิทธิ์ลบ")
		return
	}
	ok(c, gin.H{"id": id, "status": "deleted"})
}

// =============================================================================
// Manual Commission Adjustment — admin ปรับค่าคอมด้วยมือ + audit log
// =============================================================================

// ListCommissionAdjustments ดูประวัติการปรับค่าคอม
// Query: ?member_id=11&page=1&per_page=20
func (h *Handler) ListCommissionAdjustments(c *gin.Context) {
	page, perPage := pageParams(c)
	memberIDStr := c.Query("member_id")

	// ⭐ ไม่ filter agent_node_id ตรงนี้ — admin เห็นทั้งหมด
	query := h.DB.Model(&model.CommissionAdjustment{}).
		Preload("Member")

	if memberIDStr != "" {
		mID, _ := strconv.ParseInt(memberIDStr, 10, 64)
		query = query.Where("member_id = ?", mID)
	}

	var total int64
	query.Count(&total)

	var items []model.CommissionAdjustment
	query.Order("created_at DESC").Offset((page - 1) * perPage).Limit(perPage).Find(&items)

	totalPages := int(total) / perPage
	if int(total)%perPage > 0 {
		totalPages++
	}

	c.JSON(200, gin.H{
		"success": true,
		"data":    items,
		"meta":    gin.H{"page": page, "per_page": perPage, "total": total, "total_pages": totalPages},
	})
}

// CreateCommissionAdjustment ปรับค่าคอม: เพิ่ม / ลด / ยกเลิก
//
// Body: { "member_id": 11, "type": "add|deduct|cancel", "amount": 100.00, "reason": "...", "commission_id": null }
//
// Logic:
//   - add: เพิ่มค่าคอม pending ให้สมาชิก (สร้าง referral_commission ใหม่)
//   - deduct: หักค่าคอม pending (ลดจาก wallet balance)
//   - cancel: ยกเลิก commission เฉพาะรายการ (เปลี่ยน status เป็น cancelled)
func (h *Handler) CreateCommissionAdjustment(c *gin.Context) {
	adminID := mw.GetAdminID(c)
	if adminID == 0 {
		fail(c, 401, "unauthenticated")
		return
	}

	var req struct {
		MemberID     int64   `json:"member_id" binding:"required"`
		Type         string  `json:"type" binding:"required,oneof=add deduct cancel"`
		Amount       float64 `json:"amount" binding:"required,gt=0"`
		Reason       string  `json:"reason" binding:"required,min=3"`
		CommissionID *int64  `json:"commission_id"` // สำหรับ cancel เฉพาะรายการ
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// ดึงข้อมูลสมาชิก
	var member model.Member
	if err := h.DB.First(&member, req.MemberID).Error; err != nil {
		fail(c, 404, "ไม่พบสมาชิก")
		return
	}

	balanceBefore := member.Balance
	balanceAfter := balanceBefore

	tx := h.DB.Begin()

	switch req.Type {
	case "add":
		// เพิ่มค่าคอม → สร้าง referral_commission ใหม่ (status=pending)
		comm := model.ReferralCommission{
			ReferrerID:       req.MemberID,
			ReferredID:       req.MemberID, // admin ปรับเอง ไม่มี referred จริง
			BetAmount:        0,
			CommissionRate:   0,
			CommissionAmount: req.Amount,
			Status:           "pending",
		}
		if err := tx.Create(&comm).Error; err != nil {
			tx.Rollback()
			fail(c, 500, "สร้างค่าคอมไม่สำเร็จ")
			return
		}
		// ไม่เพิ่ม balance ทันที — ให้สมาชิกถอนเอง (เหมือน commission ปกติ)

	case "deduct":
		// หักค่าคอม pending → ลด pending commissions
		// อัพเดท commissions ล่าสุดเป็น cancelled จนครบ amount
		var pendingComms []model.ReferralCommission
		tx.Where("referrer_id = ? AND status = ?", req.MemberID, "pending").
			Order("created_at DESC").Find(&pendingComms)

		remaining := req.Amount
		for _, pc := range pendingComms {
			if remaining <= 0 {
				break
			}
			if pc.CommissionAmount <= remaining {
				tx.Model(&pc).Update("status", "cancelled")
				remaining -= pc.CommissionAmount
			} else {
				// partial: ลดจำนวนลง
				tx.Model(&pc).Update("commission_amount", pc.CommissionAmount-remaining)
				remaining = 0
			}
		}

	case "cancel":
		// ยกเลิก commission เฉพาะรายการ
		if req.CommissionID == nil {
			tx.Rollback()
			fail(c, 400, "กรุณาระบุ commission_id สำหรับการยกเลิก")
			return
		}
		result := tx.Model(&model.ReferralCommission{}).
			Where("id = ? AND referrer_id = ? AND status = ?", *req.CommissionID, req.MemberID, "pending").
			Update("status", "cancelled")
		if result.RowsAffected == 0 {
			tx.Rollback()
			fail(c, 404, "ไม่พบรายการค่าคอมที่ต้องการยกเลิก หรือยกเลิกไปแล้ว")
			return
		}
	}

	// สร้าง audit log
	adjustment := model.CommissionAdjustment{
		MemberID:      req.MemberID,
		AdminID:       adminID,
		Type:          req.Type,
		Amount:        req.Amount,
		Reason:        req.Reason,
		CommissionID:  req.CommissionID,
		BalanceBefore: balanceBefore,
		BalanceAfter:  balanceAfter,
	}
	tx.Create(&adjustment)

	tx.Commit()

	ok(c, gin.H{
		"adjustment": adjustment,
		"message":    "ปรับค่าคอมสำเร็จ",
	})
}
