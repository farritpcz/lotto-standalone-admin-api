// Package handler — stubs.go
// Stub implementations สำหรับทุก handler
// TODO: implement จริงเมื่อสร้าง service layer
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func jsonTODO(c *gin.Context, name string) {
	c.JSON(http.StatusOK, gin.H{"success": true, "message": name + " - TODO: implement with service layer"})
}

// Auth
func (h *Handler) AdminLogin(c *gin.Context)       { jsonTODO(c, "admin login") }

// Dashboard
func (h *Handler) GetDashboard(c *gin.Context)      { jsonTODO(c, "get dashboard") }

// Members
func (h *Handler) ListMembers(c *gin.Context)        { jsonTODO(c, "list members") }
func (h *Handler) GetMember(c *gin.Context)          { jsonTODO(c, "get member") }
func (h *Handler) UpdateMember(c *gin.Context)       { jsonTODO(c, "update member") }
func (h *Handler) UpdateMemberStatus(c *gin.Context) { jsonTODO(c, "update member status") }

// Lotteries
func (h *Handler) ListLotteries(c *gin.Context)  { jsonTODO(c, "list lotteries") }
func (h *Handler) CreateLottery(c *gin.Context)  { jsonTODO(c, "create lottery") }
func (h *Handler) UpdateLottery(c *gin.Context)  { jsonTODO(c, "update lottery") }

// Rounds
func (h *Handler) ListRounds(c *gin.Context)        { jsonTODO(c, "list rounds") }
func (h *Handler) CreateRound(c *gin.Context)       { jsonTODO(c, "create round") }
func (h *Handler) UpdateRoundStatus(c *gin.Context) { jsonTODO(c, "update round status") }

// Results — ⭐ สำคัญ: เมื่อ admin กรอกผล → trigger payout job
// Flow: กรอกผล → บันทึก result → ดึง bets ทั้งหมด → lotto-core payout.MatchAll()
//       → อัพเดท bets (won/lost) → จ่ายเงินคนชนะ (member.balance += winAmount)
func (h *Handler) SubmitResult(c *gin.Context) { jsonTODO(c, "submit result") }
func (h *Handler) ListResults(c *gin.Context)  { jsonTODO(c, "list results") }

// Number Bans
func (h *Handler) ListBans(c *gin.Context)   { jsonTODO(c, "list bans") }
func (h *Handler) CreateBan(c *gin.Context)  { jsonTODO(c, "create ban") }
func (h *Handler) DeleteBan(c *gin.Context)  { jsonTODO(c, "delete ban") }

// Pay Rates
func (h *Handler) ListRates(c *gin.Context)  { jsonTODO(c, "list rates") }
func (h *Handler) UpdateRate(c *gin.Context) { jsonTODO(c, "update rate") }

// Bets + Transactions
func (h *Handler) ListAllBets(c *gin.Context)         { jsonTODO(c, "list all bets") }
func (h *Handler) ListAllTransactions(c *gin.Context)  { jsonTODO(c, "list all transactions") }

// Reports
func (h *Handler) GetSummaryReport(c *gin.Context) { jsonTODO(c, "summary report") }
func (h *Handler) GetProfitReport(c *gin.Context)  { jsonTODO(c, "profit report") }

// Settings
func (h *Handler) GetSettings(c *gin.Context)    { jsonTODO(c, "get settings") }
func (h *Handler) UpdateSettings(c *gin.Context) { jsonTODO(c, "update settings") }
