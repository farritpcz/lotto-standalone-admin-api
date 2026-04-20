// Package handler — bans admin handlers.
// Split from stubs.go on 2026-04-20.
package handler

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

// =============================================================================
// Number Bans
// =============================================================================

func (h *Handler) ListBans(c *gin.Context) {
	page, perPage := pageParams(c)
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope
	var bans []model.NumberBan
	var total int64
	query := h.DB.Model(&model.NumberBan{}).Where("status = ?", "active")
	// ⭐ node เห็นเฉพาะเลขอั้นของตัวเอง (ไม่เห็นของคนอื่น)
	if scope.IsNode {
		query = query.Where("agent_node_id = ?", scope.NodeID)
	}
	if lt := c.Query("lottery_type_id"); lt != "" {
		query = query.Where("lottery_type_id = ?", lt)
	}
	query.Count(&total)
	query.Order("created_at DESC").Offset((page - 1) * perPage).Limit(perPage).Find(&bans)
	paginated(c, bans, total, page, perPage)
}

func (h *Handler) CreateBan(c *gin.Context) {
	// ⭐ รับทั้ง bet_type_id (int) หรือ bet_type_id (string code เช่น "3TOP")
	var req struct {
		LotteryTypeID int64       `json:"lottery_type_id" binding:"required"`
		BetTypeID     interface{} `json:"bet_type_id"` // int หรือ string code
		Number        string      `json:"number" binding:"required"`
		BanType       string      `json:"ban_type"`
		ReducedRate   float64     `json:"reduced_rate"`
		MaxAmount     float64     `json:"max_amount"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// Resolve bet_type_id — ถ้าเป็น string code ให้หา ID จาก DB
	var betTypeID int64
	switch v := req.BetTypeID.(type) {
	case float64:
		betTypeID = int64(v)
	case string:
		// หา bet_type ID จาก code เช่น "3TOP" → id
		h.DB.Table("bet_types").Select("id").Where("code = ?", v).Scan(&betTypeID)
		if betTypeID == 0 {
			fail(c, 400, "ไม่พบประเภทเดิมพัน: "+v)
			return
		}
	default:
		fail(c, 400, "bet_type_id ต้องเป็นตัวเลขหรือ code")
		return
	}

	// ⭐ node user: ตั้ง agent_node_id ให้เลขอั้นเป็นของ node ตัวเอง
	scope := mw.GetNodeScope(c, h.DB)
	var banNodeID *int64
	if scope.IsNode {
		nid := scope.NodeID
		banNodeID = &nid
	}

	ban := model.NumberBan{
		LotteryTypeID: req.LotteryTypeID,
		BetTypeID:     betTypeID,
		AgentNodeID:   banNodeID, // ⭐ NULL=ทั้งระบบ (admin), มีค่า=เฉพาะ node
		Number:        req.Number,
		BanType:       req.BanType,
		ReducedRate:   req.ReducedRate,
		MaxAmount:     req.MaxAmount,
		Status:        "active",
		CreatedAt:     time.Now(),
	}
	if ban.BanType == "" {
		ban.BanType = "full_ban"
	}
	if err := h.DB.Create(&ban).Error; err != nil {
		fail(c, 500, "failed to create ban")
		return
	}
	ok(c, ban)
}

func (h *Handler) DeleteBan(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	scope := mw.GetNodeScope(c, h.DB) // ⭐ scope
	// ⭐ node user: ลบได้เฉพาะเลขอั้นของ node ตัวเอง (ห้ามลบของ admin/ระบบ)
	if scope.IsNode {
		var ban model.NumberBan
		h.DB.First(&ban, id)
		if ban.AgentNodeID == nil || !func() bool {
			for _, nid := range scope.NodeIDs {
				if nid == *ban.AgentNodeID {
					return true
				}
			}
			return false
		}() {
			fail(c, 403, "ไม่สามารถลบเลขอั้นของระบบได้")
			return
		}
	}
	h.DB.Model(&model.NumberBan{}).Where("id = ?", id).Update("status", "inactive")
	ok(c, gin.H{"id": id, "status": "inactive"})
}
