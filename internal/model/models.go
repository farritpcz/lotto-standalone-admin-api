// Package model — GORM models สำหรับ standalone-admin-api (#5)
//
// ⭐ ใช้ models เดียวกันกับ standalone-member-api (#3)
// เพราะ share DB = "lotto_standalone"
// TODO: ในอนาคตอาจแยกเป็น shared Go module เฉพาะ models
package model

import (
	"strconv"
	"strings"
	"time"
)

type Admin struct {
	ID           int64      `gorm:"primaryKey" json:"id"`
	Username     string     `gorm:"size:50;uniqueIndex;not null" json:"username"`
	PasswordHash string     `gorm:"size:255;not null" json:"-"`
	Name         string     `gorm:"size:100" json:"name"`
	Role         string     `gorm:"size:20;not null;default:admin" json:"role"` // owner, admin, operator, viewer
	AgentNodeID  *int64     `gorm:"index" json:"agent_node_id"`                 // ⭐ NULL=ระบบ, มีค่า=สร้างโดย node นี้
	Permissions  string     `gorm:"type:text" json:"permissions"`               // JSON array เช่น ["members.view","deposits.approve"]
	Status       string     `gorm:"size:20;not null;default:active" json:"status"`
	LastLoginAt  *time.Time `json:"last_login_at"`
	LastLoginIP  string     `gorm:"size:45" json:"last_login_ip"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// AdminLoginHistory ประวัติ login ของ admin
type AdminLoginHistory struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	AdminID   int64     `gorm:"index;not null" json:"admin_id"`
	IP        string    `gorm:"size:45" json:"ip"`
	UserAgent string    `gorm:"size:255" json:"user_agent"`
	Success   bool      `gorm:"not null" json:"success"`
	CreatedAt time.Time `json:"created_at"`
}

func (AdminLoginHistory) TableName() string { return "admin_login_history" }

// ─── Permission Constants ──────────────────────────────────────────
// หมวดสมาชิก
// members.view, members.detail, members.edit, members.suspend, members.adjust_balance
// หมวดหวย
// lotteries.view, rounds.create, results.submit, bans.manage, rates.manage
// หมวดการเงิน
// deposits.view, deposits.approve, withdrawals.view, withdrawals.approve
// หมวดรายงาน
// reports.view, dashboard.view
// หมวดระบบ
// settings.manage, staff.manage, cms.manage, affiliate.manage

type Member struct {
	ID           int64   `gorm:"primaryKey" json:"id"`
	Username     string  `gorm:"size:50;uniqueIndex;not null" json:"username"`
	PasswordHash string  `gorm:"size:255;not null" json:"-"`
	Phone        string  `gorm:"size:20" json:"phone"`
	Email        string  `gorm:"size:100" json:"email"`
	Balance      float64 `gorm:"type:decimal(15,2);not null;default:0" json:"balance"`
	Status       string  `gorm:"size:20;not null;default:active" json:"status"`
	// ReferredBy — ID ของสมาชิกที่แนะนำมา (affiliate referrer)
	ReferredBy *int64 `gorm:"index" json:"referred_by,omitempty"`
	// ข้อมูลธนาคาร (กรอกตอนสมัคร)
	BankCode          string    `gorm:"size:20" json:"bank_code"`
	BankAccountNumber string    `gorm:"size:20" json:"bank_account_number"`
	BankAccountName   string    `gorm:"size:100" json:"bank_account_name"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type LotteryType struct {
	ID           int64     `gorm:"primaryKey" json:"id"`
	Name         string    `gorm:"size:100;not null" json:"name"`
	Code         string    `gorm:"size:30;uniqueIndex;not null" json:"code"`
	Category     string    `gorm:"size:30;not null;default:government" json:"category"`
	Description  string    `gorm:"type:text" json:"description"`
	ImageURL     string    `gorm:"column:image_url;size:500" json:"image_url"`
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
	AgentID       *int64     `gorm:"index" json:"agent_id,omitempty"` // NULL=global, NOT NULL=per-agent (yeekee)
	LotteryTypeID int64      `gorm:"not null;index" json:"lottery_type_id"`
	RoundNumber   string     `gorm:"size:50;not null" json:"round_number"`
	RoundDate     time.Time  `gorm:"type:date;not null" json:"round_date"`
	OpenTime      time.Time  `gorm:"not null" json:"open_time"`
	CloseTime     time.Time  `gorm:"not null" json:"close_time"`
	Status        string     `gorm:"size:20;not null;default:upcoming" json:"status"`
	ResultTop3    *string    `gorm:"column:result_top3;size:3" json:"result_top3"`
	ResultTop2    *string    `gorm:"column:result_top2;size:2" json:"result_top2"`
	ResultBottom2 *string    `gorm:"column:result_bottom2;size:2" json:"result_bottom2"`
	ResultFront3  *string    `gorm:"column:result_front3;size:3" json:"result_front3"`     // 3 ตัวหน้า (หวยไทย)
	ResultBottom3 *string    `gorm:"column:result_bottom3;size:100" json:"result_bottom3"` // 3 ตัวล่าง (comma-separated, หวยไทย)
	ResultedAt    *time.Time `json:"resulted_at"`
	RejectReason  string     `gorm:"column:reject_reason;type:text" json:"reject_reason,omitempty"` // เหตุผลยกเลิก (void)
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	// Relations
	LotteryType *LotteryType `gorm:"foreignKey:LotteryTypeID" json:"lottery_type,omitempty"`
}

type PayRate struct {
	ID              int64     `gorm:"primaryKey" json:"id"`
	AgentNodeID     *int64    `gorm:"index" json:"agent_node_id"` // ⭐ NULL=ทั้งระบบ, มีค่า=เฉพาะ node
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
	BatchID        string     `gorm:"size:36;index" json:"batch_id"`
	MemberID       int64      `gorm:"not null;index" json:"member_id"`
	LotteryRoundID int64      `gorm:"not null;index" json:"lottery_round_id"`
	BetTypeID      int64      `gorm:"not null" json:"bet_type_id"`
	Number         string     `gorm:"size:10;not null" json:"number"`
	Amount         float64    `gorm:"type:decimal(15,2);not null" json:"amount"`
	Rate           float64    `gorm:"type:decimal(10,2);not null" json:"rate"`
	Status         string     `gorm:"size:20;not null;default:pending" json:"status"` // pending, won, lost, cancelled
	WinAmount      float64    `gorm:"type:decimal(15,2);not null;default:0" json:"win_amount"`
	SettledAt      *time.Time `json:"settled_at"`
	CancelledAt    *time.Time `json:"cancelled_at"`
	CancelledBy    *int64     `json:"cancelled_by"`
	CancelReason   string     `gorm:"type:text" json:"cancel_reason,omitempty"`
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
	AgentNodeID    *int64    `gorm:"index" json:"agent_node_id"` // ⭐ NULL=ทั้งระบบ, มีค่า=เฉพาะ node
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
	ID             int64        `gorm:"primaryKey" json:"id"`
	AgentID        int64        `gorm:"not null;default:1;index" json:"agent_id"`
	AgentNodeID    *int64       `gorm:"index" json:"agent_node_id"` // ⭐ NULL=ทั้งระบบ, มีค่า=เฉพาะ node
	LotteryTypeID  *int64       `gorm:"index" json:"lottery_type_id,omitempty"`
	CommissionRate float64      `gorm:"type:decimal(5,2);not null;default:0" json:"commission_rate"`
	WithdrawalMin  float64      `gorm:"type:decimal(15,2);not null;default:1" json:"withdrawal_min"`
	WithdrawalNote string       `gorm:"type:text" json:"withdrawal_note"`
	Status         string       `gorm:"size:20;not null;default:active" json:"status"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
	LotteryType    *LotteryType `gorm:"foreignKey:LotteryTypeID" json:"lottery_type,omitempty"`
}

func (AffiliateSettings) TableName() string { return "affiliate_settings" }

// ReferralCommission บันทึกค่าคอมมิชชั่นที่คำนวณแล้ว
// สร้างโดย commission_job หลัง SubmitResult ทุกครั้ง
// share DB กับ member-api (#3) — ตาราง referral_commissions
type ReferralCommission struct {
	ID               int64      `gorm:"primaryKey" json:"id"`
	ReferrerID       int64      `gorm:"not null;index" json:"referrer_id"` // สมาชิกที่ได้ค่าคอม
	ReferredID       int64      `gorm:"not null;index" json:"referred_id"` // สมาชิกที่ถูกแนะนำมา
	AgentID          int64      `gorm:"not null;default:1" json:"agent_id"`
	BetID            *int64     `gorm:"index" json:"bet_id"`   // bet ที่ generate commission นี้
	RoundID          *int64     `gorm:"index" json:"round_id"` // round ที่ settle
	BetAmount        float64    `gorm:"type:decimal(15,2);not null" json:"bet_amount"`
	CommissionRate   float64    `gorm:"type:decimal(5,2);not null" json:"commission_rate"`
	CommissionAmount float64    `gorm:"type:decimal(15,2);not null" json:"commission_amount"`
	Status           string     `gorm:"size:20;not null;default:pending" json:"status"` // pending/paid
	PaidAt           *time.Time `json:"paid_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

func (ReferralCommission) TableName() string { return "referral_commissions" }

// =============================================================================
// Yeekee Models — ⭐ เหมือนกับ member-api (#3) เพราะ share DB
// =============================================================================

// YeekeeRound รอบยี่กี (88 รอบ/วัน)
// table: yeekee_rounds
type YeekeeRound struct {
	ID             int64     `gorm:"primaryKey" json:"id"`
	AgentID        int64     `gorm:"not null;index" json:"agent_id"` // ⭐ รอบนี้เป็นของ agent ไหน
	LotteryRoundID int64     `gorm:"not null;index" json:"lottery_round_id"`
	RoundNo        int       `gorm:"not null" json:"round_no"`
	StartTime      time.Time `gorm:"not null" json:"start_time"`
	EndTime        time.Time `gorm:"not null" json:"end_time"`
	Status         string    `gorm:"size:20;not null;default:waiting" json:"status"`
	ResultNumber   string    `gorm:"size:5" json:"result_number"`
	ServerSeed     string    `gorm:"size:64" json:"-"`         // 🔒 ไม่ส่งให้ client
	SeedHash       string    `gorm:"size:64" json:"seed_hash"` // public commitment
	TotalShoots    int       `gorm:"default:0" json:"total_shoots"`
	TotalSum       int64     `gorm:"default:0" json:"total_sum"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	// Relations
	LotteryRound *LotteryRound `gorm:"foreignKey:LotteryRoundID" json:"lottery_round,omitempty"`
}

func (YeekeeRound) TableName() string { return "yeekee_rounds" }

// YeekeeShoot เลขที่สมาชิกยิง (5 หลัก)
// table: yeekee_shoots
type YeekeeShoot struct {
	ID            int64     `gorm:"primaryKey" json:"id"`
	YeekeeRoundID int64     `gorm:"not null;index" json:"yeekee_round_id"`
	MemberID      int64     `gorm:"not null;index" json:"member_id"`
	Number        string    `gorm:"size:5;not null" json:"number"`
	ShotAt        time.Time `gorm:"not null" json:"shot_at"`
	IsBot         bool      `gorm:"default:0;not null" json:"is_bot"` // ⭐ true = ยิงโดย bot
	// Relations
	Member *Member `gorm:"foreignKey:MemberID" json:"member,omitempty"`
}

func (YeekeeShoot) TableName() string { return "yeekee_shoots" }

// AutoBanRule กฎอั้นเลขอัตโนมัติ
// table: auto_ban_rules
type AutoBanRule struct {
	ID              int64     `gorm:"primaryKey" json:"id"`
	AgentID         int64     `gorm:"not null;default:1;index" json:"agent_id"`
	AgentNodeID     *int64    `gorm:"index" json:"agent_node_id"` // ⭐ NULL=ทั้งระบบ, มีค่า=เฉพาะ node
	LotteryTypeID   int64     `gorm:"not null;index" json:"lottery_type_id"`
	BetType         string    `gorm:"size:30;not null" json:"bet_type"`
	ThresholdAmount float64   `gorm:"type:decimal(15,2);not null" json:"threshold_amount"`
	Action          string    `gorm:"size:20;not null;default:full_ban" json:"action"`
	ReducedRate     float64   `gorm:"type:decimal(10,2);default:0" json:"reduced_rate"`
	Capital         float64   `gorm:"type:decimal(15,2);default:0" json:"capital"`
	MaxLoss         float64   `gorm:"type:decimal(15,2);default:0" json:"max_loss"`
	Rate            float64   `gorm:"type:decimal(10,2);default:0" json:"rate"`
	Status          string    `gorm:"size:20;not null;default:active" json:"status"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	// Relations
	LotteryType *LotteryType `gorm:"foreignKey:LotteryTypeID" json:"lottery_type,omitempty"`
}

func (AutoBanRule) TableName() string { return "auto_ban_rules" }

// ActivityLog บันทึกทุก admin action (audit trail)
// table: activity_logs
type ActivityLog struct {
	ID          int64     `gorm:"primaryKey" json:"id"`
	AdminID     int64     `gorm:"index;not null" json:"admin_id"`
	Method      string    `gorm:"size:10;not null" json:"method"`
	Path        string    `gorm:"size:255;not null" json:"path"`
	RequestBody string    `gorm:"type:text" json:"request_body,omitempty"`
	StatusCode  int       `gorm:"not null" json:"status_code"`
	IPAddress   string    `gorm:"size:45" json:"ip_address"`
	CreatedAt   time.Time `json:"created_at"`
}

func (ActivityLog) TableName() string { return "activity_logs" }

// ShareTemplate ข้อความสำเร็จรูปสำหรับแชร์ลิงก์เชิญ (admin สร้าง)
type ShareTemplate struct {
	ID          int64     `gorm:"primaryKey" json:"id"`
	AgentID     int64     `gorm:"not null;index" json:"agent_id"`
	AgentNodeID *int64    `gorm:"index" json:"agent_node_id"` // ⭐ NULL=ทั้งระบบ, มีค่า=เฉพาะ node
	Name        string    `gorm:"size:100;not null" json:"name"`
	Content     string    `gorm:"type:text;not null" json:"content"` // placeholder: {link}, {code}, {username}
	Platform    string    `gorm:"size:30;not null;default:all" json:"platform"`
	SortOrder   int       `gorm:"not null;default:0" json:"sort_order"`
	Status      string    `gorm:"size:20;not null;default:active" json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (ShareTemplate) TableName() string { return "share_templates" }

// CommissionAdjustment admin ปรับค่าคอม (เพิ่ม/ลด/ยกเลิก) + audit log
type CommissionAdjustment struct {
	ID            int64     `gorm:"primaryKey" json:"id"`
	AgentID       int64     `gorm:"not null;index" json:"agent_id"`
	MemberID      int64     `gorm:"not null;index" json:"member_id"`
	AdminID       int64     `gorm:"not null" json:"admin_id"`
	Type          string    `gorm:"size:20;not null" json:"type"` // add, deduct, cancel
	Amount        float64   `gorm:"type:decimal(15,2);not null" json:"amount"`
	Reason        string    `gorm:"type:text;not null" json:"reason"`
	CommissionID  *int64    `json:"commission_id,omitempty"`
	BalanceBefore float64   `gorm:"type:decimal(15,2);not null" json:"balance_before"`
	BalanceAfter  float64   `gorm:"type:decimal(15,2);not null" json:"balance_after"`
	CreatedAt     time.Time `json:"created_at"`

	Member *Member `gorm:"foreignKey:MemberID" json:"member,omitempty"`
}

func (CommissionAdjustment) TableName() string { return "commission_adjustments" }

// =============================================================================
// Agent Downline Models — ระบบปล่อยสาย (Hierarchical Profit Sharing)
// share DB กับ member-api (#3) — ดู model/downline.go ใน member-api
// =============================================================================

// AgentNode — 1 node ในสายงาน (tree structure)
// table: agent_nodes
// path: materialized path เช่น "/1/5/12/" ใช้ LIKE หาลูกทั้งหมด
// share_percent: % ที่ถือ → กำไร = ส่วนต่างกับลูก
type AgentNode struct {
	ID           int64     `json:"id" gorm:"primaryKey"`
	AgentID      int64     `json:"agent_id" gorm:"not null;index"`
	ParentID     *int64    `json:"parent_id" gorm:"index"`
	Role         string    `json:"role" gorm:"size:20;not null"`
	Name         string    `json:"name" gorm:"size:100;not null"`
	Username     string    `json:"username" gorm:"size:50;not null"`
	PasswordHash string    `json:"-" gorm:"size:255;not null"`
	Depth        int       `json:"depth" gorm:"not null;default:0"`
	Path         string    `json:"path" gorm:"size:500;not null;default:/"`
	SharePercent float64   `json:"share_percent" gorm:"type:decimal(5,2);not null;default:100"`
	Phone        string    `json:"phone" gorm:"size:20"`
	LineID       string    `json:"line_id" gorm:"size:50"`
	Note         string    `json:"note" gorm:"type:text"`
	Status       string    `json:"status" gorm:"size:20;not null;default:active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	// Computed fields (ไม่อยู่ใน DB)
	Children    []AgentNode `json:"children,omitempty" gorm:"-"`
	MemberCount int64       `json:"member_count" gorm:"-"`
	ChildCount  int64       `json:"child_count" gorm:"-"`
	Parent      *AgentNode  `json:"parent,omitempty" gorm:"foreignKey:ParentID"`
}

func (AgentNode) TableName() string { return "agent_nodes" }

// RootNodeID คืน id ของ root node ใน tree นี้ — parse จาก Path (เช่น "/1/5/12/" → 1)
// AIDEV-NOTE: ใช้ใน JWT token claim เพื่อ scope ข้อมูล (node → เห็นเฉพาะ tree ตัวเอง)
// ถ้า Path ไม่มี segment (root ตัวเอง) → return n.ID
func (n AgentNode) RootNodeID() int64 {
	p := strings.Trim(n.Path, "/")
	if p == "" {
		return n.ID
	}
	// ตัด segment แรก
	first := p
	if i := strings.Index(p, "/"); i > 0 {
		first = p[:i]
	}
	if root, err := strconv.ParseInt(first, 10, 64); err == nil && root > 0 {
		return root
	}
	return n.ID
}

// AgentNodeCommissionSetting — ตั้ง % แยกตามประเภทหวย (override)
// table: agent_node_commission_settings
type AgentNodeCommissionSetting struct {
	ID           int64     `json:"id" gorm:"primaryKey"`
	AgentNodeID  int64     `json:"agent_node_id" gorm:"not null;index"`
	LotteryType  string    `json:"lottery_type" gorm:"size:50;not null"`
	SharePercent float64   `json:"share_percent" gorm:"type:decimal(5,2);not null"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (AgentNodeCommissionSetting) TableName() string { return "agent_node_commission_settings" }

// AgentProfitTransaction — บันทึกกำไร/ขาดทุนของ 1 node จาก 1 bet
// table: agent_profit_transactions
type AgentProfitTransaction struct {
	ID           int64      `json:"id" gorm:"primaryKey"`
	AgentID      int64      `json:"agent_id" gorm:"not null"`
	RoundID      int64      `json:"round_id" gorm:"not null;index"`
	BetID        int64      `json:"bet_id" gorm:"not null"`
	AgentNodeID  int64      `json:"agent_node_id" gorm:"not null;index"`
	FromNodeID   *int64     `json:"from_node_id"`
	MemberID     int64      `json:"member_id" gorm:"not null;index"`
	BetAmount    float64    `json:"bet_amount" gorm:"type:decimal(12,2);not null"`
	NetResult    float64    `json:"net_result" gorm:"type:decimal(12,2);not null"`
	MyPercent    float64    `json:"my_percent" gorm:"type:decimal(5,2);not null"`
	ChildPercent float64    `json:"child_percent" gorm:"type:decimal(5,2);not null"`
	DiffPercent  float64    `json:"diff_percent" gorm:"type:decimal(5,2);not null"`
	ProfitAmount float64    `json:"profit_amount" gorm:"type:decimal(12,2);not null"`
	CreatedAt    time.Time  `json:"created_at"`
	AgentNode    *AgentNode `json:"agent_node,omitempty" gorm:"foreignKey:AgentNodeID"`
}

func (AgentProfitTransaction) TableName() string { return "agent_profit_transactions" }

// RoleHierarchy ลำดับยศ: index น้อย = สูงกว่า
var RoleHierarchy = map[string]int{
	"admin": 0, "share_holder": 1, "senior": 2,
	"master": 3, "agent": 4, "agent_downline": 5,
}

// NextRole คืน role ถัดไปเมื่อสร้างลูก
func NextRole(parentRole string) string {
	switch parentRole {
	case "admin":
		return "share_holder"
	case "share_holder":
		return "senior"
	case "senior":
		return "master"
	case "master":
		return "agent"
	default:
		return "agent_downline"
	}
}

// AdminActionLog บันทึก business action ที่แอดมินทำ (เช่น กดออกผลยี่กี manual)
// table: admin_action_logs
type AdminActionLog struct {
	ID         int64     `gorm:"primaryKey" json:"id"`
	AdminID    int64     `gorm:"not null;index" json:"admin_id"`
	Action     string    `gorm:"size:100;not null;index" json:"action"` // เช่น yeekee_manual_settle
	TargetType string    `gorm:"size:50" json:"target_type"`            // เช่น yeekee_round
	TargetID   int64     `json:"target_id"`
	Details    string    `gorm:"type:json" json:"details"` // JSON รายละเอียด
	IP         string    `gorm:"size:45" json:"ip"`
	CreatedAt  time.Time `json:"created_at"`
}

func (AdminActionLog) TableName() string { return "admin_action_logs" }
