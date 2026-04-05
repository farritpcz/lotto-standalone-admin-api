// Package handler — node_portal_handler.go
// Portal สำหรับ Agent Node — login + ดูสายงาน + CRUD ลูกตรง + ดูกำไร
//
// ความสัมพันธ์:
//   - ใช้ middleware.NodeJWTAuth() ตรวจสอบ JWT จาก "node_token" cookie
//   - ใช้ model.AgentNode, AgentProfitTransaction จาก models.go
//   - frontend: admin-web /node/login + /node/portal
//
// กฎสิทธิ์:
//   1. เห็นทั้งสาย: ancestors (สายบน) + ตัวเอง + descendants (สายล่าง)
//   2. แก้ไขได้เฉพาะลูกตรง (parent_id = ตัวเอง)
//   3. หลาน/เหลน = read-only (แก้ไขไม่ได้)
//
// Endpoints:
//   POST /node/auth/login       → login ด้วย username/password
//   POST /node/auth/logout      → ลบ cookie
//   GET  /node/me               → ข้อมูลตัวเอง
//   GET  /node/tree             → tree สายที่เกี่ยวข้อง (ancestors + self + descendants)
//   GET  /node/children         → ลูกตรง (แก้ไขได้)
//   POST /node/children         → สร้างลูกตรง
//   PUT  /node/children/:id     → แก้ไขลูกตรง
//   DELETE /node/children/:id   → ลบลูกตรง
//   GET  /node/profits          → กำไร/ขาดทุนของตัวเอง + สายล่าง
package handler

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

// =============================================================================
// POST /node/auth/login — Node Login
//
// ดึง agent_nodes by username → เช็ค bcrypt password → สร้าง JWT
// ตั้ง httpOnly cookie "node_token"
// =============================================================================
func (h *Handler) NodeLogin(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "กรุณากรอก username และ password")
		return
	}

	// ดึง node จาก DB
	var node model.AgentNode
	if err := h.DB.Where("username = ? AND agent_id = 1", req.Username).First(&node).Error; err != nil {
		fail(c, 401, "ชื่อผู้ใช้หรือรหัสผ่านไม่ถูกต้อง")
		return
	}

	// เช็ค password (bcrypt)
	if err := bcrypt.CompareHashAndPassword([]byte(node.PasswordHash), []byte(req.Password)); err != nil {
		fail(c, 401, "ชื่อผู้ใช้หรือรหัสผ่านไม่ถูกต้อง")
		return
	}

	// เช็คสถานะ
	if node.Status != "active" {
		fail(c, 403, "บัญชีถูกระงับ — กรุณาติดต่อหัวสาย")
		return
	}

	// สร้าง JWT token
	token, err := mw.GenerateNodeToken(
		node.ID, node.AgentID, node.Username, node.Role,
		h.AdminJWTSecret, h.AdminJWTExpiryHours,
	)
	if err != nil {
		fail(c, 500, "สร้าง token ไม่สำเร็จ")
		return
	}

	// ตั้ง httpOnly cookie
	maxAge := h.AdminJWTExpiryHours * 3600
	mw.SetNodeTokenCookie(c, token, maxAge, mw.CookieConfig{
		Domain: h.CookieDomain,
		Secure: h.CookieSecure,
	})

	// ตั้ง CSRF cookie สำหรับ node (ใช้ชื่อแยก)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     "node_csrf_token",
		Value:    fmt.Sprintf("%d", time.Now().UnixNano()),
		Path:     "/",
		Domain:   h.CookieDomain,
		MaxAge:   86400,
		HttpOnly: false, // ⭐ frontend ต้องอ่านได้
		Secure:   h.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})

	ok(c, gin.H{
		"node": gin.H{
			"id":            node.ID,
			"name":          node.Name,
			"username":      node.Username,
			"role":          node.Role,
			"share_percent": node.SharePercent,
		},
		"token": token,
	})
}

// =============================================================================
// POST /node/auth/logout — ลบ node cookie
// =============================================================================
func (h *Handler) NodeLogout(c *gin.Context) {
	mw.ClearNodeTokenCookie(c, mw.CookieConfig{
		Domain: h.CookieDomain,
		Secure: h.CookieSecure,
	})
	// ลบ CSRF cookie ด้วย
	http.SetCookie(c.Writer, &http.Cookie{
		Name: "node_csrf_token", Value: "", Path: "/",
		Domain: h.CookieDomain, MaxAge: -1,
		Secure: h.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
	ok(c, gin.H{"message": "ออกจากระบบสำเร็จ"})
}

// =============================================================================
// GET /node/me — ดูข้อมูลตัวเอง
// =============================================================================
func (h *Handler) NodeGetMe(c *gin.Context) {
	nodeID := mw.GetNodeID(c)

	var node model.AgentNode
	if err := h.DB.First(&node, nodeID).Error; err != nil {
		fail(c, 404, "ไม่พบข้อมูล node")
		return
	}

	// นับ members + children
	h.DB.Model(&model.Member{}).Where("agent_node_id = ?", nodeID).Count(&node.MemberCount)
	h.DB.Model(&model.AgentNode{}).Where("parent_id = ?", nodeID).Count(&node.ChildCount)

	// ดึง parent info
	if node.ParentID != nil {
		var parent model.AgentNode
		if err := h.DB.First(&parent, *node.ParentID).Error; err == nil {
			node.Parent = &parent
		}
	}

	ok(c, node)
}

// =============================================================================
// GET /node/tree — ดู tree ที่เกี่ยวข้อง
//
// สร้าง tree เฉพาะ:
//   - ancestors: สายบนจากตัวเอง → root (read-only)
//   - self: ตัวเอง
//   - descendants: สายล่างทั้งหมด
//
// Response เพิ่ม field "editable" = true/false ในแต่ละ node
//   - ลูกตรง (parent_id = me) → editable = true
//   - ที่เหลือ → editable = false
// =============================================================================
func (h *Handler) NodeGetTree(c *gin.Context) {
	nodeID := mw.GetNodeID(c)
	agentID := mw.GetNodeAgentID(c)

	// ดึง node ปัจจุบัน
	var me model.AgentNode
	if err := h.DB.First(&me, nodeID).Error; err != nil {
		fail(c, 404, "ไม่พบข้อมูล node")
		return
	}

	// ดึง nodes ทั้งหมดของ agent
	var allNodes []model.AgentNode
	h.DB.Where("agent_id = ?", agentID).Order("depth ASC, id ASC").Find(&allNodes)

	// นับ member count + child count
	type countRow struct {
		ID    int64 `gorm:"column:id"`
		Count int64 `gorm:"column:cnt"`
	}
	var memberCounts []countRow
	h.DB.Raw(`SELECT agent_node_id as id, COUNT(*) as cnt FROM members WHERE agent_node_id IS NOT NULL AND agent_id = ? GROUP BY agent_node_id`, agentID).Scan(&memberCounts)
	mcMap := map[int64]int64{}
	for _, mc := range memberCounts {
		mcMap[mc.ID] = mc.Count
	}

	var childCounts []countRow
	h.DB.Raw(`SELECT parent_id as id, COUNT(*) as cnt FROM agent_nodes WHERE agent_id = ? AND parent_id IS NOT NULL GROUP BY parent_id`, agentID).Scan(&childCounts)
	ccMap := map[int64]int64{}
	for _, cc := range childCounts {
		ccMap[cc.ID] = cc.Count
	}

	// สร้าง map id → node
	nodeMap := map[int64]*model.AgentNode{}
	for i := range allNodes {
		allNodes[i].MemberCount = mcMap[allNodes[i].ID]
		allNodes[i].ChildCount = ccMap[allNodes[i].ID]
		nodeMap[allNodes[i].ID] = &allNodes[i]
	}

	// === หา descendants (สายล่าง): ใช้ path LIKE ===
	// เห็นแค่ตัวเอง + สายล่างทั้งหมด (ไม่เห็นสายบน)
	myPath := me.Path
	descendantIDs := map[int64]bool{}
	for _, n := range allNodes {
		if n.ID != nodeID && strings.HasPrefix(n.Path, myPath) {
			descendantIDs[n.ID] = true
		}
	}

	// === filter: ตัวเอง + descendants เท่านั้น ===
	type treeNode struct {
		model.AgentNode
		Editable bool       `json:"editable"` // ⭐ true = แก้ไขได้ (ลูกตรง)
		Children []treeNode `json:"children"`
	}

	type flatNode struct {
		model.AgentNode
		Editable bool `json:"editable"`
	}

	var relatedFlat []flatNode
	for _, n := range allNodes {
		isRelated := n.ID == nodeID || descendantIDs[n.ID]
		if !isRelated {
			continue
		}
		// ⭐ editable = true เฉพาะลูกตรง (parent_id = me)
		editable := n.ParentID != nil && *n.ParentID == nodeID
		relatedFlat = append(relatedFlat, flatNode{AgentNode: n, Editable: editable})
	}

	// Build tree จาก flat list
	flatMap := map[int64]*flatNode{}
	for i := range relatedFlat {
		flatMap[relatedFlat[i].ID] = &relatedFlat[i]
	}

	// Recursive build
	var buildTree func(id int64) treeNode
	buildTree = func(id int64) treeNode {
		fn := flatMap[id]
		tn := treeNode{
			AgentNode: fn.AgentNode,
			Editable:  fn.Editable,
			Children:  []treeNode{},
		}
		for _, n := range relatedFlat {
			if n.ParentID != nil && *n.ParentID == id {
				tn.Children = append(tn.Children, buildTree(n.ID))
			}
		}
		return tn
	}

	// Root = ตัวเอง (ไม่มีสายบน)
	var result []treeNode
	result = append(result, buildTree(nodeID))

	ok(c, gin.H{
		"tree":    result,
		"my_node": me,
	})
}

// =============================================================================
// GET /node/children — ดูลูกตรง (parent_id = me)
// =============================================================================
func (h *Handler) NodeListChildren(c *gin.Context) {
	nodeID := mw.GetNodeID(c)

	var children []model.AgentNode
	h.DB.Where("parent_id = ?", nodeID).Order("id ASC").Find(&children)

	// นับ members ของแต่ละลูก
	for i := range children {
		h.DB.Model(&model.Member{}).Where("agent_node_id = ?", children[i].ID).Count(&children[i].MemberCount)
		h.DB.Model(&model.AgentNode{}).Where("parent_id = ?", children[i].ID).Count(&children[i].ChildCount)
	}

	ok(c, children)
}

// =============================================================================
// POST /node/children — สร้างลูกตรงใหม่
//
// เหมือน CreateDownlineNode แต่ parent_id = ตัวเอง (บังคับ)
// =============================================================================
func (h *Handler) NodeCreateChild(c *gin.Context) {
	nodeID := mw.GetNodeID(c)

	var req struct {
		Name         string  `json:"name" binding:"required"`
		Username     string  `json:"username" binding:"required"`
		Password     string  `json:"password" binding:"required"`
		SharePercent float64 `json:"share_percent" binding:"required"`
		Phone        string  `json:"phone"`
		LineID       string  `json:"line_id"`
		Note         string  `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "ข้อมูลไม่ถูกต้อง: "+err.Error())
		return
	}

	// ดึง parent (ตัวเอง)
	var me model.AgentNode
	if err := h.DB.First(&me, nodeID).Error; err != nil {
		fail(c, 404, "ไม่พบข้อมูลตัวเอง")
		return
	}

	// Validate share_percent < ตัวเอง
	if req.SharePercent >= me.SharePercent {
		fail(c, 400, fmt.Sprintf("share_percent (%.2f) ต้องน้อยกว่าของคุณ (%.2f)", req.SharePercent, me.SharePercent))
		return
	}
	if req.SharePercent <= 0 {
		fail(c, 400, "share_percent ต้องมากกว่า 0")
		return
	}

	// Hash password
	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		fail(c, 500, "hash password ไม่สำเร็จ")
		return
	}

	// กำหนด role ถัดไป
	role := model.NextRole(me.Role)

	// สร้าง node
	child := model.AgentNode{
		AgentID:      me.AgentID,
		ParentID:     &nodeID,
		Role:         role,
		Name:         req.Name,
		Username:     req.Username,
		PasswordHash: string(hashed),
		Depth:        me.Depth + 1,
		Path:         me.Path, // temporary
		SharePercent: req.SharePercent,
		Phone:        req.Phone,
		LineID:       req.LineID,
		Note:         req.Note,
		Status:       "active",
	}

	if err := h.DB.Create(&child).Error; err != nil {
		if strings.Contains(err.Error(), "Duplicate") || strings.Contains(err.Error(), "uk_agent_node_username") {
			fail(c, 400, "username ซ้ำ — กรุณาเปลี่ยน")
			return
		}
		fail(c, 500, "สร้างลูกสายไม่สำเร็จ: "+err.Error())
		return
	}

	// อัพเดท path
	child.Path = me.Path + fmt.Sprintf("%d/", child.ID)
	h.DB.Model(&child).Update("path", child.Path)

	ok(c, child)
}

// =============================================================================
// PUT /node/children/:id — แก้ไขลูกตรง
//
// ⭐ กฎสำคัญ: ต้องเป็นลูกตรงเท่านั้น (parent_id = me)
// ถ้าเป็นหลาน/เหลน → 403 Forbidden
// =============================================================================
func (h *Handler) NodeUpdateChild(c *gin.Context) {
	nodeID := mw.GetNodeID(c)
	childID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, 400, "invalid id")
		return
	}

	// ดึง child node
	var child model.AgentNode
	if err := h.DB.First(&child, childID).Error; err != nil {
		fail(c, 404, "ไม่พบ node")
		return
	}

	// ⭐ เช็คว่าเป็นลูกตรง (parent_id = ตัวเอง)
	if child.ParentID == nil || *child.ParentID != nodeID {
		fail(c, 403, "สามารถแก้ไขได้เฉพาะลูกตรงของคุณเท่านั้น")
		return
	}

	var req struct {
		Name         *string  `json:"name"`
		SharePercent *float64 `json:"share_percent"`
		Phone        *string  `json:"phone"`
		LineID       *string  `json:"line_id"`
		Note         *string  `json:"note"`
		Status       *string  `json:"status"`
		Password     *string  `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// ดึง me เพื่อ validate share_percent
	var me model.AgentNode
	h.DB.First(&me, nodeID)

	// Validate share_percent
	if req.SharePercent != nil {
		if *req.SharePercent >= me.SharePercent {
			fail(c, 400, fmt.Sprintf("share_percent (%.2f) ต้องน้อยกว่าของคุณ (%.2f)", *req.SharePercent, me.SharePercent))
			return
		}
		if *req.SharePercent <= 0 {
			fail(c, 400, "share_percent ต้องมากกว่า 0")
			return
		}
		// ต้อง > ลูกของ child ทุกคน
		var maxGrandchildPercent float64
		h.DB.Model(&model.AgentNode{}).Where("parent_id = ?", childID).
			Select("COALESCE(MAX(share_percent), 0)").Row().Scan(&maxGrandchildPercent)
		if *req.SharePercent <= maxGrandchildPercent {
			fail(c, 400, fmt.Sprintf("share_percent (%.2f) ต้องมากกว่าลูกของเขา (%.2f)", *req.SharePercent, maxGrandchildPercent))
			return
		}
	}

	// Build updates
	updates := map[string]interface{}{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.SharePercent != nil {
		updates["share_percent"] = *req.SharePercent
	}
	if req.Phone != nil {
		updates["phone"] = *req.Phone
	}
	if req.LineID != nil {
		updates["line_id"] = *req.LineID
	}
	if req.Note != nil {
		updates["note"] = *req.Note
	}
	if req.Status != nil {
		if *req.Status != "active" && *req.Status != "suspended" {
			fail(c, 400, "status ต้องเป็น active หรือ suspended")
			return
		}
		updates["status"] = *req.Status
	}
	if req.Password != nil && *req.Password != "" {
		hashed, err := bcrypt.GenerateFromPassword([]byte(*req.Password), bcrypt.DefaultCost)
		if err != nil {
			fail(c, 500, "hash password ไม่สำเร็จ")
			return
		}
		updates["password_hash"] = string(hashed)
	}

	if len(updates) == 0 {
		fail(c, 400, "ไม่มีข้อมูลที่ต้องอัพเดท")
		return
	}
	updates["updated_at"] = time.Now()

	h.DB.Model(&model.AgentNode{}).Where("id = ?", childID).Updates(updates)
	h.DB.First(&child, childID)
	ok(c, child)
}

// =============================================================================
// DELETE /node/children/:id — ลบลูกตรง
//
// ⭐ ต้องเป็นลูกตรง + ไม่มี children + ไม่มี members
// =============================================================================
func (h *Handler) NodeDeleteChild(c *gin.Context) {
	nodeID := mw.GetNodeID(c)
	childID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, 400, "invalid id")
		return
	}

	var child model.AgentNode
	if err := h.DB.First(&child, childID).Error; err != nil {
		fail(c, 404, "ไม่พบ node")
		return
	}

	// ⭐ เช็คลูกตรง
	if child.ParentID == nil || *child.ParentID != nodeID {
		fail(c, 403, "สามารถลบได้เฉพาะลูกตรงของคุณเท่านั้น")
		return
	}

	// เช็ค children ของ child
	var grandchildCount int64
	h.DB.Model(&model.AgentNode{}).Where("parent_id = ?", childID).Count(&grandchildCount)
	if grandchildCount > 0 {
		fail(c, 400, fmt.Sprintf("ไม่สามารถลบได้ — มีลูกสาย %d คน", grandchildCount))
		return
	}

	// เช็ค members
	var memberCount int64
	h.DB.Model(&model.Member{}).Where("agent_node_id = ?", childID).Count(&memberCount)
	if memberCount > 0 {
		fail(c, 400, fmt.Sprintf("ไม่สามารถลบได้ — มีสมาชิก %d คน", memberCount))
		return
	}

	h.DB.Where("agent_node_id = ?", childID).Delete(&model.AgentNodeCommissionSetting{})
	h.DB.Delete(&model.AgentNode{}, childID)

	ok(c, gin.H{"deleted": true, "id": childID})
}

// =============================================================================
// GET /node/profits — กำไร/ขาดทุนของตัวเอง + สายล่าง
//
// Query params: date_from, date_to, page, per_page
// =============================================================================
func (h *Handler) NodeGetProfits(c *gin.Context) {
	nodeID := mw.GetNodeID(c)
	page, perPage := pageParams(c)

	// ดึง me
	var me model.AgentNode
	h.DB.First(&me, nodeID)

	// หา descendant node IDs (ทุก node ที่ path ขึ้นต้นด้วย path ของเรา)
	var descendantIDs []int64
	h.DB.Model(&model.AgentNode{}).
		Where("path LIKE ? AND id != ?", me.Path+"%", nodeID).
		Pluck("id", &descendantIDs)

	// รวม node IDs = ตัวเอง + descendants
	allNodeIDs := append([]int64{nodeID}, descendantIDs...)

	// ดึง profit transactions
	query := h.DB.Model(&model.AgentProfitTransaction{}).Where("agent_node_id IN ?", allNodeIDs)
	if from := c.Query("date_from"); from != "" {
		query = query.Where("created_at >= ?", from)
	}
	if to := c.Query("date_to"); to != "" {
		query = query.Where("created_at < DATE_ADD(?, INTERVAL 1 DAY)", to)
	}

	var total int64
	query.Count(&total)

	var transactions []model.AgentProfitTransaction
	query.Preload("AgentNode").
		Order("created_at DESC").
		Offset((page - 1) * perPage).
		Limit(perPage).
		Find(&transactions)

	// สรุปยอด
	var totalProfit float64
	var totalBets int64
	sumQuery := h.DB.Model(&model.AgentProfitTransaction{}).Where("agent_node_id IN ?", allNodeIDs)
	if from := c.Query("date_from"); from != "" {
		sumQuery = sumQuery.Where("created_at >= ?", from)
	}
	if to := c.Query("date_to"); to != "" {
		sumQuery = sumQuery.Where("created_at < DATE_ADD(?, INTERVAL 1 DAY)", to)
	}
	sumQuery.Select("COALESCE(SUM(profit_amount), 0)").Row().Scan(&totalProfit)
	sumQuery.Count(&totalBets)

	// สรุปกำไรเฉพาะตัวเอง
	var myProfit float64
	myQuery := h.DB.Model(&model.AgentProfitTransaction{}).Where("agent_node_id = ?", nodeID)
	if from := c.Query("date_from"); from != "" {
		myQuery = myQuery.Where("created_at >= ?", from)
	}
	if to := c.Query("date_to"); to != "" {
		myQuery = myQuery.Where("created_at < DATE_ADD(?, INTERVAL 1 DAY)", to)
	}
	myQuery.Select("COALESCE(SUM(profit_amount), 0)").Row().Scan(&myProfit)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"my_node":      me,
			"my_profit":    math.Round(myProfit*100) / 100,
			"total_profit": math.Round(totalProfit*100) / 100,
			"total_bets":   totalBets,
			"transactions": gin.H{
				"items":    transactions,
				"total":    total,
				"page":     page,
				"per_page": perPage,
			},
		},
	})
}
