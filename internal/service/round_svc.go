// =============================================================================
// Package service — round_svc.go (core + simple ops)
//
// # Round Service — ศูนย์กลางจัดการรอบหวยทุก operation
//
// ⭐ หลักการ: ทุกการเปลี่ยนแปลงของรอบหวยต้องผ่าน service นี้
//   - Auto cron jobs → เรียก RoundService
//   - Admin handlers → เรียก RoundService
//   - Auto-result jobs → เรียก RoundService
//
// ไฟล์ที่เกี่ยวข้อง:
//   - round_svc.go          — struct, types, simple ops (create/open/close/batch)
//   - round_svc_submit.go   — SubmitResult + settle bets + payout
//   - round_svc_void.go     — VoidRound + refund + reverse wins
//
// ความสัมพันธ์:
//   - ใช้ lotto-core payout.SettleRound() สำหรับคำนวณแพ้ชนะ
//   - ใช้ model.* สำหรับ DB operations
//   - ถูกเรียกโดย: handler/stubs.go, job/round_lifecycle.go, job/auto_result.go
//
// =============================================================================
package service

import (
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"

	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

// =============================================================================
// RoundService — main struct
// =============================================================================

// RoundService จัดการรอบหวยทุก operation
type RoundService struct {
	db *gorm.DB
}

// NewRoundService สร้าง RoundService instance
func NewRoundService(db *gorm.DB) *RoundService {
	return &RoundService{db: db}
}

// DB คืน gorm.DB สำหรับให้ cron jobs ใช้ (เช่น auto_result)
func (s *RoundService) DB() *gorm.DB {
	return s.db
}

// =============================================================================
// Result types — return values สำหรับแต่ละ operation
// =============================================================================

// SettleResult ผลลัพธ์จากการกรอกผล + settle bets
type SettleResult struct {
	RoundID     int64   `json:"round_id"`
	TotalBets   int     `json:"total_bets"`       // จำนวน bet ทั้งหมด
	WinnerCount int     `json:"winner_count"`     // จำนวนคนถูก
	TotalBetAmt float64 `json:"total_bet_amount"` // ยอดแทงรวม
	TotalWinAmt float64 `json:"total_win_amount"` // ยอดจ่ายรวม
	Profit      float64 `json:"profit"`           // กำไร (bet - win)
}

// VoidResult ผลลัพธ์จากการยกเลิกรอบ
type VoidResult struct {
	RoundID      int64   `json:"round_id"`
	RefundedBets int     `json:"refunded_bets"`   // จำนวน bet ที่คืนเงิน
	RefundedAmt  float64 `json:"refunded_amount"` // ยอดเงินคืนรวม
	ReversedWins int     `json:"reversed_wins"`   // จำนวนรางวัลที่หักคืน
	ReversedAmt  float64 `json:"reversed_amount"` // ยอดรางวัลที่หักคืน
}

// =============================================================================
// CreateRound — สร้างรอบหวยใหม่
// =============================================================================

// CreateRound สร้างรอบหวยใหม่ในสถานะ upcoming (ใช้ทั้ง auto cron + manual admin)
// ⚠️ จะ fail ถ้า round_number ซ้ำ (UNIQUE constraint)
func (s *RoundService) CreateRound(lotteryTypeID int64, roundNumber string, roundDate, openTime, closeTime time.Time) (*model.LotteryRound, error) {
	round := model.LotteryRound{
		LotteryTypeID: lotteryTypeID,
		RoundNumber:   roundNumber,
		RoundDate:     roundDate,
		OpenTime:      openTime,
		CloseTime:     closeTime,
		Status:        "upcoming",
	}

	if err := s.db.Create(&round).Error; err != nil {
		return nil, fmt.Errorf("สร้างรอบไม่สำเร็จ: %w", err)
	}

	log.Printf("📅 สร้างรอบ #%d [%s] type=%d open=%s close=%s",
		round.ID, roundNumber, lotteryTypeID,
		openTime.Format("15:04"), closeTime.Format("15:04"))

	return &round, nil
}

// =============================================================================
// OpenRound / CloseRound — เปลี่ยนสถานะรายตัว
// =============================================================================

// OpenRound เปลี่ยนสถานะจาก upcoming → open (auto cron หรือ admin manual)
// ⚠️ fail ถ้าสถานะไม่ใช่ upcoming
func (s *RoundService) OpenRound(roundID int64) error {
	var round model.LotteryRound
	if err := s.db.First(&round, roundID).Error; err != nil {
		return fmt.Errorf("ไม่พบรอบ #%d", roundID)
	}
	if round.Status != "upcoming" {
		return fmt.Errorf("ไม่สามารถเปิดรอบได้ — สถานะปัจจุบัน: %s (ต้องเป็น upcoming)", round.Status)
	}
	if err := s.db.Model(&round).Update("status", "open").Error; err != nil {
		return fmt.Errorf("อัพเดทสถานะไม่สำเร็จ: %w", err)
	}
	log.Printf("🟢 เปิดรอบ #%d [%s]", round.ID, round.RoundNumber)
	return nil
}

// CloseRound เปลี่ยนสถานะจาก open → closed (auto cron หรือ admin manual)
// ⚠️ fail ถ้าสถานะไม่ใช่ open
func (s *RoundService) CloseRound(roundID int64) error {
	var round model.LotteryRound
	if err := s.db.First(&round, roundID).Error; err != nil {
		return fmt.Errorf("ไม่พบรอบ #%d", roundID)
	}
	if round.Status != "open" {
		return fmt.Errorf("ไม่สามารถปิดรอบได้ — สถานะปัจจุบัน: %s (ต้องเป็น open)", round.Status)
	}
	if err := s.db.Model(&round).Update("status", "closed").Error; err != nil {
		return fmt.Errorf("อัพเดทสถานะไม่สำเร็จ: %w", err)
	}
	log.Printf("🔴 ปิดรอบ #%d [%s]", round.ID, round.RoundNumber)
	return nil
}

// =============================================================================
// BatchTransition — auto transition หลายรอบพร้อมกัน (cron ทุก 30 วินาที)
// =============================================================================

// BatchOpenRounds เปิดรอบทั้งหมดที่ถึง open_time แล้ว (ยกเว้น yeekee)
// ⭐ ใช้ batch UPDATE ไม่ใช่ loop ทีละรอบ — ประสิทธิภาพดีกว่า
func (s *RoundService) BatchOpenRounds() int64 {
	now := time.Now()
	result := s.db.Table("lottery_rounds").
		Where("status = ? AND open_time <= ?", "upcoming", now).
		Where("lottery_type_id NOT IN (SELECT id FROM lottery_types WHERE code = 'YEEKEE')").
		Update("status", "open")
	if result.RowsAffected > 0 {
		log.Printf("🟢 Auto-opened %d rounds", result.RowsAffected)
	}
	return result.RowsAffected
}

// BatchCloseRounds ปิดรอบทั้งหมดที่ถึง close_time แล้ว (ยกเว้น yeekee)
func (s *RoundService) BatchCloseRounds() int64 {
	now := time.Now()
	result := s.db.Table("lottery_rounds").
		Where("status = ? AND close_time <= ?", "open", now).
		Where("lottery_type_id NOT IN (SELECT id FROM lottery_types WHERE code = 'YEEKEE')").
		Update("status", "closed")
	if result.RowsAffected > 0 {
		log.Printf("🔴 Auto-closed %d rounds", result.RowsAffected)
	}
	return result.RowsAffected
}
