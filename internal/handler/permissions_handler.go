// Package handler — permissions admin handlers.
// Split from stubs.go on 2026-04-20.
package handler

import (
	"github.com/gin-gonic/gin"
)

func (h *Handler) GetAvailablePermissions(c *gin.Context) {
	type PermGroup struct {
		Group string `json:"group"`
		Label string `json:"label"`
		Perms []struct {
			Key   string `json:"key"`
			Label string `json:"label"`
		} `json:"perms"`
	}

	permissions := []gin.H{
		{
			"group": "members", "label": "สมาชิก",
			"perms": []gin.H{
				{"key": "members.view", "label": "ดูรายการสมาชิก"},
				{"key": "members.detail", "label": "ดูรายละเอียดสมาชิก"},
				{"key": "members.edit", "label": "แก้ไขข้อมูลสมาชิก"},
				{"key": "members.suspend", "label": "ระงับ/เปิดบัญชี"},
				{"key": "members.adjust_balance", "label": "ปรับยอดเงิน (เติม/หัก)"},
			},
		},
		{
			"group": "lottery", "label": "หวย",
			"perms": []gin.H{
				{"key": "lotteries.view", "label": "ดูประเภทหวย"},
				{"key": "rounds.create", "label": "สร้างรอบหวย"},
				{"key": "results.submit", "label": "กรอกผลหวย"},
				{"key": "bans.manage", "label": "จัดการเลขอั้น"},
				{"key": "rates.manage", "label": "แก้ไขอัตราจ่าย"},
			},
		},
		{
			"group": "finance", "label": "การเงิน",
			"perms": []gin.H{
				{"key": "deposits.view", "label": "ดูรายการฝาก"},
				{"key": "deposits.approve", "label": "อนุมัติ/ปฏิเสธฝาก"},
				{"key": "withdrawals.view", "label": "ดูรายการถอน"},
				{"key": "withdrawals.approve", "label": "อนุมัติ/ปฏิเสธถอน"},
			},
		},
		{
			"group": "reports", "label": "รายงาน",
			"perms": []gin.H{
				{"key": "dashboard.view", "label": "ดู Dashboard"},
				{"key": "reports.view", "label": "ดูรายงาน"},
				{"key": "bets.view", "label": "ดูรายการแทง"},
				{"key": "transactions.view", "label": "ดูธุรกรรม"},
			},
		},
		{
			"group": "system", "label": "ระบบ",
			"perms": []gin.H{
				{"key": "staff.manage", "label": "จัดการพนักงาน"},
				{"key": "settings.manage", "label": "ตั้งค่าระบบ"},
				{"key": "cms.manage", "label": "จัดการหน้าเว็บ"},
				{"key": "affiliate.manage", "label": "จัดการ Affiliate"},
			},
		},
	}

	ok(c, permissions)
}
