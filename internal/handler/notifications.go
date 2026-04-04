// Package handler — notifications.go
// ระบบแจ้งเตือน Telegram webhook สำหรับ admin-api (#5)
//
// ⭐ ฟีเจอร์:
// - ตั้งค่า Telegram Bot Token + Chat ID
// - เลือกประเภทการแจ้งเตือน: ฝาก/ถอน/สมาชิกใหม่/ยอดเสีย
// - ทดสอบส่ง notification
// - ส่งจริงเมื่อเกิด event (เรียกจาก handler อื่น)
//
// ความสัมพันธ์:
// - เก็บ config ใน settings table (key prefix = "notify_")
// - ใช้ร่วมกับ deposit/withdraw handlers (เรียก SendNotification)
// - admin-web (#6) ใช้ตั้งค่า + ทดสอบ
//
// Routes:
//   GET    /api/v1/notifications/config    → ดึงการตั้งค่า notification
//   PUT    /api/v1/notifications/config    → บันทึกการตั้งค่า
//   POST   /api/v1/notifications/test      → ทดสอบส่ง notification
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
// Types — Notification Config
// =============================================================================

// notifyConfig โครงสร้าง JSON เก็บใน settings.value
type notifyConfig struct {
	Enabled       bool   `json:"enabled"`                  // เปิด/ปิด notification ทั้งหมด
	BotToken      string `json:"bot_token"`                // Telegram Bot Token
	ChatID        string `json:"chat_id"`                  // Telegram Chat ID (group/personal)
	OnDeposit     bool   `json:"on_deposit"`               // แจ้งเมื่อมีคำขอฝากเงิน
	OnWithdraw    bool   `json:"on_withdraw"`              // แจ้งเมื่อมีคำขอถอนเงิน
	OnNewMember   bool   `json:"on_new_member"`            // แจ้งเมื่อสมัครสมาชิกใหม่
	OnLargeWin    bool   `json:"on_large_win"`             // แจ้งเมื่อถูกรางวัลใหญ่
	LargeWinMin   float64 `json:"large_win_min"`           // ยอดถูกรางวัลขั้นต่ำที่แจ้ง (บาท)
}

// =============================================================================
// GetNotificationConfig — GET /api/v1/notifications/config
// ดึงการตั้งค่า notification ปัจจุบัน
// =============================================================================
func (h *Handler) GetNotificationConfig(c *gin.Context) {
	var raw string
	h.DB.Table("settings").Select("value").Where("`key` = ?", "notify_config").Row().Scan(&raw)

	// ⭐ ถ้ายังไม่มีการตั้งค่า ให้ส่ง default กลับ
	cfg := notifyConfig{
		Enabled:     false,
		OnDeposit:   true,
		OnWithdraw:  true,
		OnNewMember: true,
		OnLargeWin:  false,
		LargeWinMin: 10000,
	}

	if raw != "" {
		json.Unmarshal([]byte(raw), &cfg)
	}

	ok(c, cfg)
}

// =============================================================================
// UpdateNotificationConfig — PUT /api/v1/notifications/config
// บันทึกการตั้งค่า notification
// =============================================================================
func (h *Handler) UpdateNotificationConfig(c *gin.Context) {
	var cfg notifyConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		fail(c, 400, err.Error())
		return
	}

	// ⭐ serialize เป็น JSON เก็บใน settings table
	raw, _ := json.Marshal(cfg)

	h.DB.Exec(
		"INSERT INTO settings (`key`, value, description, updated_at) VALUES (?, ?, ?, ?) ON DUPLICATE KEY UPDATE value = VALUES(value), updated_at = VALUES(updated_at)",
		"notify_config", string(raw), "Telegram notification settings", time.Now(),
	)

	ok(c, cfg)
}

// =============================================================================
// TestNotification — POST /api/v1/notifications/test
// ส่งข้อความทดสอบไปยัง Telegram
// =============================================================================
func (h *Handler) TestNotification(c *gin.Context) {
	// ดึง config จาก DB
	var raw string
	h.DB.Table("settings").Select("value").Where("`key` = ?", "notify_config").Row().Scan(&raw)
	if raw == "" {
		fail(c, 400, "ยังไม่ได้ตั้งค่า notification — กรุณาบันทึก Bot Token + Chat ID ก่อน")
		return
	}

	var cfg notifyConfig
	json.Unmarshal([]byte(raw), &cfg)

	if cfg.BotToken == "" || cfg.ChatID == "" {
		fail(c, 400, "กรุณากรอก Bot Token และ Chat ID")
		return
	}

	// ⭐ ส่งข้อความทดสอบ
	msg := "🔔 ทดสอบ Notification\n\nระบบแจ้งเตือน Telegram ทำงานปกติ!\nเวลา: " + time.Now().Format("2006-01-02 15:04:05")
	err := sendTelegramMessage(cfg.BotToken, cfg.ChatID, msg)
	if err != nil {
		fail(c, 500, "ส่ง notification ไม่สำเร็จ: "+err.Error())
		return
	}

	ok(c, gin.H{"sent": true, "message": "ส่งข้อความทดสอบสำเร็จ"})
}

// =============================================================================
// SendNotification — ฟังก์ชันส่ง notification (เรียกจาก handler อื่น)
// ⭐ ใช้ goroutine เพื่อไม่ block main request
// =============================================================================
func (h *Handler) SendNotification(eventType string, message string) {
	go func() {
		// ดึง config จาก DB
		var raw string
		h.DB.Table("settings").Select("value").Where("`key` = ?", "notify_config").Row().Scan(&raw)
		if raw == "" {
			return // ยังไม่ได้ตั้งค่า
		}

		var cfg notifyConfig
		json.Unmarshal([]byte(raw), &cfg)

		// ⭐ ตรวจว่าเปิด notification + เปิด event type นี้หรือไม่
		if !cfg.Enabled || cfg.BotToken == "" || cfg.ChatID == "" {
			return
		}

		switch eventType {
		case "deposit":
			if !cfg.OnDeposit {
				return
			}
		case "withdraw":
			if !cfg.OnWithdraw {
				return
			}
		case "new_member":
			if !cfg.OnNewMember {
				return
			}
		case "large_win":
			if !cfg.OnLargeWin {
				return
			}
		}

		// ส่งข้อความจริง
		if err := sendTelegramMessage(cfg.BotToken, cfg.ChatID, message); err != nil {
			log.Printf("⚠️ Telegram notification failed [%s]: %v", eventType, err)
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
