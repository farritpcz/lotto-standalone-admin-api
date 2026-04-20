// Package handler — lotteries admin handlers.
// Split from stubs.go on 2026-04-20.
package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

// =============================================================================
// Lotteries CRUD
// =============================================================================

func (h *Handler) ListLotteries(c *gin.Context) {
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope

	// ⭐ ทุกเว็บเห็นประเภทหวยเหมือนกัน แต่เพิ่ม field "enabled" ตาม config ของเว็บ
	// เวลาเปิด/ปิดส่งผลเฉพาะเว็บตัวเอง (ลูกค้าของเว็บนี้แทงได้/ไม่ได้)
	type lotteryWithEnabled struct {
		model.LotteryType
		Enabled *bool `json:"enabled" gorm:"-"` // nil = ยังไม่ตั้ง (default = เปิด)
	}

	var types []model.LotteryType
	h.DB.Order("id ASC").Find(&types)

	// ดึง config เปิด/ปิด ของเว็บนี้
	enabledMap := map[int64]bool{}
	if scope.IsNode {
		type configRow struct {
			LotteryTypeID int64 `gorm:"column:lottery_type_id"`
			Enabled       bool  `gorm:"column:enabled"`
		}
		var configs []configRow
		h.DB.Raw("SELECT lottery_type_id, enabled FROM agent_lottery_config WHERE agent_node_id = ?", scope.NodeID).Scan(&configs)
		for _, c := range configs {
			enabledMap[c.LotteryTypeID] = c.Enabled
		}
	}

	// Build response — เพิ่ม enabled flag
	result := make([]lotteryWithEnabled, len(types))
	for i, lt := range types {
		result[i].LotteryType = lt
		if scope.IsNode {
			if enabled, ok := enabledMap[lt.ID]; ok {
				result[i].Enabled = &enabled
			}
			// nil = ยังไม่ตั้ง config → frontend แสดงเป็น default (ปิด)
		}
	}

	ok(c, result)
}

func (h *Handler) CreateLottery(c *gin.Context) {
	var lt model.LotteryType
	if err := c.ShouldBindJSON(&lt); err != nil {
		fail(c, 400, err.Error())
		return
	}
	if err := h.DB.Create(&lt).Error; err != nil {
		fail(c, 500, "failed to create")
		return
	}
	ok(c, lt)
}

// UpdateLottery อัพเดทประเภทหวย — partial update
// ⭐ รับเฉพาะ fields ที่ส่งมา (ไม่ต้องส่งทุก field)
// ใช้สำหรับ: toggle status, แก้ชื่อ, แก้ category ฯลฯ
func (h *Handler) UpdateLottery(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	// ⭐ ตรวจว่ามี lottery นี้อยู่จริง
	var lt model.LotteryType
	if err := h.DB.First(&lt, id).Error; err != nil {
		fail(c, 404, "not found")
		return
	}

	// ⭐ ใช้ map สำหรับ partial update (ไม่ bind ลง model ตรง เพราะจะ overwrite ค่าว่าง)
	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// อนุญาตเฉพาะ fields ที่แก้ได้
	allowed := map[string]bool{
		"name": true, "code": true, "category": true,
		"description": true, "icon": true, "status": true,
		"is_auto_result": true, "image_url": true,
	}
	updates := map[string]interface{}{}
	for k, v := range req {
		if allowed[k] {
			updates[k] = v
		}
	}

	if len(updates) == 0 {
		fail(c, 400, "ไม่มีข้อมูลให้อัพเดท")
		return
	}

	h.DB.Model(&lt).Updates(updates)

	// โหลดข้อมูลล่าสุดส่งกลับ
	h.DB.First(&lt, id)
	ok(c, lt)
}

// UpdateLotteryImage อัพเดทรูปประเภทหวย
// PUT /api/v1/lotteries/:id/image
// Body: { "image_url": "https://..." }
func (h *Handler) UpdateLotteryImage(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		ImageURL string `json:"image_url" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	h.DB.Model(&model.LotteryType{}).Where("id = ?", id).Update("image_url", req.ImageURL)
	ok(c, gin.H{"id": id, "image_url": req.ImageURL})
}
