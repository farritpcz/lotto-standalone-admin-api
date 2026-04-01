// Package model — GORM models สำหรับ standalone-admin-api (#5)
//
// ⭐ ใช้ models เดียวกันกับ standalone-member-api (#3)
// เพราะ share DB = "lotto_standalone"
// TODO: ในอนาคตอาจแยกเป็น shared Go module เฉพาะ models
package model

import "time"

type Admin struct {
	ID           int64      `gorm:"primaryKey" json:"id"`
	Username     string     `gorm:"size:50;uniqueIndex;not null" json:"username"`
	PasswordHash string     `gorm:"size:255;not null" json:"-"`
	Name         string     `gorm:"size:100" json:"name"`
	Role         string     `gorm:"size:20;not null;default:admin" json:"role"`
	Status       string     `gorm:"size:20;not null;default:active" json:"status"`
	LastLoginAt  *time.Time `json:"last_login_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type Member struct {
	ID           int64     `gorm:"primaryKey" json:"id"`
	Username     string    `gorm:"size:50;uniqueIndex;not null" json:"username"`
	PasswordHash string    `gorm:"size:255;not null" json:"-"`
	Phone        string    `gorm:"size:20" json:"phone"`
	Email        string    `gorm:"size:100" json:"email"`
	Balance      float64   `gorm:"type:decimal(15,2);not null;default:0" json:"balance"`
	Status       string    `gorm:"size:20;not null;default:active" json:"status"`
	// ReferredBy — ID ของสมาชิกที่แนะนำมา (affiliate referrer)
	// ⭐ ใช้โดย commission_job เพื่อหา referrer ของ bettor
	ReferredBy   *int64    `gorm:"index" json:"referred_by,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type LotteryType struct {
	ID           int64     `gorm:"primaryKey" json:"id"`
	Name         string    `gorm:"size:100;not null" json:"name"`
	Code         string    `gorm:"size:30;uniqueIndex;not null" json:"code"`
	Category     string    `gorm:"size:30;not null;default:government" json:"category"`
	Description  string    `gorm:"type:text" json:"description"`
	Icon         string    `gorm:"size:50" json:"icon"`
	IsAutoResult bool      `gorm:"column:is_auto_result;not null;default:false" json:"is_auto_result"`
	Status       string    `gorm:"size:20;not null;default:active" json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type BetType struct {
	ID          int64     `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"size:50;not null" json:"name"`
	Code        string    `gorm:"size:20;uniqueIndex;not null" json:"code"`
	DigitCount  int       `gorm:"not null" json:"digit_count"`
	Description string    `gorm:"type:text" json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

type LotteryRound struct {
	ID            int64      `gorm:"primaryKey" json:"id"`
	LotteryTypeID int64      `gorm:"not null;index" json:"lottery_type_id"`
	RoundNumber   string     `gorm:"size:50;not null" json:"round_number"`
	RoundDate     time.Time  `gorm:"type:date;not null" json:"round_date"`
	OpenTime      time.Time  `gorm:"not null" json:"open_time"`
	CloseTime     time.Time  `gorm:"not null" json:"close_time"`
	Status        string     `gorm:"size:20;not null;default:upcoming" json:"status"`
	ResultTop3    *string    `gorm:"column:result_top3;size:3" json:"result_top3"`
	ResultTop2    *string    `gorm:"column:result_top2;size:2" json:"result_top2"`
	ResultBottom2 *string    `gorm:"column:result_bottom2;size:2" json:"result_bottom2"`
	ResultedAt    *time.Time `json:"resulted_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	// Relations
	LotteryType *LotteryType `gorm:"foreignKey:LotteryTypeID" json:"lottery_type,omitempty"`
}

type PayRate struct {
	ID              int64     `gorm:"primaryKey" json:"id"`
	LotteryTypeID   int64     `gorm:"not null" json:"lottery_type_id"`
	BetTypeID       int64     `gorm:"not null" json:"bet_type_id"`
	Rate            float64   `gorm:"type:decimal(10,2);not null" json:"rate"`
	MaxBetPerNumber float64   `gorm:"type:decimal(15,2);not null;default:0" json:"max_bet_per_number"`
	Status          string    `gorm:"size:20;not null;default:active" json:"status"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	// Relations
	BetType     *BetType     `gorm:"foreignKey:BetTypeID" json:"bet_type,omitempty"`
	LotteryType *LotteryType `gorm:"foreignKey:LotteryTypeID" json:"lottery_type,omitempty"`
}

type Bet struct {
	ID             int64      `gorm:"primaryKey" json:"id"`
	MemberID       int64      `gorm:"not null;index" json:"member_id"`
	LotteryRoundID int64      `gorm:"not null;index" json:"lottery_round_id"`
	BetTypeID      int64      `gorm:"not null" json:"bet_type_id"`
	Number         string     `gorm:"size:10;not null" json:"number"`
	Amount         float64    `gorm:"type:decimal(15,2);not null" json:"amount"`
	Rate           float64    `gorm:"type:decimal(10,2);not null" json:"rate"`
	Status         string     `gorm:"size:20;not null;default:pending" json:"status"`
	WinAmount      float64    `gorm:"type:decimal(15,2);not null;default:0" json:"win_amount"`
	SettledAt      *time.Time `json:"settled_at"`
	CreatedAt      time.Time  `json:"created_at"`
	// Relations
	Member       *Member       `gorm:"foreignKey:MemberID" json:"member,omitempty"`
	LotteryRound *LotteryRound `gorm:"foreignKey:LotteryRoundID" json:"lottery_round,omitempty"`
	BetType      *BetType      `gorm:"foreignKey:BetTypeID" json:"bet_type,omitempty"`
}

type NumberBan struct {
	ID             int64     `gorm:"primaryKey" json:"id"`
	LotteryTypeID  int64     `gorm:"not null" json:"lottery_type_id"`
	LotteryRoundID *int64    `json:"lottery_round_id"`
	BetTypeID      int64     `gorm:"not null" json:"bet_type_id"`
	Number         string    `gorm:"size:10;not null" json:"number"`
	BanType        string    `gorm:"size:20;not null;default:full_ban" json:"ban_type"`
	ReducedRate    float64   `gorm:"type:decimal(10,2);not null;default:0" json:"reduced_rate"`
	MaxAmount      float64   `gorm:"type:decimal(15,2);not null;default:0" json:"max_amount"`
	Status         string    `gorm:"size:20;not null;default:active" json:"status"`
	CreatedAt      time.Time `json:"created_at"`
}

type Transaction struct {
	ID            int64     `gorm:"primaryKey" json:"id"`
	MemberID      int64     `gorm:"not null;index" json:"member_id"`
	Type          string    `gorm:"size:20;not null" json:"type"`
	Amount        float64   `gorm:"type:decimal(15,2);not null" json:"amount"`
	BalanceBefore float64   `gorm:"type:decimal(15,2);not null" json:"balance_before"`
	BalanceAfter  float64   `gorm:"type:decimal(15,2);not null" json:"balance_after"`
	ReferenceID   *int64    `json:"reference_id"`
	ReferenceType string    `gorm:"size:30" json:"reference_type"`
	Note          string    `gorm:"type:text" json:"note"`
	CreatedAt     time.Time `json:"created_at"`
}

type Setting struct {
	ID          int64     `gorm:"primaryKey" json:"id"`
	Key         string    `gorm:"size:50;uniqueIndex;not null" json:"key"`
	Value       string    `gorm:"type:text;not null" json:"value"`
	Description string    `gorm:"type:text" json:"description"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// AffiliateSettings การตั้งค่าค่าคอมมิชชั่น (agent เป็นคนตั้ง)
// share DB กับ member-api (#3) — ตาราง affiliate_settings
// lottery_type_id = nil → default rate ใช้กับทุกประเภทหวย
type AffiliateSettings struct {
	ID             int64      `gorm:"primaryKey" json:"id"`
	AgentID        int64      `gorm:"not null;default:1;index" json:"agent_id"`
	LotteryTypeID  *int64     `gorm:"index" json:"lottery_type_id,omitempty"`
	CommissionRate float64    `gorm:"type:decimal(5,2);not null;default:0" json:"commission_rate"`
	WithdrawalMin  float64    `gorm:"type:decimal(15,2);not null;default:1" json:"withdrawal_min"`
	WithdrawalNote string     `gorm:"type:text" json:"withdrawal_note"`
	Status         string     `gorm:"size:20;not null;default:active" json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	LotteryType    *LotteryType `gorm:"foreignKey:LotteryTypeID" json:"lottery_type,omitempty"`
}

func (AffiliateSettings) TableName() string { return "affiliate_settings" }

// ReferralCommission บันทึกค่าคอมมิชชั่นที่คำนวณแล้ว
// สร้างโดย commission_job หลัง SubmitResult ทุกครั้ง
// share DB กับ member-api (#3) — ตาราง referral_commissions
type ReferralCommission struct {
	ID               int64      `gorm:"primaryKey" json:"id"`
	ReferrerID       int64      `gorm:"not null;index" json:"referrer_id"`        // สมาชิกที่ได้ค่าคอม
	ReferredID       int64      `gorm:"not null;index" json:"referred_id"`         // สมาชิกที่ถูกแนะนำมา
	AgentID          int64      `gorm:"not null;default:1" json:"agent_id"`
	BetID            *int64     `gorm:"index" json:"bet_id"`                      // bet ที่ generate commission นี้
	RoundID          *int64     `gorm:"index" json:"round_id"`                    // round ที่ settle
	BetAmount        float64    `gorm:"type:decimal(15,2);not null" json:"bet_amount"`
	CommissionRate   float64    `gorm:"type:decimal(5,2);not null" json:"commission_rate"`
	CommissionAmount float64    `gorm:"type:decimal(15,2);not null" json:"commission_amount"`
	Status           string     `gorm:"size:20;not null;default:pending" json:"status"` // pending/paid
	PaidAt           *time.Time `json:"paid_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

func (ReferralCommission) TableName() string { return "referral_commissions" }
