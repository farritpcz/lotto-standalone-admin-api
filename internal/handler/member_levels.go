// Package handler — member_levels.go
// ระบบ Member Level สำหรับ admin-api (#5)
//
// ⭐ ฟีเจอร์:
// - CRUD levels (Bronze, Silver, Gold, Platinum ฯลฯ)
// - กำหนดเงื่อนไขเลื่อน level: ยอดฝากขั้นต่ำ, จำนวน bet ขั้นต่ำ
// - ผลประโยชน์แต่ละ level: commission rate %, cashback %, bonus %
// - แสดงจำนวนสมาชิกในแต่ละ level (realtime count)
//
// ความสัมพันธ์:
// - ตาราง member_levels → share DB กับ member-api (#3)
// - member-web (#4) ใช้แสดงสิทธิประโยชน์
// - admin-web (#6) ใช้ CRUD + ตั้งค่า commission rate
//
// Routes:
//   GET    /api/v1/member-levels          → รายการ level ทั้งหมด + member count
//   POST   /api/v1/member-levels          → สร้าง level ใหม่
//   PUT    /api/v1/member-levels/:id      → แก้ไข level
//   DELETE /api/v1/member-levels/:id      → ลบ level (ถ้าไม่มีสมาชิกอยู่)
//   PUT    /api/v1/member-levels/reorder  → จัดลำดับ level
package handler

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// =============================================================================
// Inline Model — memberLevel
// ⭐ ใช้ inline struct เพราะไม่ต้อง share กับ service layer
// =============================================================================

// memberLevel โครงสร้างตาราง member_levels
// เก็บชื่อ level, สี, เงื่อนไข, ผลประโยชน์
type memberLevel struct {
	ID             int64     `json:"id" gorm:"primaryKey"`
	AgentID        int64     `json:"agent_id" gorm:"not null;default:1;index"`       // ⭐ 1 agent = 1 ชุด levels
	Name           string    `json:"name" gorm:"size:50;not null"`                    // ชื่อ level เช่น "Bronze"
	Color          string    `json:"color" gorm:"size:20;not null;default:#CD7F32"`   // สีแสดงใน UI
	Icon           string    `json:"icon" gorm:"size:50"`                              // icon (Lucide name)
	SortOrder      int       `json:"sort_order" gorm:"not null;default:0"`             // ลำดับ (น้อย→สูง)
	MinDeposit     float64   `json:"min_deposit" gorm:"type:decimal(15,2);default:0"` // ยอดฝากขั้นต่ำเพื่อเลื่อน level
	MinBets        int       `json:"min_bets" gorm:"default:0"`                       // จำนวน bet ขั้นต่ำ
	CommissionRate float64   `json:"commission_rate" gorm:"type:decimal(5,2);default:0"` // ค่าคอม affiliate %
	CashbackRate   float64   `json:"cashback_rate" gorm:"type:decimal(5,2);default:0"`  // คืนยอดเสีย %
	BonusPct       float64   `json:"bonus_pct" gorm:"type:decimal(5,2);default:0"`      // โบนัสฝากเงิน %
	MaxWithdrawDay float64   `json:"max_withdraw_day" gorm:"type:decimal(15,2);default:0"` // ถอนสูงสุด/วัน (0=ไม่จำกัด)
	Description    string    `json:"description" gorm:"type:text"`                        // คำอธิบาย level
	Status         string    `json:"status" gorm:"size:20;not null;default:active"`       // active/inactive
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	MemberCount    int64     `json:"member_count" gorm:"-"` // ⭐ computed field — ไม่เก็บ DB
}

func (memberLevel) TableName() string { return "member_levels" }

// =============================================================================
// ListMemberLevels — GET /api/v1/member-levels
// ดึง level ทั้งหมด + นับจำนวนสมาชิกในแต่ละ level
// =============================================================================
func (h *Handler) ListMemberLevels(c *gin.Context) {
	var levels []memberLevel

	// ดึง levels ทั้งหมด เรียงตาม sort_order (น้อย→มาก)
	if err := h.DB.Where("agent_id = ?", 1).Order("sort_order ASC, id ASC").Find(&levels).Error; err != nil {
		fail(c, 500, "ดึงข้อมูล level ไม่สำเร็จ")
		return
	}

	// ⭐ นับจำนวนสมาชิกในแต่ละ level (LEFT JOIN members.level_id)
	// ใช้ raw query เพราะ GORM subquery ซับซ้อน
	type lvlCount struct {
		LevelID int64 `gorm:"column:level_id"`
		Count   int64 `gorm:"column:cnt"`
	}
	var counts []lvlCount
	h.DB.Raw(`
		SELECT COALESCE(m.level_id, 0) as level_id, COUNT(*) as cnt
		FROM members m
		WHERE m.status = 'active'
		GROUP BY m.level_id
	`).Scan(&counts)

	// map level_id → count สำหรับ lookup เร็วๆ
	countMap := make(map[int64]int64)
	for _, c := range counts {
		countMap[c.LevelID] = c.Count
	}

	// ใส่ member_count ให้แต่ละ level
	for i := range levels {
		levels[i].MemberCount = countMap[levels[i].ID]
	}

	ok(c, levels)
}

// =============================================================================
// CreateMemberLevel — POST /api/v1/member-levels
// สร้าง level ใหม่ พร้อมเงื่อนไข + ผลประโยชน์
// =============================================================================
func (h *Handler) CreateMemberLevel(c *gin.Context) {
	var req struct {
		Name           string  `json:"name" binding:"required,min=1,max=50"`
		Color          string  `json:"color" binding:"required"`        // hex color เช่น "#FFD700"
		Icon           string  `json:"icon"`                            // optional Lucide icon name
		SortOrder      int     `json:"sort_order"`                      // ลำดับแสดง
		MinDeposit     float64 `json:"min_deposit"`                     // ยอดฝากขั้นต่ำ
		MinBets        int     `json:"min_bets"`                        // จำนวน bet ขั้นต่ำ
		CommissionRate float64 `json:"commission_rate"`                  // ค่าคอม %
		CashbackRate   float64 `json:"cashback_rate"`                   // คืนยอดเสีย %
		BonusPct       float64 `json:"bonus_pct"`                       // โบนัส %
		MaxWithdrawDay float64 `json:"max_withdraw_day"`                // ถอนสูงสุด/วัน
		Description    string  `json:"description"`                     // คำอธิบาย
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// ⭐ ตรวจชื่อซ้ำในระบบเดียวกัน (agent_id = 1)
	var exists int64
	h.DB.Table("member_levels").Where("agent_id = 1 AND name = ?", req.Name).Count(&exists)
	if exists > 0 {
		fail(c, 400, "ชื่อ level \""+req.Name+"\" มีอยู่แล้ว")
		return
	}

	level := memberLevel{
		AgentID:        1,
		Name:           req.Name,
		Color:          req.Color,
		Icon:           req.Icon,
		SortOrder:      req.SortOrder,
		MinDeposit:     req.MinDeposit,
		MinBets:        req.MinBets,
		CommissionRate: req.CommissionRate,
		CashbackRate:   req.CashbackRate,
		BonusPct:       req.BonusPct,
		MaxWithdrawDay: req.MaxWithdrawDay,
		Description:    req.Description,
		Status:         "active",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if err := h.DB.Create(&level).Error; err != nil {
		fail(c, 500, "สร้าง level ไม่สำเร็จ: "+err.Error())
		return
	}

	ok(c, level)
}

// =============================================================================
// UpdateMemberLevel — PUT /api/v1/member-levels/:id
// แก้ไข level — อัพเดทเฉพาะ fields ที่ส่งมา
// =============================================================================
func (h *Handler) UpdateMemberLevel(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	var req struct {
		Name           *string  `json:"name"`
		Color          *string  `json:"color"`
		Icon           *string  `json:"icon"`
		SortOrder      *int     `json:"sort_order"`
		MinDeposit     *float64 `json:"min_deposit"`
		MinBets        *int     `json:"min_bets"`
		CommissionRate *float64 `json:"commission_rate"`
		CashbackRate   *float64 `json:"cashback_rate"`
		BonusPct       *float64 `json:"bonus_pct"`
		MaxWithdrawDay *float64 `json:"max_withdraw_day"`
		Description    *string  `json:"description"`
		Status         *string  `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// ⭐ สร้าง map เฉพาะ fields ที่ส่งมา (partial update)
	updates := map[string]interface{}{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Color != nil {
		updates["color"] = *req.Color
	}
	if req.Icon != nil {
		updates["icon"] = *req.Icon
	}
	if req.SortOrder != nil {
		updates["sort_order"] = *req.SortOrder
	}
	if req.MinDeposit != nil {
		updates["min_deposit"] = *req.MinDeposit
	}
	if req.MinBets != nil {
		updates["min_bets"] = *req.MinBets
	}
	if req.CommissionRate != nil {
		updates["commission_rate"] = *req.CommissionRate
	}
	if req.CashbackRate != nil {
		updates["cashback_rate"] = *req.CashbackRate
	}
	if req.BonusPct != nil {
		updates["bonus_pct"] = *req.BonusPct
	}
	if req.MaxWithdrawDay != nil {
		updates["max_withdraw_day"] = *req.MaxWithdrawDay
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}

	if len(updates) == 0 {
		fail(c, 400, "ไม่มีข้อมูลให้อัพเดท")
		return
	}

	updates["updated_at"] = time.Now()

	result := h.DB.Table("member_levels").Where("id = ? AND agent_id = 1", id).Updates(updates)
	if result.RowsAffected == 0 {
		fail(c, 404, "ไม่พบ level นี้")
		return
	}

	// ดึง level ที่อัพเดทแล้ว ส่งกลับ
	var level memberLevel
	h.DB.First(&level, id)
	ok(c, level)
}

// =============================================================================
// DeleteMemberLevel — DELETE /api/v1/member-levels/:id
// ลบ level — ต้องไม่มีสมาชิกอยู่ใน level นี้
// =============================================================================
func (h *Handler) DeleteMemberLevel(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	// ⭐ ตรวจว่ามีสมาชิกอยู่ใน level นี้หรือไม่
	var memberCount int64
	h.DB.Table("members").Where("level_id = ?", id).Count(&memberCount)
	if memberCount > 0 {
		fail(c, 400, "ไม่สามารถลบได้ — มีสมาชิก "+strconv.FormatInt(memberCount, 10)+" คนอยู่ใน level นี้")
		return
	}

	result := h.DB.Exec("DELETE FROM member_levels WHERE id = ? AND agent_id = 1", id)
	if result.RowsAffected == 0 {
		fail(c, 404, "ไม่พบ level นี้")
		return
	}

	ok(c, gin.H{"id": id, "deleted": true})
}

// =============================================================================
// ReorderMemberLevels — PUT /api/v1/member-levels/reorder
// จัดลำดับ levels ใหม่ (drag & drop)
// ⭐ รับ array ของ {id, sort_order} → อัพเดท sort_order ทีเดียว
// =============================================================================
func (h *Handler) ReorderMemberLevels(c *gin.Context) {
	var req struct {
		Orders []struct {
			ID        int64 `json:"id" binding:"required"`
			SortOrder int   `json:"sort_order"`
		} `json:"orders" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// ⭐ อัพเดททีละ row (ใน transaction เดียว)
	tx := h.DB.Begin()
	for _, o := range req.Orders {
		tx.Exec("UPDATE member_levels SET sort_order = ? WHERE id = ? AND agent_id = 1", o.SortOrder, o.ID)
	}
	tx.Commit()

	ok(c, gin.H{"updated": len(req.Orders)})
}
