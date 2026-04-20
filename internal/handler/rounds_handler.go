// Package handler — rounds admin handlers.
// Split from stubs.go on 2026-04-20.
package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/farritpcz/lotto-standalone-admin-api/internal/job"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/service"
)

// =============================================================================
// Rounds
// =============================================================================

func (h *Handler) ListRounds(c *gin.Context) {
	page, perPage := pageParams(c)
	// ⭐ ทุกเว็บเห็นรอบเดียวกัน (ผลหวยเหมือนกัน เช่น หวยรัฐออก 847 ทุกเว็บก็ 847)
	// แต่สถานะเปิด/ปิดรับแทงแยกตามเว็บ (ผ่าน agent_round_config)
	var rounds []model.LotteryRound
	var total int64
	query := h.DB.Model(&model.LotteryRound{}).Preload("LotteryType")
	if s := c.Query("status"); s != "" {
		query = query.Where("status = ?", s)
	}
	if lt := c.Query("lottery_type_id"); lt != "" {
		query = query.Where("lottery_type_id = ?", lt)
	}
	query.Count(&total)
	query.Order("round_date DESC, close_time DESC").Offset((page - 1) * perPage).Limit(perPage).Find(&rounds)
	paginated(c, rounds, total, page, perPage)
}

func (h *Handler) CreateRound(c *gin.Context) {
	var round model.LotteryRound
	if err := c.ShouldBindJSON(&round); err != nil {
		fail(c, 400, err.Error())
		return
	}
	round.Status = "upcoming"
	if err := h.DB.Create(&round).Error; err != nil {
		fail(c, 500, "failed to create round")
		return
	}
	ok(c, round)
}

func (h *Handler) UpdateRoundStatus(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	h.DB.Model(&model.LotteryRound{}).Where("id = ?", id).Update("status", req.Status)
	ok(c, gin.H{"id": id, "status": req.Status})
}

// =============================================================================
// Manual Round Control — เปิด/ปิด/ยกเลิก ผ่าน RoundService
// =============================================================================

// ManualOpenRound เปิดรับแทงรอบที่ยัง upcoming อยู่
// PUT /api/v1/rounds/:id/open
// ใช้เมื่อ: admin ต้องการเปิดรอบก่อนเวลา
func (h *Handler) ManualOpenRound(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	svc := h.getRoundService()
	if svc == nil {
		fail(c, 500, "round service not configured")
		return
	}
	if err := svc.OpenRound(id); err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, gin.H{"id": id, "status": "open", "message": "เปิดรับแทงสำเร็จ"})
}

// ManualCloseRound ปิดรับแทงรอบที่ open อยู่
// PUT /api/v1/rounds/:id/close
// ใช้เมื่อ: admin ต้องการปิดรอบก่อนเวลา
func (h *Handler) ManualCloseRound(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	svc := h.getRoundService()
	if svc == nil {
		fail(c, 500, "round service not configured")
		return
	}
	if err := svc.CloseRound(id); err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, gin.H{"id": id, "status": "closed", "message": "ปิดรับแทงสำเร็จ"})
}

// VoidRound ยกเลิกรอบ + refund ทุก bet
// PUT /api/v1/rounds/:id/void
// Body: { "reason": "กรอกผลผิด" }
// ⚠️ operation นี้รุนแรง — ยกเลิกรอบ + คืนเงินทุก bet + หักรางวัลที่จ่ายแล้ว
func (h *Handler) VoidRound(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	adminID := middleware.GetAdminID(c)
	var req struct {
		Reason string `json:"reason"`
	}
	c.ShouldBindJSON(&req)
	if req.Reason == "" {
		req.Reason = "ยกเลิกโดยแอดมิน"
	}

	svc := h.getRoundService()
	if svc == nil {
		fail(c, 500, "round service not configured")
		return
	}
	result, err := svc.VoidRound(id, req.Reason, adminID)
	if err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, result)
}

// ListSchedules แสดงตาราง schedule สร้างรอบอัตโนมัติ
// GET /api/v1/rounds/schedules
func (h *Handler) ListSchedules(c *gin.Context) {
	ok(c, job.GetDefaultSchedules(h.DB))
}

// getRoundService ดึง RoundService จาก Handler (type assertion)
func (h *Handler) getRoundService() *service.RoundService {
	if h.RoundService == nil {
		return nil
	}
	svc, ok := h.RoundService.(*service.RoundService)
	if !ok {
		return nil
	}
	return svc
}
