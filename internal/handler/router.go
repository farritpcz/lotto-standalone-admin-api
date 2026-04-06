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
	CookieDomain        string           // httpOnly cookie domain
	CookieSecure        bool             // httpOnly cookie secure flag
	Env                 string           // "development" or "production"
	DB                  *gorm.DB         // inject จาก main.go — ⭐ share DB กับ member-api (#3)
	Redis               *redis.Client    // Redis สำหรับ cache dashboard stats
	RKAutoClient        interface{}      // *rkauto.Client (nil = disabled)
	EncryptionKey       string           // AES-256 key สำหรับ encrypt bank credentials
	R2                  interface{}      // *storage.R2Client (nil = local fallback)
	RoundService        interface{}      // *service.RoundService — ⭐ centralized round management
	Config              *DeployHandlerConfig // ⭐ config สำหรับ deploy เว็บใหม่
}

// DeployHandlerConfig config ย่อยสำหรับ Handler (ไม่ import config package เพื่อหลีกเลี่ยง circular)
type DeployHandlerConfig struct {
	NginxSitesDir string
	MemberWebPort string
	ServerIP      string
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
		api.POST("/auth/logout", h.AdminLogout)
		api.GET("/public/contact-channels", h.ListPublicContactChannels)

		// === Protected (ต้อง Admin JWT + CSRF + Audit Log) ===
		protected := api.Group("")
		protected.Use(mw.AdminJWTAuth(h.AdminJWTSecret))
		protected.Use(mw.CSRFProtect(h.Env))
		protected.Use(mw.AuditLog(h.DB))
		{
			// Dashboard — ⭐ ทุกคนดูได้ (owner/admin/operator/viewer)
			protected.GET("/dashboard", mw.RequirePermission(h.DB, "dashboard.view"), h.GetDashboard)
			protected.GET("/dashboard/v2", mw.RequirePermission(h.DB, "dashboard.view"), h.GetDashboardV2)

			// Members — ⭐ permission: members.*
			protected.GET("/members", mw.RequirePermission(h.DB, "members.view"), h.ListMembers)
			protected.GET("/members/:id", mw.RequirePermission(h.DB, "members.view"), h.GetMember)
			protected.PUT("/members/:id", mw.RequirePermission(h.DB, "members.edit"), h.UpdateMember)
			protected.PUT("/members/:id/status", mw.RequirePermission(h.DB, "members.status"), h.UpdateMemberStatus)
			protected.PUT("/members/:id/balance", mw.RequirePermission(h.DB, "members.adjust_balance"), h.AdjustMemberBalance)

			// Lotteries — ⭐ permission: lottery.*
			protected.GET("/lotteries", mw.RequirePermission(h.DB, "lottery.view"), h.ListLotteries)
			protected.POST("/lotteries", mw.RequirePermission(h.DB, "lottery.create"), h.CreateLottery)
			protected.PUT("/lotteries/:id", mw.RequirePermission(h.DB, "lottery.edit"), h.UpdateLottery)
			protected.PUT("/lotteries/:id/image", mw.RequirePermission(h.DB, "lottery.edit"), h.UpdateLotteryImage)

			// Rounds — ⭐ permission: lottery.*
			protected.GET("/rounds", mw.RequirePermission(h.DB, "lottery.view"), h.ListRounds)
			protected.POST("/rounds", mw.RequirePermission(h.DB, "lottery.create"), h.CreateRound)
			protected.PUT("/rounds/:id/status", mw.RequirePermission(h.DB, "lottery.create"), h.UpdateRoundStatus)
			protected.PUT("/rounds/:id/open", mw.RequirePermission(h.DB, "lottery.create"), h.ManualOpenRound)
			protected.PUT("/rounds/:id/close", mw.RequirePermission(h.DB, "lottery.create"), h.ManualCloseRound)
			protected.PUT("/rounds/:id/void", mw.RequirePermission(h.DB, "lottery.create"), h.VoidRound)
			protected.GET("/rounds/schedules", mw.RequirePermission(h.DB, "lottery.view"), h.ListSchedules)

			// Results — ดูผลรางวัล (อ่านอย่างเดียว)
			protected.GET("/results", mw.RequirePermission(h.DB, "lottery.view"), h.ListResults)

			// Number Bans — ⭐ permission: lottery.bans
			protected.GET("/bans", mw.RequirePermission(h.DB, "lottery.bans"), h.ListBans)
			protected.POST("/bans", mw.RequirePermission(h.DB, "lottery.bans"), h.CreateBan)
			protected.DELETE("/bans/:id", mw.RequirePermission(h.DB, "lottery.bans"), h.DeleteBan)

			// Pay Rates — ⭐ permission: lottery.rates
			protected.GET("/rates", mw.RequirePermission(h.DB, "lottery.rates"), h.ListRates)
			protected.PUT("/rates/:id", mw.RequirePermission(h.DB, "lottery.rates"), h.UpdateRate)

			// Bets — ⭐ permission: finance.bets
			bets := protected.Group("/bets")
			bets.Use(mw.RequirePermission(h.DB, "finance.bets"))
			{
				bets.GET("", h.ListAllBets)
				bets.GET("/bill/:batchId", h.GetBillDetail)
				bets.PUT("/bill/:batchId/cancel", h.CancelBill)
				bets.GET("/:id/logs", h.GetBetLogs)
				bets.PUT("/:id/cancel", h.CancelBet)
			}

			// Transactions — ⭐ permission: finance.transactions
			protected.GET("/transactions", mw.RequirePermission(h.DB, "finance.transactions"), h.ListAllTransactions)

			// Reports — ⭐ permission: reports.*
			protected.GET("/reports/summary", mw.RequirePermission(h.DB, "reports.view"), h.GetSummaryReport)
			protected.GET("/reports/profit", mw.RequirePermission(h.DB, "reports.view"), h.GetProfitReport)

			// Settings — ⭐ permission: system.settings
			protected.GET("/settings", mw.RequirePermission(h.DB, "system.settings"), h.GetSettings)
			protected.PUT("/settings", mw.RequirePermission(h.DB, "system.settings"), h.UpdateSettings)

			// Agent Theme — ⭐ permission: system.cms
			protected.GET("/agent/theme", mw.RequirePermission(h.DB, "system.cms"), h.GetAgentTheme)
			protected.PUT("/agent/theme", mw.RequirePermission(h.DB, "system.cms"), h.UpdateAgentTheme)

			// Themes — รายการธีมสำเร็จรูป (ทุกคนดูได้)
			protected.GET("/themes", h.ListThemes)

			// Deposit Requests — ⭐ permission: finance.deposits + finance.approve_deposit
			deposits := protected.Group("/deposits")
			{
				deposits.GET("", mw.RequirePermission(h.DB, "finance.deposits"), h.ListDepositRequests)
				deposits.GET("/:id/logs", mw.RequirePermission(h.DB, "finance.deposits"), h.GetDepositLogs)
				deposits.PUT("/:id/approve", mw.RequirePermission(h.DB, "finance.approve_deposit"), h.ApproveDeposit)
				deposits.PUT("/:id/reject", mw.RequirePermission(h.DB, "finance.approve_deposit"), h.RejectDeposit)
				deposits.PUT("/:id/cancel", mw.RequirePermission(h.DB, "finance.approve_deposit"), h.CancelDeposit)
			}

			// Withdraw Requests — ⭐ permission: finance.withdrawals + finance.approve_withdraw
			withdrawals := protected.Group("/withdrawals")
			{
				withdrawals.GET("", mw.RequirePermission(h.DB, "finance.withdrawals"), h.ListWithdrawRequests)
				withdrawals.GET("/:id/logs", mw.RequirePermission(h.DB, "finance.withdrawals"), h.GetWithdrawLogs)
				withdrawals.PUT("/:id/approve", mw.RequirePermission(h.DB, "finance.approve_withdraw"), h.ApproveWithdraw)
				withdrawals.PUT("/:id/reject", mw.RequirePermission(h.DB, "finance.approve_withdraw"), h.RejectWithdraw)
			}

			// Upload — ทุกคนอัพโหลดได้ (ใช้กับหลายเมนู)
			protected.POST("/upload", h.UploadFile)

			// Contact Channels — ⭐ permission: system.cms
			contacts := protected.Group("/contact-channels")
			contacts.Use(mw.RequirePermission(h.DB, "system.cms"))
			{
				contacts.GET("", h.ListContactChannels)
				contacts.POST("", h.CreateContactChannel)
				contacts.PUT("/:id", h.UpdateContactChannel)
				contacts.DELETE("/:id", h.DeleteContactChannel)
			}

			// Agent Bank Accounts — ⭐ permission: system.settings
			agentBank := protected.Group("/agent/bank-accounts")
			agentBank.Use(mw.RequirePermission(h.DB, "system.settings"))
			{
				agentBank.GET("", h.ListAgentBankAccounts)
				agentBank.POST("", h.CreateAgentBankAccount)
				agentBank.PUT("/:id", h.UpdateAgentBankAccount)
				agentBank.DELETE("/:id", h.DeleteAgentBankAccount)
			}

			// RKAUTO Bank Account Operations
			protected.POST("/bank-accounts/:id/register-rkauto", h.RegisterBankAccountRKAuto)
			protected.POST("/bank-accounts/:id/activate-rkauto", h.ActivateBankAccountRKAuto)
			protected.POST("/bank-accounts/:id/deactivate-rkauto", h.DeactivateBankAccountRKAuto)

			// Member Levels — ⭐ permission: system.cms
			memberLevels := protected.Group("/member-levels")
			memberLevels.Use(mw.RequirePermission(h.DB, "system.cms"))
			{
				memberLevels.GET("", h.ListMemberLevels)
				memberLevels.POST("", h.CreateMemberLevel)
				memberLevels.PUT("/reorder", h.ReorderMemberLevels)
				memberLevels.PUT("/:id", h.UpdateMemberLevel)
				memberLevels.DELETE("/:id", h.DeleteMemberLevel)
			}

			// Promotions — ⭐ permission: system.cms
			promos := protected.Group("/promotions")
			promos.Use(mw.RequirePermission(h.DB, "system.cms"))
			{
				promos.GET("", h.ListPromotions)
				promos.POST("", h.CreatePromotion)
				promos.PUT("/:id", h.UpdatePromotion)
				promos.PUT("/:id/status", h.UpdatePromotionStatus)
				promos.DELETE("/:id", h.DeletePromotion)
			}

			// CMS — ⭐ permission: system.cms
			cms := protected.Group("/cms")
			cms.Use(mw.RequirePermission(h.DB, "system.cms"))
			{
				cms.GET("/banners", h.ListBanners)
				cms.POST("/banners", h.CreateBanner)
				cms.PUT("/banners/reorder", h.ReorderBanners)
				cms.PUT("/banners/:id", h.UpdateBanner)
				cms.DELETE("/banners/:id", h.DeleteBanner)
				cms.GET("/ticker", h.GetTicker)
				cms.PUT("/ticker", h.UpdateTicker)
			}

			// Notifications — ⭐ permission: system.settings
			notif := protected.Group("/notifications")
			notif.Use(mw.RequirePermission(h.DB, "system.settings"))
			{
				notif.GET("/config", h.GetNotificationConfig)
				notif.PUT("/config", h.UpdateNotificationConfig)
				notif.POST("/test", h.TestNotification)
			}

			// Member Credit Report — ⭐ permission: reports.view
			protected.GET("/reports/member-credit", mw.RequirePermission(h.DB, "reports.view"), h.GetMemberCreditReport)

			// Auto-Ban Rules — ⭐ permission: lottery.bans
			autoBan := protected.Group("/auto-ban-rules")
			autoBan.Use(mw.RequirePermission(h.DB, "lottery.bans"))
			{
				autoBan.GET("", h.ListAutoBanRules)
				autoBan.POST("", h.CreateAutoBanRule)
				autoBan.POST("/bulk", h.BulkCreateAutoBanRules)
				autoBan.PUT("/:id", h.UpdateAutoBanRule)
				autoBan.DELETE("/:id", h.DeleteAutoBanRule)
			}

			// Yeekee — ⭐ permission: lottery.view (ดู) + lottery.create (จัดการ)
			yeekee := protected.Group("/yeekee")
			{
				yeekee.GET("/rounds", mw.RequirePermission(h.DB, "lottery.view"), h.ListYeekeeRounds)
				yeekee.GET("/rounds/:id", mw.RequirePermission(h.DB, "lottery.view"), h.GetYeekeeRoundDetail)
				yeekee.GET("/rounds/:id/shoots", mw.RequirePermission(h.DB, "lottery.view"), h.ListYeekeeShoots)
				yeekee.POST("/rounds/:id/settle", mw.RequirePermission(h.DB, "lottery.create"), h.ManualSettleYeekeeRound)
				yeekee.GET("/stats", mw.RequirePermission(h.DB, "lottery.view"), h.GetYeekeeStats)
				yeekee.GET("/config", mw.RequirePermission(h.DB, "lottery.view"), h.GetYeekeeAgentConfig)
				yeekee.POST("/config", mw.RequirePermission(h.DB, "lottery.create"), h.SetYeekeeAgentConfig)
			}

			// Staff — ⭐ permission: system.staff
			staff := protected.Group("/staff")
			staff.Use(mw.RequirePermission(h.DB, "system.staff"))
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

			// ⭐ Agent Downline — ระบบปล่อยสาย (Hierarchical Profit Sharing)
			// โครงสร้าง: admin → share_holder → senior → master → agent → agent_downline
			// กำไร = ส่วนต่าง % ระหว่างตัวเองกับลูก
			downline := protected.Group("/downline")
			{
				// Tree view — ดึง tree ทั้งหมด (hierarchical)
				downline.GET("/tree", h.GetDownlineTree)
				// Nodes CRUD — จัดการ node ในสายงาน
				downline.GET("/nodes", h.ListDownlineNodes)
				downline.GET("/nodes/:id", h.GetDownlineNode)
				downline.POST("/nodes", h.CreateDownlineNode)
				downline.PUT("/nodes/:id", h.UpdateDownlineNode)
				downline.DELETE("/nodes/:id", h.DeleteDownlineNode)
				// Commission Settings — ตั้ง % แยกตามประเภทหวย
				downline.GET("/nodes/:id/commission", h.GetNodeCommissionSettings)
				downline.PUT("/nodes/:id/commission", h.UpdateNodeCommissionSettings)
				// Profit Reports — รายงานกำไร
				downline.GET("/profits", h.GetDownlineProfits)
				downline.GET("/profits/:nodeId", h.GetNodeProfits)
			}

			// ⭐ EasySlip — ตั้งค่าระบบตรวจสลิปอัตโนมัติ
			easyslipCfg := protected.Group("/easyslip")
			easyslipCfg.Use(mw.RequirePermission(h.DB, "system.settings"))
			{
				easyslipCfg.GET("/config", h.GetEasySlipConfig)           // ดึง config ปัจจุบัน
				easyslipCfg.POST("/config", h.UpsertEasySlipConfig)       // สร้าง/อัพเดท config
				easyslipCfg.DELETE("/config", h.DeleteEasySlipConfig)     // ลบ config (ปิด EasySlip)
				easyslipCfg.POST("/test", h.TestEasySlipConnection)       // ทดสอบ API key
				easyslipCfg.GET("/verifications", mw.RequirePermission(h.DB, "finance.deposits"), h.ListEasySlipVerifications)
				easyslipCfg.GET("/deposits/:id/verification", mw.RequirePermission(h.DB, "finance.deposits"), h.GetDepositVerification)
			}

			// Affiliate Settings — commission rates + withdrawal conditions
			affiliate := protected.Group("/affiliate")
			{
				affiliate.GET("/settings", h.GetAffiliateSettings)
				affiliate.POST("/settings", h.UpsertAffiliateSetting)
				affiliate.DELETE("/settings/:id", h.DeleteAffiliateSetting)
				affiliate.GET("/report", h.GetAffiliateReport)

				// Share Templates — ข้อความสำเร็จรูปสำหรับแชร์
				affiliate.GET("/share-templates", h.ListShareTemplates)
				affiliate.POST("/share-templates", h.CreateShareTemplate)
				affiliate.PUT("/share-templates/:id", h.UpdateShareTemplate)
				affiliate.DELETE("/share-templates/:id", h.DeleteShareTemplate)

				// Commission Adjustments — ปรับค่าคอมด้วยมือ + audit log
				affiliate.GET("/adjustments", h.ListCommissionAdjustments)
				affiliate.POST("/adjustments", h.CreateCommissionAdjustment)
			}
		}
	}

	// =================================================================
	// ⭐ Node Portal — login + ดูสายงาน + CRUD ลูกตรง + ดูกำไร
	// แยกจาก admin routes — ใช้ JWT "node_token" cookie
	// กฎ: เห็นทั้งสาย, แก้ไขได้เฉพาะลูกตรง, หลาน = read-only
	// =================================================================
	nodeAuth := api.Group("/node/auth")
	{
		nodeAuth.POST("/login", h.NodeLogin)
		nodeAuth.POST("/logout", h.NodeLogout)
	}

	nodeProtected := api.Group("/node")
	nodeProtected.Use(mw.NodeJWTAuth(h.AdminJWTSecret))
	{
		nodeProtected.GET("/me", h.NodeGetMe)
		nodeProtected.GET("/tree", h.NodeGetTree)
		nodeProtected.GET("/children", h.NodeListChildren)
		nodeProtected.POST("/children", h.NodeCreateChild)
		nodeProtected.PUT("/children/:id", h.NodeUpdateChild)
		nodeProtected.DELETE("/children/:id", h.NodeDeleteChild)
		nodeProtected.GET("/profits", h.NodeGetProfits)
	}

	// Static files — serve uploaded images
	r.Static("/uploads", "./uploads")

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
