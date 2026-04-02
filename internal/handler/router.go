// Package handler จัดการ HTTP handlers สำหรับ standalone-admin-api
//
// ความสัมพันธ์:
// - repo #5 (admin API) — จัดการระบบหลังบ้าน
// - คู่กับ #6 (admin frontend)
// - share DB กับ #3 (member API)
//
// Admin API Routes:
//
//	POST   /api/v1/auth/login              → Admin Login
//
//	GET    /api/v1/dashboard               → Dashboard stats         [auth]
//
//	GET    /api/v1/members                 → List members            [auth]
//	GET    /api/v1/members/:id             → Get member detail       [auth]
//	PUT    /api/v1/members/:id             → Update member           [auth]
//	PUT    /api/v1/members/:id/status      → Suspend/Activate member [auth]
//
//	GET    /api/v1/lotteries               → List lottery types      [auth]
//	POST   /api/v1/lotteries               → Create lottery type     [auth]
//	PUT    /api/v1/lotteries/:id           → Update lottery type     [auth]
//
//	GET    /api/v1/rounds                  → List rounds             [auth]
//	POST   /api/v1/rounds                  → Create round            [auth]
//	PUT    /api/v1/rounds/:id/status       → Update round status     [auth]
//
//	POST   /api/v1/results/:roundId        → Submit result           [auth]
//	GET    /api/v1/results                 → List results            [auth]
//
//	GET    /api/v1/bans                    → List number bans        [auth]
//	POST   /api/v1/bans                    → Create ban              [auth]
//	DELETE /api/v1/bans/:id                → Remove ban              [auth]
//
//	GET    /api/v1/rates                   → List pay rates          [auth]
//	PUT    /api/v1/rates/:id               → Update pay rate         [auth]
//
//	GET    /api/v1/bets                    → List all bets           [auth]
//	GET    /api/v1/transactions            → List all transactions   [auth]
//
//	GET    /api/v1/reports/summary          → Summary report         [auth]
//	GET    /api/v1/reports/profit           → Profit/Loss report     [auth]
//
//	GET    /api/v1/settings                → Get settings            [auth]
//	PUT    /api/v1/settings                → Update settings         [auth]
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
)

// Handler รวม dependencies ทั้งหมด
type Handler struct {
	AdminJWTSecret      string
	AdminJWTExpiryHours int
	DB                  *gorm.DB         // inject จาก main.go — ⭐ share DB กับ member-api (#3)
	Redis               *redis.Client    // Redis สำหรับ cache dashboard stats
	RKAutoClient        interface{}      // *rkauto.Client (nil = disabled)
}

// NewHandler สร้าง Handler instance
func NewHandler(adminJWTSecret string, adminJWTExpiryHours int) *Handler {
	return &Handler{
		AdminJWTSecret:      adminJWTSecret,
		AdminJWTExpiryHours: adminJWTExpiryHours,
	}
}

// SetupRoutes ลงทะเบียน routes ทั้งหมด
func (h *Handler) SetupRoutes(r *gin.Engine) {
	api := r.Group("/api/v1")
	{
		// === Public ===
		api.POST("/auth/login", h.AdminLogin)

		// === Protected (ต้อง Admin JWT + Audit Log) ===
		protected := api.Group("")
		protected.Use(mw.AdminJWTAuth(h.AdminJWTSecret))
		protected.Use(mw.AuditLog(h.DB))
		{
			// Dashboard
			protected.GET("/dashboard", h.GetDashboard)
			protected.GET("/dashboard/v2", h.GetDashboardV2)

			// Members
			protected.GET("/members", h.ListMembers)
			protected.GET("/members/:id", h.GetMember)
			protected.PUT("/members/:id", h.UpdateMember)
			protected.PUT("/members/:id/status", h.UpdateMemberStatus)
			protected.PUT("/members/:id/balance", h.AdjustMemberBalance)

			// Lotteries
			protected.GET("/lotteries", h.ListLotteries)
			protected.POST("/lotteries", h.CreateLottery)
			protected.PUT("/lotteries/:id", h.UpdateLottery)

			// Rounds
			protected.GET("/rounds", h.ListRounds)
			protected.POST("/rounds", h.CreateRound)
			protected.PUT("/rounds/:id/status", h.UpdateRoundStatus)

			// Results — กรอกผลรางวัล
			// ⭐ ตรงนี้สำคัญ: เมื่อ admin กรอกผล → trigger job คำนวณแพ้ชนะ + จ่ายเงิน
			// ใช้ lotto-core: payout.MatchAll() + payout.SummarizeResults()
			protected.POST("/results/:roundId/preview", h.PreviewResult)
			protected.POST("/results/:roundId", h.SubmitResult)
			protected.GET("/results", h.ListResults)

			// Number Bans — เลขอั้น
			protected.GET("/bans", h.ListBans)
			protected.POST("/bans", h.CreateBan)
			protected.DELETE("/bans/:id", h.DeleteBan)

			// Pay Rates
			protected.GET("/rates", h.ListRates)
			protected.PUT("/rates/:id", h.UpdateRate)

			// Bets
			protected.GET("/bets", h.ListAllBets)

			// Transactions
			protected.GET("/transactions", h.ListAllTransactions)

			// Reports
			protected.GET("/reports/summary", h.GetSummaryReport)
			protected.GET("/reports/profit", h.GetProfitReport)

			// Settings
			protected.GET("/settings", h.GetSettings)
			protected.PUT("/settings", h.UpdateSettings)

			// Deposit Requests — อนุมัติ/ปฏิเสธคำขอฝากเงิน
			deposits := protected.Group("/deposits")
			{
				deposits.GET("", h.ListDepositRequests)
				deposits.PUT("/:id/approve", h.ApproveDeposit)
				deposits.PUT("/:id/reject", h.RejectDeposit)
				deposits.PUT("/:id/cancel", h.CancelDeposit)
			}

			// Withdraw Requests — อนุมัติ/ปฏิเสธคำขอถอนเงิน
			withdrawals := protected.Group("/withdrawals")
			{
				withdrawals.GET("", h.ListWithdrawRequests)
				withdrawals.PUT("/:id/approve", h.ApproveWithdraw)
				withdrawals.PUT("/:id/reject", h.RejectWithdraw)
			}

			// RKAUTO Bank Account Operations
			protected.POST("/bank-accounts/:id/register-rkauto", h.RegisterBankAccountRKAuto)
			protected.POST("/bank-accounts/:id/activate-rkauto", h.ActivateBankAccountRKAuto)
			protected.POST("/bank-accounts/:id/deactivate-rkauto", h.DeactivateBankAccountRKAuto)

			// ⭐ Auto-Ban Rules — กฎอั้นเลขอัตโนมัติ
			autoBan := protected.Group("/auto-ban-rules")
			{
				autoBan.GET("", h.ListAutoBanRules)
				autoBan.POST("", h.CreateAutoBanRule)
				autoBan.POST("/bulk", h.BulkCreateAutoBanRules)
				autoBan.PUT("/:id", h.UpdateAutoBanRule)
				autoBan.DELETE("/:id", h.DeleteAutoBanRule)
			}

			// ⭐ Yeekee — monitoring รอบยี่กี real-time
			yeekee := protected.Group("/yeekee")
			{
				yeekee.GET("/rounds", h.ListYeekeeRounds)           // รายการรอบ (paginated + filter)
				yeekee.GET("/rounds/:id", h.GetYeekeeRoundDetail)   // รอบเดียว + shoots
				yeekee.GET("/rounds/:id/shoots", h.ListYeekeeShoots) // เลขยิงในรอบ (paginated)
				yeekee.GET("/stats", h.GetYeekeeStats)              // สถิติวันนี้
			}

			// Staff (Admin Users) — CRUD + permissions + history
			staff := protected.Group("/staff")
			{
				staff.GET("", h.ListStaff)
				staff.GET("/permissions", h.GetAvailablePermissions)
				staff.POST("", h.CreateStaff)
				staff.PUT("/:id", h.UpdateStaff)
				staff.PUT("/:id/status", h.UpdateStaffStatus)
				staff.DELETE("/:id", h.DeleteStaff)
				staff.GET("/:id/login-history", h.GetStaffLoginHistory)
				staff.GET("/:id/activity", h.GetStaffActivity)
			}

			// Affiliate Settings — commission rates + withdrawal conditions
			affiliate := protected.Group("/affiliate")
			{
				affiliate.GET("/settings", h.GetAffiliateSettings)
				affiliate.POST("/settings", h.UpsertAffiliateSetting)
				affiliate.DELETE("/settings/:id", h.DeleteAffiliateSetting)
				affiliate.GET("/report", h.GetAffiliateReport)
			}
		}
	}

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "lotto-standalone-admin-api"})
	})
}

// SetupWebhookRoutes ลงทะเบียน webhook routes (PUBLIC — ไม่ต้อง JWT)
// ⚠️ SECURITY: ป้องกันด้วย WebhookSecurity middleware (IP whitelist + signature + rate limit)
func (h *Handler) SetupWebhookRoutes(r *gin.Engine, webhookCfg mw.WebhookSecurityConfig) {
	webhooks := r.Group("/webhooks/rkauto")
	webhooks.Use(mw.WebhookSecurity(webhookCfg))
	{
		webhooks.POST("/deposit-notify", h.HandleDepositNotify)
		webhooks.POST("/withdraw-notify", h.HandleWithdrawNotify)
	}
}
