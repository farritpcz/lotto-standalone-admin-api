// Package rkauto — types สำหรับ GobexPay (RKAUTO) payment gateway
//
// ใช้สำหรับ:
// - Register บัญชีธนาคาร
// - สั่งถอนเงิน
// - รับ webhook callback (ฝาก/ถอน)
package rkauto

// =============================================================================
// Bank Account Registration
// =============================================================================

// RegisterBankAccountRequest สร้างบัญชีใหม่ใน RKAUTO
// bank_system: "SMS" (SCB/KBANK via SMS), "BANK" (GSB/TMW direct login), "KBIZ" (KBank K-BIZ)
type RegisterBankAccountRequest struct {
	BankSystem      string `json:"bank_system"`                  // SMS, BANK, KBIZ
	BankCode        string `json:"bank_code,omitempty"`          // GSB, TMW (ใช้กับ BANK system)
	BankAccountNo   string `json:"bank_account_no,omitempty"`    // เลขบัญชี (ใช้กับ BANK, KBIZ)
	MobileNumber    string `json:"mobile_number,omitempty"`      // เบอร์โทร (ใช้กับ SMS)
	BankAccountName string `json:"bank_account_name"`            // ชื่อบัญชี
	Username        string `json:"username"`                     // username login ธนาคาร
	Password        string `json:"password"`                     // password login ธนาคาร
	IsDeposit       bool   `json:"is_deposit"`                   // รับฝากเงิน
	IsWithdraw      bool   `json:"is_withdraw"`                  // จ่ายถอนเงิน
}

// RegisterBankAccountResponse ผลลัพธ์จาก register
type RegisterBankAccountResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		UUID            string `json:"uuid"`
		BankSystem      string `json:"bank_system"`
		BankCode        string `json:"bank_code"`
		BankAccountNo   string `json:"bank_account_no"`
		BankAccountName string `json:"bank_account_name"`
		IsDeposit       bool   `json:"is_deposit"`
		IsWithdraw      bool   `json:"is_withdraw"`
		Status          string `json:"status"`
		CreatedAt       string `json:"created_at"`
	} `json:"data"`
}

// =============================================================================
// Withdrawal
// =============================================================================

// CreateWithdrawalRequest สั่งถอนเงินผ่าน RKAUTO
type CreateWithdrawalRequest struct {
	TransactionID   string  `json:"transaction_id"`     // ID จากระบบเรา (unique)
	BankAccountUUID string  `json:"bank_account_uuid"`  // UUID ของบัญชีต้นทาง (ที่ register ไว้)
	ToAccountNo     string  `json:"to_account_no"`      // เลขบัญชีปลายทาง (สมาชิก)
	ToBank          string  `json:"to_bank"`            // รหัสธนาคารปลายทาง (SCB, KBANK, etc.)
	Amount          float64 `json:"amount"`             // จำนวนเงิน
	Currency        string  `json:"currency"`           // THB
}

// CreateWithdrawalResponse ผลลัพธ์จาก create withdrawal
type CreateWithdrawalResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		UUID          string  `json:"uuid"`
		TransactionID string  `json:"transaction_id"`
		Status        string  `json:"status"`        // processing
		Amount        float64 `json:"amount"`
		Currency      string  `json:"currency"`
		ToAccountNo   string  `json:"to_account_no"`
		ToBank        string  `json:"to_bank"`
		TrackID       string  `json:"track_id"`
		CreatedAt     string  `json:"created_at"`
	} `json:"data"`
}

// =============================================================================
// Webhook Payloads — RKAUTO ส่งมาหาเรา
// =============================================================================

// DepositWebhookPayload callback เมื่อมีเงินเข้าบัญชี
type DepositWebhookPayload struct {
	Event                  string  `json:"event"`                     // deposit.completed
	UUID                   string  `json:"uuid"`                      // RKAUTO deposit UUID
	ProviderBankAccountUUID string `json:"provider_bank_account_uuid"` // UUID บัญชีที่รับเงิน
	TransactionID          string  `json:"transaction_id"`            // RKAUTO transaction ID
	Amount                 float64 `json:"amount"`                    // จำนวนเงิน
	Currency               string  `json:"currency"`                  // THB
	Status                 string  `json:"status"`                    // completed
	FromAccountNo          string  `json:"from_account_no"`           // เลขบัญชีผู้โอน
	FromBank               string  `json:"from_bank"`                 // ธนาคารผู้โอน
	TransactionInfo        string  `json:"transaction_info"`          // ข้อความ SMS/notification
	CallbackReceived       bool    `json:"callback_received"`
	CallbackReceivedAt     string  `json:"callback_received_at"`
	ErrorMessage           *string `json:"error_message"`
	CreatedAt              string  `json:"created_at"`
	UpdatedAt              string  `json:"updated_at"`
}

// WithdrawalWebhookPayload callback เมื่อถอนเงินเสร็จ
type WithdrawalWebhookPayload struct {
	Event                  string  `json:"event"`                     // withdrawal.completed / withdrawal.failed
	UUID                   string  `json:"uuid"`                      // RKAUTO withdrawal UUID
	ProviderBankAccountUUID string `json:"provider_bank_account_uuid"` // UUID บัญชีต้นทาง
	TransactionID          string  `json:"transaction_id"`            // ID จากระบบเรา
	Amount                 float64 `json:"amount"`
	Currency               string  `json:"currency"`
	Status                 string  `json:"status"`                    // completed / failed
	ToAccountNo            string  `json:"to_account_no"`
	ToBank                 string  `json:"to_bank"`
	FromAccountNo          string  `json:"from_account_no"`
	FromBank               string  `json:"from_bank"`
	BalanceBefore          float64 `json:"balance_before"`
	BalanceAfter           float64 `json:"balance_after"`
	CallbackReceived       bool    `json:"callback_received"`
	CallbackReceivedAt     string  `json:"callback_received_at"`
	ErrorMessage           *string `json:"error_message"`
	CreatedAt              string  `json:"created_at"`
	UpdatedAt              string  `json:"updated_at"`
}

// =============================================================================
// Webhook Config
// =============================================================================

// WebhookConfigRequest ตั้งค่า webhook URLs
type WebhookConfigRequest struct {
	DepositWebhookURL    string `json:"deposit_webhook_url"`
	WithdrawalWebhookURL string `json:"withdrawal_webhook_url"`
}

// GenericResponse response ทั่วไป
type GenericResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}
