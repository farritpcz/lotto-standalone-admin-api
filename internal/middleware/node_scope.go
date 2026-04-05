// Package middleware — node_scope.go
// Helper สำหรับ scope ข้อมูลตามสายงาน (Downline Scoping)
//
// ทุก agent_node ที่ login เข้าหลังบ้าน = เว็บหวย 1 เว็บ
// ต้องเห็นเฉพาะข้อมูลภายใต้สายของตัวเอง
// Admin (role != "node") เห็นทุกอย่างเหมือนเดิม
//
// การใช้งานใน handler:
//
//	scope := mw.GetNodeScope(c, h.DB)
//	if scope.IsNode {
//	    query = query.Where("agent_node_id IN ?", scope.NodeIDs)   // สำหรับ members
//	    query = query.Where("member_id IN ?", scope.MemberIDs)     // สำหรับ bets/transactions
//	}
//
// หรือใช้ helper methods:
//
//	query = scope.ScopeByNodeID(query, "agent_node_id")  // filter ด้วย node IDs
//	query = scope.ScopeByMemberID(query, "member_id")    // filter ด้วย member IDs
package middleware

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// NodeScope เก็บข้อมูล scope สำหรับ node user
// ใช้กำหนดว่า handler ควรเห็นข้อมูลอะไรบ้าง
type NodeScope struct {
	IsNode    bool    // true = login จาก agent_nodes (เว็บสายงาน)
	NodeID    int64   // agent_nodes.id ของ node ที่ login
	NodeIDs   []int64 // ตัวเอง + descendants ทั้งหมด (สำหรับ filter agent_node_id)
	MemberIDs []int64 // members.id ที่อยู่ใต้ nodeIDs เหล่านี้ (สำหรับ filter member_id)
}

// GetNodeScope ดึง scope จาก context + คำนวณ NodeIDs/MemberIDs
//
// - admin (role != "node") → IsNode=false, เห็นทุกอย่าง
// - node (role == "node") → IsNode=true, NodeIDs = ตัวเอง + descendants, MemberIDs = members ใต้สาย
//
// ⭐ Cache ใน context เพื่อไม่ query ซ้ำถ้าเรียกหลายครั้งใน request เดียวกัน
func GetNodeScope(c *gin.Context, db *gorm.DB) *NodeScope {
	// เช็ค cache ก่อน
	if cached, exists := c.Get("_node_scope"); exists {
		return cached.(*NodeScope)
	}

	role, _ := c.Get("admin_role")
	roleStr, _ := role.(string)

	// Admin → ไม่ scope
	if roleStr != "node" {
		scope := &NodeScope{IsNode: false}
		c.Set("_node_scope", scope)
		return scope
	}

	// Node → คำนวณ scope
	nodeID := GetAdminID(c) // admin_id = node.ID เมื่อ role="node"

	// หา node ปัจจุบันเพื่อดึง path
	type nodeInfo struct {
		ID   int64  `gorm:"column:id"`
		Path string `gorm:"column:path"`
	}
	var me nodeInfo
	db.Raw("SELECT id, path FROM agent_nodes WHERE id = ?", nodeID).Scan(&me)

	// หา descendants: ทุก node ที่ path ขึ้นต้นด้วย path ของเรา (รวมตัวเอง)
	var nodeIDs []int64
	db.Table("agent_nodes").Where("path LIKE ?", me.Path+"%").Pluck("id", &nodeIDs)

	// ถ้าไม่เจอ descendants ให้ใส่ตัวเองอย่างน้อย
	if len(nodeIDs) == 0 {
		nodeIDs = []int64{nodeID}
	}

	// หา member IDs ใต้ nodeIDs เหล่านี้
	var memberIDs []int64
	db.Table("members").Where("agent_node_id IN ?", nodeIDs).Pluck("id", &memberIDs)

	scope := &NodeScope{
		IsNode:    true,
		NodeID:    nodeID,
		NodeIDs:   nodeIDs,
		MemberIDs: memberIDs,
	}
	c.Set("_node_scope", scope)
	return scope
}

// ScopeByNodeID เพิ่ม WHERE filter ด้วย node IDs
// ใช้สำหรับ query ที่มี column agent_node_id
// admin → ไม่เพิ่มอะไร (เห็นทุกอย่าง)
// node → WHERE column IN (nodeIDs...)
func (s *NodeScope) ScopeByNodeID(query *gorm.DB, column string) *gorm.DB {
	if !s.IsNode {
		return query
	}
	return query.Where(column+" IN ?", s.NodeIDs)
}

// ScopeByMemberID เพิ่ม WHERE filter ด้วย member IDs
// ใช้สำหรับ query ที่มี column member_id
// admin → ไม่เพิ่มอะไร
// node → WHERE column IN (memberIDs...)
// ⭐ ถ้าไม่มี members เลย → WHERE column IN (0) เพื่อไม่ให้ return ทั้งหมด
func (s *NodeScope) ScopeByMemberID(query *gorm.DB, column string) *gorm.DB {
	if !s.IsNode {
		return query
	}
	if len(s.MemberIDs) == 0 {
		return query.Where(column+" IN ?", []int64{0}) // ไม่มี members → return empty
	}
	return query.Where(column+" IN ?", s.MemberIDs)
}

// HasMember เช็คว่า member_id อยู่ในสายหรือไม่
// admin → true เสมอ
// node → เช็คว่า memberID อยู่ใน MemberIDs
func (s *NodeScope) HasMember(memberID int64) bool {
	if !s.IsNode {
		return true
	}
	for _, id := range s.MemberIDs {
		if id == memberID {
			return true
		}
	}
	return false
}

// MemberIDsForSQL คืน member IDs สำหรับใช้ใน raw SQL
// admin → nil (ไม่ต้อง filter)
// node → []int64{...}
func (s *NodeScope) MemberIDsForSQL() []int64 {
	if !s.IsNode {
		return nil
	}
	if len(s.MemberIDs) == 0 {
		return []int64{0}
	}
	return s.MemberIDs
}

// ScopeSettings เพิ่ม WHERE filter สำหรับตาราง settings (agent_node_id)
// admin → เห็นทุกอย่าง (ไม่ filter)
// node → เห็นเฉพาะของตัวเอง (agent_node_id = nodeID) + ของระบบ (agent_node_id IS NULL)
func (s *NodeScope) ScopeSettings(query *gorm.DB) *gorm.DB {
	if !s.IsNode {
		return query
	}
	return query.Where("agent_node_id IS NULL OR agent_node_id = ?", s.NodeID)
}

// ScopeSettingsOwn เพิ่ม WHERE filter เฉพาะของ node ตัวเอง (ไม่รวมของระบบ)
// ใช้สำหรับ CREATE/UPDATE/DELETE — ให้ทำได้เฉพาะของตัวเอง
func (s *NodeScope) ScopeSettingsOwn(query *gorm.DB) *gorm.DB {
	if !s.IsNode {
		return query
	}
	return query.Where("agent_node_id = ?", s.NodeID)
}

// SettingNodeID คืน *int64 สำหรับ INSERT — admin → nil, node → &nodeID
func (s *NodeScope) SettingNodeID() *int64 {
	if !s.IsNode {
		return nil
	}
	nid := s.NodeID
	return &nid
}
