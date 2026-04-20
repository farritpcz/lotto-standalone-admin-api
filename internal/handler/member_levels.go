// Package handler — member_levels.go (v3 — 2026-04-20 rebuild)
//
// ⭐ ระบบ Member Level v3 (โละของเก่า)
//   - เกณฑ์เดียว: ยอดฝาก rolling 30 วัน (`min_deposit_30d`)
//   - สิทธิ์: Badge/Icon cosmetic อย่างเดียว (ไม่มี commission/cashback/bonus)
//   - Auto promote/demote: member-api cron daily 02:00
//   - Admin override: `PUT /members/:id/level` → set level + lock (cron จะไม่แก้)
//   - ระบบ scope per-agent (แต่ละเว็บใต้สายตั้งระดับเอง)
//
// Routes:
//   GET    /api/v1/member-levels               → list + member_count ต่อระดับ
//   POST   /api/v1/member-levels               → สร้าง
//   PUT    /api/v1/member-levels/:id           → แก้
//   DELETE /api/v1/member-levels/:id           → ลบ (ต้องไม่มีสมาชิกอยู่)
//   PUT    /api/v1/member-levels/reorder       → จัดลำดับ
//   PUT    /api/v1/members/:id/level           → override (set + lock)
//   DELETE /api/v1/members/:id/level-lock      → ยกเลิก lock (ให้ cron คำนวณใหม่)
//   GET    /api/v1/members/:id/level-history   → ประวัติการเปลี่ยนระดับ
//
// ความสัมพันธ์:
//   - DB: share กับ member-api (ตาราง member_levels, members.level_id*, member_level_history)
//   - Cron: member-api/internal/job/level_recalc_cron.go คำนวณ + บันทึก history
//   - Rule: docs/rules/member_levels.md
package handler

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
)

// =============================================================================
// Models (inline) — v3 schema
// =============================================================================

// memberLevel — โครงสร้าง `member_levels` (v3 — ตัด commission/cashback/bonus ออก)
type memberLevel struct {
	ID             int64     `json:"id" gorm:"primaryKey"`
	AgentNodeID    *int64    `json:"agent_node_id" gorm:"index"`
	Name           string    `json:"name" gorm:"size:50;not null"`
	Color          string    `json:"color" gorm:"size:20;not null;default:#CD7F32"`
	Icon           string    `json:"icon" gorm:"size:50"`
	SortOrder      int       `json:"sort_order" gorm:"not null;default:0"`
	MinDeposit30d  float64   `json:"min_deposit_30d" gorm:"column:min_deposit_30d;type:decimal(15,2);default:0"`
	Description    string    `json:"description" gorm:"type:text"`
	Status         string    `json:"status" gorm:"size:20;not null;default:active"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	MemberCount    int64     `json:"member_count" gorm:"-"` // computed (not stored)
}

func (memberLevel) TableName() string { return "member_levels" }

// memberLevelHistory — `member_level_history` (audit trail)
type memberLevelHistory struct {
	ID                  int64     `json:"id" gorm:"primaryKey"`
	MemberID            int64     `json:"member_id"`
	FromLevelID         *int64    `json:"from_level_id"`
	ToLevelID           *int64    `json:"to_level_id"`
	Reason              string    `json:"reason"` // auto|admin_override|admin_unlock|initial
	Deposit30dSnapshot  float64   `json:"deposit_30d_snapshot" gorm:"column:deposit_30d_snapshot"`
	ChangedByAdminID    *int64    `json:"changed_by_admin_id"`
	Note                string    `json:"note"`
	CreatedAt           time.Time `json:"created_at"`
}

func (memberLevelHistory) TableName() string { return "member_level_history" }

// getAdminIDPtr — ดึง admin_id จาก gin context (รองรับทั้ง int64 + float64 จาก JWT)
// คืน *int64 (nil ถ้าไม่มี — ใช้กับ column ที่เก็บ NULL ได้)
func getAdminIDPtr(c *gin.Context) *int64 {
	v, exists := c.Get("admin_id")
	if !exists {
		return nil
	}
	if id, ok := v.(int64); ok && id > 0 {
		return &id
	}
	if idF, ok := v.(float64); ok && idF > 0 {
		id := int64(idF)
		return &id
	}
	return nil
}

// =============================================================================
// ListMemberLevels — GET /api/v1/member-levels
// ⭐ scope per-node — node เห็นเฉพาะของตัวเอง, admin เห็น rootNode
// ⭐ แนบ member_count + (optional) distribution pct คำนวณฝั่ง frontend
// =============================================================================
func (h *Handler) ListMemberLevels(c *gin.Context) {
	scope := mw.GetNodeScope(c, h.DB)

	var levels []memberLevel
	q := h.DB.Model(&memberLevel{})
	if scope.IsNode {
		q = q.Where("agent_node_id = ?", scope.NodeID)
	} else {
		q = q.Where("agent_node_id = ?", scope.RootNodeID)
	}
	if err := q.Order("sort_order ASC, id ASC").Find(&levels).Error; err != nil {
		fail(c, 500, "ดึงข้อมูล level ไม่สำเร็จ")
		return
	}

	// นับสมาชิกแต่ละ level (เฉพาะ active + ใน scope)
	type lvlCount struct {
		LevelID int64 `gorm:"column:level_id"`
		Cnt     int64 `gorm:"column:cnt"`
	}
	var counts []lvlCount
	cntQ := h.DB.Table("members").
		Select("COALESCE(level_id, 0) as level_id, COUNT(*) as cnt").
		Where("status = ?", "active").
		Group("level_id")
	if scope.IsNode {
		cntQ = cntQ.Where("agent_node_id = ?", scope.NodeID)
	} else {
		cntQ = cntQ.Where("agent_node_id = ?", scope.RootNodeID)
	}
	cntQ.Scan(&counts)

	countMap := make(map[int64]int64)
	for _, cc := range counts {
		countMap[cc.LevelID] = cc.Cnt
	}
	for i := range levels {
		levels[i].MemberCount = countMap[levels[i].ID]
	}

	// ⭐ unassigned = สมาชิกที่ยังไม่มี level (level_id IS NULL)
	// ส่งกลับแยก field เพื่อให้ admin-web โชว์ใน distribution chart
	unassigned := countMap[0]

	ok(c, gin.H{
		"levels":     levels,
		"unassigned": unassigned, // สมาชิกที่ยังไม่ได้ assign level
	})
}

// =============================================================================
// CreateMemberLevel — POST /api/v1/member-levels
// =============================================================================
func (h *Handler) CreateMemberLevel(c *gin.Context) {
	scope := mw.GetNodeScope(c, h.DB)

	var req struct {
		Name          string  `json:"name" binding:"required,min=1,max=50"`
		Color         string  `json:"color" binding:"required"`
		Icon          string  `json:"icon"`
		SortOrder     int     `json:"sort_order"`
		MinDeposit30d float64 `json:"min_deposit_30d"`
		Description   string  `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// เช็คชื่อซ้ำใน scope
	var exists int64
	dq := h.DB.Table("member_levels").Where("name = ?", req.Name)
	if scope.IsNode {
		dq = dq.Where("agent_node_id = ?", scope.NodeID)
	} else {
		dq = dq.Where("agent_node_id = ?", scope.RootNodeID)
	}
	dq.Count(&exists)
	if exists > 0 {
		fail(c, 400, "ชื่อ level \""+req.Name+"\" มีอยู่แล้ว")
		return
	}

	lvl := memberLevel{
		AgentNodeID:   scope.SettingNodeID(),
		Name:          req.Name,
		Color:         req.Color,
		Icon:          req.Icon,
		SortOrder:     req.SortOrder,
		MinDeposit30d: req.MinDeposit30d,
		Description:   req.Description,
		Status:        "active",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	if err := h.DB.Create(&lvl).Error; err != nil {
		fail(c, 500, "สร้าง level ไม่สำเร็จ: "+err.Error())
		return
	}
	ok(c, lvl)
}

// =============================================================================
// UpdateMemberLevel — PUT /api/v1/member-levels/:id
// =============================================================================
func (h *Handler) UpdateMemberLevel(c *gin.Context) {
	scope := mw.GetNodeScope(c, h.DB)
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	var req struct {
		Name          *string  `json:"name"`
		Color         *string  `json:"color"`
		Icon          *string  `json:"icon"`
		SortOrder     *int     `json:"sort_order"`
		MinDeposit30d *float64 `json:"min_deposit_30d"`
		Description   *string  `json:"description"`
		Status        *string  `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

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
	if req.MinDeposit30d != nil {
		updates["min_deposit_30d"] = *req.MinDeposit30d
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

	q := h.DB.Table("member_levels").Where("id = ?", id)
	if scope.IsNode {
		q = q.Where("agent_node_id = ?", scope.NodeID)
	}
	res := q.Updates(updates)
	if res.RowsAffected == 0 {
		fail(c, 404, "ไม่พบ level นี้หรือไม่มีสิทธิ์แก้ไข")
		return
	}
	var lvl memberLevel
	h.DB.First(&lvl, id)
	ok(c, lvl)
}

// =============================================================================
// DeleteMemberLevel — DELETE /api/v1/member-levels/:id
// ⭐ ต้องไม่มีสมาชิกอยู่ใน level นี้ (safety)
// =============================================================================
func (h *Handler) DeleteMemberLevel(c *gin.Context) {
	scope := mw.GetNodeScope(c, h.DB)
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	var cnt int64
	h.DB.Table("members").Where("level_id = ?", id).Count(&cnt)
	if cnt > 0 {
		fail(c, 400, "ไม่สามารถลบได้ — มีสมาชิก "+strconv.FormatInt(cnt, 10)+" คนอยู่ใน level นี้")
		return
	}

	q := "DELETE FROM member_levels WHERE id = ?"
	args := []interface{}{id}
	if scope.IsNode {
		q += " AND agent_node_id = ?"
		args = append(args, scope.NodeID)
	}
	res := h.DB.Exec(q, args...)
	if res.RowsAffected == 0 {
		fail(c, 404, "ไม่พบ level นี้หรือไม่มีสิทธิ์ลบ")
		return
	}
	ok(c, gin.H{"id": id, "deleted": true})
}

// =============================================================================
// ReorderMemberLevels — PUT /api/v1/member-levels/reorder
// =============================================================================
func (h *Handler) ReorderMemberLevels(c *gin.Context) {
	scope := mw.GetNodeScope(c, h.DB)
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
	tx := h.DB.Begin()
	for _, o := range req.Orders {
		if scope.IsNode {
			tx.Exec("UPDATE member_levels SET sort_order = ? WHERE id = ? AND agent_node_id = ?", o.SortOrder, o.ID, scope.NodeID)
		} else {
			tx.Exec("UPDATE member_levels SET sort_order = ? WHERE id = ? AND agent_node_id = ?", o.SortOrder, o.ID, scope.RootNodeID)
		}
	}
	tx.Commit()
	ok(c, gin.H{"updated": len(req.Orders)})
}

// =============================================================================
// OverrideMemberLevel — PUT /api/v1/members/:id/level
// ⭐ admin override: เปลี่ยน level + lock (cron daily จะไม่แก้ต่อ)
// ⭐ บันทึก history reason=admin_override
// =============================================================================
func (h *Handler) OverrideMemberLevel(c *gin.Context) {
	scope := mw.GetNodeScope(c, h.DB)
	memberID, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	var req struct {
		LevelID *int64 `json:"level_id"` // NULL = ตกระดับทั้งหมด (ไม่เข้า tier ใด)
		Note    string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// เช็ค member อยู่ใน scope ของ admin (ป้องกันข้าม node)
	type memRow struct {
		ID              int64
		LevelID         *int64
		Deposit30dCached float64 `gorm:"column:deposit_30d_cached"`
		AgentNodeID     int64
	}
	var mem memRow
	mq := h.DB.Table("members").Where("id = ?", memberID)
	if scope.IsNode {
		mq = mq.Where("agent_node_id = ?", scope.NodeID)
	}
	if err := mq.Select("id, level_id, deposit_30d_cached, agent_node_id").Take(&mem).Error; err != nil {
		fail(c, 404, "ไม่พบสมาชิกนี้หรือไม่มีสิทธิ์")
		return
	}

	// ถ้าระบุ level_id → เช็คว่า level นั้นอยู่ใน scope เดียวกัน
	if req.LevelID != nil {
		var ok64 int64
		h.DB.Table("member_levels").Where("id = ? AND agent_node_id = ?", *req.LevelID, mem.AgentNodeID).Count(&ok64)
		if ok64 == 0 {
			fail(c, 400, "level_id นี้ไม่อยู่ใน scope ของสมาชิก")
			return
		}
	}

	adminIDPtr := getAdminIDPtr(c)
	now := time.Now()

	// transaction: update member + insert history
	tx := h.DB.Begin()
	if err := tx.Exec(`
		UPDATE members
		SET level_id = ?, level_locked = 1, level_updated_at = ?, updated_at = ?
		WHERE id = ?
	`, req.LevelID, now, now, memberID).Error; err != nil {
		tx.Rollback()
		fail(c, 500, "update member ล้มเหลว: "+err.Error())
		return
	}
	hist := memberLevelHistory{
		MemberID:           memberID,
		FromLevelID:        mem.LevelID,
		ToLevelID:          req.LevelID,
		Reason:             "admin_override",
		Deposit30dSnapshot: mem.Deposit30dCached,
		ChangedByAdminID:   adminIDPtr,
		Note:               req.Note,
		CreatedAt:          now,
	}
	if err := tx.Create(&hist).Error; err != nil {
		tx.Rollback()
		fail(c, 500, "insert history ล้มเหลว: "+err.Error())
		return
	}
	tx.Commit()

	ok(c, gin.H{"member_id": memberID, "level_id": req.LevelID, "locked": true})
}

// =============================================================================
// UnlockMemberLevel — DELETE /api/v1/members/:id/level-lock
// ⭐ ยกเลิก lock — cron ครั้งถัดไปจะคำนวณใหม่จาก deposit 30d
// =============================================================================
func (h *Handler) UnlockMemberLevel(c *gin.Context) {
	scope := mw.GetNodeScope(c, h.DB)
	memberID, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	adminIDPtr := getAdminIDPtr(c)
	now := time.Now()

	// เช็ค + update ใน scope
	q := "UPDATE members SET level_locked = 0, updated_at = ? WHERE id = ? AND level_locked = 1"
	args := []interface{}{now, memberID}
	if scope.IsNode {
		q += " AND agent_node_id = ?"
		args = append(args, scope.NodeID)
	}
	res := h.DB.Exec(q, args...)
	if res.RowsAffected == 0 {
		fail(c, 404, "ไม่พบสมาชิก หรือไม่ได้ถูก lock อยู่")
		return
	}

	// log history (reason=admin_unlock)
	var curLv *int64
	var snap float64
	h.DB.Table("members").Where("id = ?", memberID).Select("level_id").Scan(&curLv)
	h.DB.Table("members").Where("id = ?", memberID).Select("deposit_30d_cached").Scan(&snap)
	h.DB.Create(&memberLevelHistory{
		MemberID:           memberID,
		FromLevelID:        curLv,
		ToLevelID:          curLv,
		Reason:             "admin_unlock",
		Deposit30dSnapshot: snap,
		ChangedByAdminID:   adminIDPtr,
		CreatedAt:          now,
	})

	ok(c, gin.H{"member_id": memberID, "locked": false})
}

// =============================================================================
// GetMemberLevelHistory — GET /api/v1/members/:id/level-history
// ⭐ ประวัติการเปลี่ยนระดับของสมาชิก (auto + admin)
// =============================================================================
func (h *Handler) GetMemberLevelHistory(c *gin.Context) {
	scope := mw.GetNodeScope(c, h.DB)
	memberID, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	// เช็ค member อยู่ใน scope
	mq := h.DB.Table("members").Where("id = ?", memberID)
	if scope.IsNode {
		mq = mq.Where("agent_node_id = ?", scope.NodeID)
	}
	var cnt int64
	mq.Count(&cnt)
	if cnt == 0 {
		fail(c, 404, "ไม่พบสมาชิก")
		return
	}

	var hist []memberLevelHistory
	h.DB.Table("member_level_history").
		Where("member_id = ?", memberID).
		Order("created_at DESC").
		Limit(100).
		Find(&hist)

	ok(c, hist)
}
