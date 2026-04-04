// Package handler — notifications.go
// ระบบแจ้งเตือน Telegram webhook สำหรับ admin-api (#5)
//
// ⭐ รองรับหลายกลุ่ม (multi-group):
//   - แต่ละกลุ่มมี Bot Token + Chat ID + จุดแจ้งเตือนแยกกัน
//   - ตัวอย่าง: กลุ่ม "แอดมิน" แจ้งฝาก/ถอน, กลุ่ม "เจ้าของ" แจ้งยอดสูง
//   - เก็บ config เป็น JSON array ใน settings table (key = "notify_groups")
//
// ความสัมพันธ์:
// - เก็บ config ใน settings table (key = "notify_groups")
// - ใช้ร่วมกับ deposit/withdraw handlers (เรียก SendNotification)
// - admin-web (#6) ใช้ตั้งค่า + ทดสอบ
//
// Routes:
//   GET    /api/v1/notifications/groups          → ดึงกลุ่มแจ้งเตือนทั้งหมด
//   PUT    /api/v1/notifications/groups          → บันทึกกลุ่มทั้งหมด (replace all)
//   POST   /api/v1/notifications/test/:groupId   → ทดสอบส่ง notification ไปกลุ่มเดียว
package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// =============================================================================
// Types — Multi-Group Notification Config
// =============================================================================

// notifyGroup กลุ่มแจ้งเตือน 1 กลุ่ม
// แต่ละกลุ่มมี Bot Token + Chat ID + จุดแจ้งเตือนของตัวเอง
type notifyGroup struct {
	ID               string  `json:"id"`                 // unique ID เช่น "grp_abc123"
	Name             string  `json:"name"`               // ชื่อกลุ่ม เช่น "กลุ่มแอดมิน"
	BotToken         string  `json:"bot_token"`          // Telegram Bot Token
	ChatID           string  `json:"chat_id"`            // Telegram Chat ID (group/personal)
	Active           bool    `json:"active"`             // เปิด/ปิดกลุ่มนี้
	OnDeposit        bool    `json:"on_deposit"`         // แจ้งเมื่อมีคำขอฝากเงิน
	OnWithdraw       bool    `json:"on_withdraw"`        // แจ้งเมื่อมีคำขอถอนเงิน
	OnDepositApprove bool    `json:"on_deposit_approve"` // แจ้งเมื่ออนุมัติฝาก
	OnWithdrawApprove bool   `json:"on_withdraw_approve"` // แจ้งเมื่ออนุมัติถอน
	OnNewMember      bool    `json:"on_new_member"`      // แจ้งเมื่อสมัครสมาชิกใหม่
	OnResult         bool    `json:"on_result"`          // แจ้งเมื่อกรอกผลหวย
	OnLargeWin       bool    `json:"on_large_win"`       // แจ้งเมื่อถูกรางวัลใหญ่
	OnLargeBet       bool    `json:"on_large_bet"`       // แจ้งเมื่อเดิมพันยอดสูง
	OnLogin          bool    `json:"on_login"`           // แจ้งเมื่อแอดมิน login
	LargeWinMin      float64 `json:"large_win_min"`      // ยอดถูกรางวัลขั้นต่ำที่แจ้ง (บาท)
	LargeBetMin      float64 `json:"large_bet_min"`      // ยอดเดิมพันขั้นต่ำที่แจ้ง (บาท)
}

// =============================================================================
// DB key สำหรับเก็บ config
// =============================================================================
const notifyGroupsKey = "notify_groups"

// =============================================================================
// loadNotifyGroups — helper โหลดกลุ่มทั้งหมดจาก DB
// =============================================================================
func (h *Handler) loadNotifyGroups() []notifyGroup {
	var raw string
	h.DB.Table("settings").Select("value").Where("`key` = ?", notifyGroupsKey).Row().Scan(&raw)
	if raw == "" {
		return []notifyGroup{}
	}
	var groups []notifyGroup
	json.Unmarshal([]byte(raw), &groups)
	return groups
}

// =============================================================================
// saveNotifyGroups — helper บันทึกกลุ่มทั้งหมดลง DB
// =============================================================================
func (h *Handler) saveNotifyGroups(groups []notifyGroup) error {
	raw, err := json.Marshal(groups)
	if err != nil {
		return err
	}
	return h.DB.Exec(
		"INSERT INTO settings (`key`, value, description, updated_at) VALUES (?, ?, ?, ?) ON DUPLICATE KEY UPDATE value = VALUES(value), updated_at = VALUES(updated_at)",
		notifyGroupsKey, string(raw), "Telegram notification groups (multi-group)", time.Now(),
	).Error
}

// =============================================================================
// GetNotificationConfig — GET /api/v1/notifications/config
// ⭐ backward-compatible: ถ้ามี notify_groups ให้ส่ง groups กลับ
//    ถ้ามีแค่ notify_config (format เก่า) ให้แปลงเป็น group 1 กลุ่ม
// =============================================================================
func (h *Handler) GetNotificationConfig(c *gin.Context) {
	groups := h.loadNotifyGroups()

	// ⭐ ถ้ายังไม่มี groups → ลองอ่าน config เก่า (single) แปลงเป็น group
	if len(groups) == 0 {
		var oldRaw string
		h.DB.Table("settings").Select("value").Where("`key` = ?", "notify_config").Row().Scan(&oldRaw)
		if oldRaw != "" {
			// แปลง format เก่า → group เดียว
			var old struct {
				Enabled     bool    `json:"enabled"`
				BotToken    string  `json:"bot_token"`
				ChatID      string  `json:"chat_id"`
				OnDeposit   bool    `json:"on_deposit"`
				OnWithdraw  bool    `json:"on_withdraw"`
				OnNewMember bool    `json:"on_new_member"`
				OnLargeWin  bool    `json:"on_large_win"`
				LargeWinMin float64 `json:"large_win_min"`
			}
			json.Unmarshal([]byte(oldRaw), &old)
			if old.BotToken != "" || old.ChatID != "" {
				groups = []notifyGroup{{
					ID: "migrated", Name: "กลุ่มหลัก",
					BotToken: old.BotToken, ChatID: old.ChatID,
					Active: old.Enabled, OnDeposit: old.OnDeposit,
					OnWithdraw: old.OnWithdraw, OnNewMember: old.OnNewMember,
					OnLargeWin: old.OnLargeWin, LargeWinMin: old.LargeWinMin,
				}}
			}
		}
	}

	ok(c, groups)
}

// =============================================================================
// UpdateNotificationConfig — PUT /api/v1/notifications/config
// รับ array ของ groups → บันทึกทั้งหมด (replace all)
// =============================================================================
func (h *Handler) UpdateNotificationConfig(c *gin.Context) {
	var groups []notifyGroup
	if err := c.ShouldBindJSON(&groups); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// ⭐ validate: แต่ละ group ต้องมี id + name
	for i := range groups {
		if groups[i].ID == "" {
			fail(c, 400, fmt.Sprintf("กลุ่มที่ %d ไม่มี id", i+1))
			return
		}
		if groups[i].Name == "" {
			fail(c, 400, fmt.Sprintf("กลุ่มที่ %d ไม่มีชื่อ", i+1))
			return
		}
	}

	if err := h.saveNotifyGroups(groups); err != nil {
		fail(c, 500, "บันทึกไม่สำเร็จ: "+err.Error())
		return
	}

	ok(c, groups)
}

// =============================================================================
// TestNotification — POST /api/v1/notifications/test
// ⭐ รับ group_id ใน body → ทดสอบส่งไปกลุ่มนั้น
// =============================================================================
func (h *Handler) TestNotification(c *gin.Context) {
	var req struct {
		GroupID string `json:"group_id"`
	}
	c.ShouldBindJSON(&req)

	groups := h.loadNotifyGroups()

	// ⭐ ถ้าไม่ส่ง group_id → ส่งไปกลุ่มแรกที่มี token + chat_id
	var target *notifyGroup
	for i := range groups {
		if req.GroupID != "" && groups[i].ID == req.GroupID {
			target = &groups[i]
			break
		}
		if req.GroupID == "" && groups[i].BotToken != "" && groups[i].ChatID != "" {
			target = &groups[i]
			break
		}
	}

	if target == nil {
		fail(c, 400, "ไม่พบกลุ่มที่จะทดสอบ — กรุณากรอก Bot Token + Chat ID ก่อน")
		return
	}

	if target.BotToken == "" || target.ChatID == "" {
		fail(c, 400, "กลุ่ม \""+target.Name+"\" ยังไม่ได้กรอก Bot Token หรือ Chat ID")
		return
	}

	// ⭐ ส่งข้อความทดสอบ
	msg := fmt.Sprintf(
		"🔔 ทดสอบ Notification\n\nกลุ่ม: %s\nระบบแจ้งเตือน Telegram ทำงานปกติ!\nเวลา: %s",
		target.Name, time.Now().Format("2006-01-02 15:04:05"),
	)
	err := sendTelegramMessage(target.BotToken, target.ChatID, msg)
	if err != nil {
		fail(c, 500, "ส่งไม่สำเร็จ: "+err.Error())
		return
	}

	ok(c, gin.H{"sent": true, "group_id": target.ID, "group_name": target.Name})
}

// =============================================================================
// SendNotification — ฟังก์ชันส่ง notification (เรียกจาก handler อื่น)
// ⭐ multi-group: วนทุกกลุ่มที่ active + เปิด event type นี้ → ส่งทุกกลุ่ม
// ⭐ ใช้ goroutine เพื่อไม่ block main request
// =============================================================================
func (h *Handler) SendNotification(eventType string, message string) {
	go func() {
		groups := h.loadNotifyGroups()
		if len(groups) == 0 {
			return
		}

		for _, g := range groups {
			// ⭐ ข้ามกลุ่มที่ปิด / ไม่มี token
			if !g.Active || g.BotToken == "" || g.ChatID == "" {
				continue
			}

			// ⭐ ตรวจว่ากลุ่มนี้เปิด event type นี้หรือไม่
			shouldSend := false
			switch eventType {
			case "deposit":
				shouldSend = g.OnDeposit
			case "withdraw":
				shouldSend = g.OnWithdraw
			case "deposit_approve":
				shouldSend = g.OnDepositApprove
			case "withdraw_approve":
				shouldSend = g.OnWithdrawApprove
			case "new_member":
				shouldSend = g.OnNewMember
			case "result":
				shouldSend = g.OnResult
			case "large_win":
				shouldSend = g.OnLargeWin
			case "large_bet":
				shouldSend = g.OnLargeBet
			case "login":
				shouldSend = g.OnLogin
			}

			if !shouldSend {
				continue
			}

			// ⭐ ส่งข้อความ — ใส่ชื่อกลุ่มใน prefix
			fullMsg := fmt.Sprintf("[%s]\n%s", g.Name, message)
			if err := sendTelegramMessage(g.BotToken, g.ChatID, fullMsg); err != nil {
				log.Printf("⚠️ Telegram notification failed [%s → %s]: %v", eventType, g.Name, err)
			}
		}
	}()
}

// =============================================================================
// sendTelegramMessage — helper ส่งข้อความผ่าน Telegram Bot API
// ⭐ ใช้ HTTP POST ไปที่ https://api.telegram.org/bot<token>/sendMessage
// =============================================================================
func sendTelegramMessage(botToken, chatID, text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)

	// สร้าง request body เป็น JSON
	body := fmt.Sprintf(`{"chat_id":"%s","text":"%s","parse_mode":"HTML"}`, chatID, escapeJSON(text))
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// ⭐ ตรวจ response status
	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Telegram API error %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// escapeJSON escape special characters สำหรับ JSON string
func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}
