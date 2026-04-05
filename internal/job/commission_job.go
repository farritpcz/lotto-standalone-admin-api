// Package job — commission_job.go
// คำนวณและ insert ค่าคอมมิชชั่นหลัง bet round settle เสร็จ
//
// ⭐ ความสัมพันธ์:
//   - เรียกจาก: admin-api SubmitResult() ใน handler/stubs.go
//   - อ่าน table: bets, members, affiliate_settings (share DB กับ member-api #3)
//   - เขียน table: referral_commissions
//
// Flow:
//  1. ดึง round → รู้ lottery_type_id
//  2. ดึง affiliate_settings ของ agent → หา commission rate
//     (lottery-specific rate override default rate)
//  3. ดึง bets ที่ settled แล้ว (won/lost) ของ round
//  4. cache map: bettor_id → referrer_id (ดึงจาก members.referred_by)
//  5. Loop bets → คำนวณ commission → insert referral_commissions
//     (dedup check: ถ้า bet_id ซ้ำ ข้าม)
//
// เรียกใน goroutine ใหม่ → ไม่ block response ของ SubmitResult
package job

import (
	"log"
	"math"
	"time"

	"gorm.io/gorm"

	"github.com/farritpcz/lotto-standalone-admin-api/internal/model"
)

// CalculateCommissions คำนวณค่าคอมมิชชั่นสำหรับ round ที่ settle แล้ว
//
// Parameters:
//   - db: *gorm.DB (แยกจาก main DB ไม่ใช้ transaction — round settle เสร็จแล้ว)
//   - roundID: ID ของ round ที่ submit result แล้ว
//   - agentID: agent ที่รับผิดชอบ (standalone = 1)
//
// หมายเหตุ: ฟังก์ชันนี้ run ใน goroutine แยก ไม่ panic เมื่อ error
func CalculateCommissions(db *gorm.DB, roundID, agentID int64) {
	// -----------------------------------------------------------------------
	// Step 1: ดึง round เพื่อรู้ lottery_type_id
	// -----------------------------------------------------------------------
	var round model.LotteryRound
	if err := db.First(&round, roundID).Error; err != nil {
		log.Printf("[commission_job] round %d not found: %v", roundID, err)
		return
	}

	// -----------------------------------------------------------------------
	// Step 2: ดึง affiliate_settings ของ agent → หา commission rate
	//
	// ลำดับความสำคัญ:
	//   1. lottery_type_id matching → rate เฉพาะประเภทหวยนี้
	//   2. lottery_type_id = NULL → default rate ทุกประเภท
	// -----------------------------------------------------------------------
	var settings []model.AffiliateSettings
	db.Where("agent_id = ? AND status = ?", agentID, "active").Find(&settings)

	defaultRate := 0.0
	specificRate := 0.0
	hasSpecific := false

	for _, s := range settings {
		if s.LotteryTypeID == nil {
			// default rate — ใช้เมื่อไม่มี rate เฉพาะ
			defaultRate = s.CommissionRate
		} else if *s.LotteryTypeID == round.LotteryTypeID {
			// rate เฉพาะประเภทหวยนี้ → override default
			specificRate = s.CommissionRate
			hasSpecific = true
		}
	}

	commissionRate := defaultRate
	if hasSpecific {
		commissionRate = specificRate
	}

	// ถ้า rate = 0 → ไม่มีค่าคอม ข้ามเลย
	if commissionRate <= 0 {
		log.Printf("[commission_job] round %d: commission rate = 0, skip", roundID)
		return
	}

	// -----------------------------------------------------------------------
	// Step 3: ดึง bets ที่ settled แล้ว (won/lost) ของ round นี้
	// -----------------------------------------------------------------------
	var bets []model.Bet
	db.Where("lottery_round_id = ? AND status IN ?", roundID, []string{"won", "lost"}).Find(&bets)

	if len(bets) == 0 {
		log.Printf("[commission_job] round %d: no settled bets found", roundID)
		return
	}

	// -----------------------------------------------------------------------
	// Step 4: สร้าง cache map: bettor_id → referrer_id
	// ดึงเฉพาะ members ที่มี referred_by ไม่ query ทีละคน
	// -----------------------------------------------------------------------
	memberIDs := make([]int64, 0, len(bets))
	seenMember := map[int64]bool{}
	for _, b := range bets {
		if !seenMember[b.MemberID] {
			memberIDs = append(memberIDs, b.MemberID)
			seenMember[b.MemberID] = true
		}
	}

	// ดึงเฉพาะ members ที่มี referred_by ไม่ใช่ NULL
	var members []model.Member
	db.Select("id, referred_by").
		Where("id IN ? AND referred_by IS NOT NULL", memberIDs).
		Find(&members)

	// map: bettor_id → referrer_id
	referrerMap := map[int64]int64{}
	for _, m := range members {
		if m.ReferredBy != nil {
			referrerMap[m.ID] = *m.ReferredBy
		}
	}

	// ถ้าไม่มีใครมี referrer → ข้าม
	if len(referrerMap) == 0 {
		log.Printf("[commission_job] round %d: no members with referrer", roundID)
		return
	}

	// -----------------------------------------------------------------------
	// Step 5: Loop bets → คำนวณ commission → insert referral_commissions
	// -----------------------------------------------------------------------
	now := time.Now()
	inserted := 0
	skipped := 0

	for _, bet := range bets {
		// หา referrer ของ bettor นี้
		referrerID, hasReferrer := referrerMap[bet.MemberID]
		if !hasReferrer {
			continue
		}

		// คำนวณ commission
		// commission_rate เป็น % เช่น 0.5 = 0.5% ของ bet amount
		commissionAmount := bet.Amount * commissionRate / 100
		if commissionAmount <= 0 {
			continue
		}

		// ⭐ Dedup check: ถ้า bet_id + referrer_id ซ้ำ แสดงว่า insert ไปแล้ว
		// ป้องกัน commission ซ้ำถ้า SubmitResult ถูกเรียกซ้ำ (ไม่ควรเกิด แต่ป้องกันไว้)
		var existing int64
		db.Model(&model.ReferralCommission{}).
			Where("bet_id = ? AND referrer_id = ?", bet.ID, referrerID).
			Count(&existing)
		if existing > 0 {
			skipped++
			continue
		}

		// Insert commission record
		comm := model.ReferralCommission{
			ReferrerID:       referrerID,
			ReferredID:       bet.MemberID,
			AgentID:          agentID,
			BetID:            &bet.ID,
			RoundID:          &roundID,
			BetAmount:        bet.Amount,
			CommissionRate:   commissionRate,
			CommissionAmount: commissionAmount,
			Status:           "pending", // member ต้องกด "ถอน" เอง → เปลี่ยนเป็น paid
			CreatedAt:        now,
		}

		if err := db.Create(&comm).Error; err != nil {
			log.Printf("[commission_job] failed to insert commission for bet %d: %v", bet.ID, err)
			continue
		}
		inserted++
	}

	log.Printf(
		"[commission_job] round %d done: rate=%.2f%%, inserted=%d, skipped=%d",
		roundID, commissionRate, inserted, skipped,
	)
}

// =============================================================================
// CalculateDownlineProfits คำนวณกำไร/ขาดทุนของทุก node ในสายงาน
//
// เรียกจาก: SubmitResult() หลังจาก CalculateCommissions
// Run ใน goroutine แยก → ไม่ block response
//
// Flow:
//  1. ดึง bets ที่ settled (won/lost)
//  2. หา member → agent_node_id (ลูกค้าอยู่ใต้ node ไหน)
//  3. Walk up tree: จาก leaf node → root
//  4. ���ต่ละ node: profit = netResult × (myPercent - childPercent) / 100
//  5. Insert agent_profit_transactions (dedup by bet_id + agent_node_id)
//
// ตัวอย่าง: ลูกค้าเล่นเสีย 100 บาท ใต้ agent_downline(91%)
//   agent_downline(91%): diff=91%, profit = 100 × 91% = 91 บาท
//   agent(92%):          diff=1%,  profit = 100 × 1%  = 1  บาท
//   master(93%):         diff=1%,  profit = 100 × 1%  = 1  บาท
//   senior(94%):         diff=1%,  profit = 100 × 1%  = 1  บาท
//   share_holder(95%):   diff=1%,  profit = 100 × 1%  = 1  บาท
//   admin(100%):         diff=5%,  profit = 100 × 5%  = 5  บาท
//                                                  รวม = 100 ✓
// =============================================================================
func CalculateDownlineProfits(db *gorm.DB, roundID, agentID int64) {
	// ── Step 1: ดึง round เพื่อรู้ lottery_type ──
	var round model.LotteryRound
	if err := db.First(&round, roundID).Error; err != nil {
		log.Printf("[downline_profit] round %d not found: %v", roundID, err)
		return
	}

	// ดึง lottery code สำหรับหา commission override
	var lotteryType model.LotteryType
	db.First(&lotteryType, round.LotteryTypeID)
	lotteryCode := lotteryType.Code

	// ── Step 2: ดึง bets ที่ settled ──
	var bets []model.Bet
	db.Where("lottery_round_id = ? AND status IN ?", roundID, []string{"won", "lost"}).Find(&bets)
	if len(bets) == 0 {
		log.Printf("[downline_profit] round %d: no settled bets", roundID)
		return
	}

	// ── Step 3: หา member → agent_node_id ──
	memberIDs := make([]int64, 0, len(bets))
	seen := map[int64]bool{}
	for _, b := range bets {
		if !seen[b.MemberID] {
			memberIDs = append(memberIDs, b.MemberID)
			seen[b.MemberID] = true
		}
	}

	type memberNodeInfo struct {
		ID          int64  `gorm:"column:id"`
		AgentNodeID *int64 `gorm:"column:agent_node_id"`
	}
	var memberNodes []memberNodeInfo
	db.Raw("SELECT id, agent_node_id FROM members WHERE id IN ? AND agent_node_id IS NOT NULL", memberIDs).
		Scan(&memberNodes)

	memberNodeMap := map[int64]int64{} // member_id → agent_node_id
	for _, mn := range memberNodes {
		if mn.AgentNodeID != nil {
			memberNodeMap[mn.ID] = *mn.AgentNodeID
		}
	}

	if len(memberNodeMap) == 0 {
		log.Printf("[downline_profit] round %d: no members in downline", roundID)
		return
	}

	// ── Step 4: ดึง agent_nodes ทั้งหมด (build tree in memory) ──
	var allNodes []model.AgentNode
	db.Where("agent_id = ?", agentID).Find(&allNodes)

	nodeMap := map[int64]*model.AgentNode{}
	for i := range allNodes {
		nodeMap[allNodes[i].ID] = &allNodes[i]
	}

	// ── Step 5: ดึง commission overrides (per lottery type) ──
	var overrides []model.AgentNodeCommissionSetting
	db.Where("lottery_type = ?", lotteryCode).Find(&overrides)
	overrideMap := map[int64]float64{} // node_id → share_percent override
	for _, o := range overrides {
		overrideMap[o.AgentNodeID] = o.SharePercent
	}

	// Helper: ดึง share_percent (ใช้ override ถ้ามี, ไม่มีใช้ค่าหลัก)
	getSharePercent := func(nodeID int64) float64 {
		if pct, ok := overrideMap[nodeID]; ok {
			return pct
		}
		if n, ok := nodeMap[nodeID]; ok {
			return n.SharePercent
		}
		return 0
	}

	// ── Step 6: คำนวณ profit ทุก bet × ทุก node ──
	now := time.Now()
	inserted := 0

	for _, bet := range bets {
		nodeID, hasNode := memberNodeMap[bet.MemberID]
		if !hasNode {
			continue
		}

		// net_result: + = ลูกค้าเสีย (เราได้กำไร), - = ลูกค้าชนะ (เราขาดทุน)
		var netResult float64
		if bet.Status == "lost" {
			netResult = bet.Amount // ลูกค้าเสีย = +
		} else {
			netResult = bet.Amount - bet.WinAmount // ลูกค้าชนะ = มักเป็น -
		}

		// Walk up tree: leaf → root
		currentNodeID := nodeID
		childPercent := 0.0 // leaf: member ไม่มี % (ไม่ใช่ node)

		for currentNodeID != 0 {
			currentNode, exists := nodeMap[currentNodeID]
			if !exists {
				break
			}

			myPercent := getSharePercent(currentNodeID)
			diffPercent := myPercent - childPercent
			profitAmount := math.Round(netResult*diffPercent/100*100) / 100

			// Dedup check
			var existing int64
			db.Model(&model.AgentProfitTransaction{}).
				Where("bet_id = ? AND agent_node_id = ?", bet.ID, currentNodeID).
				Count(&existing)
			if existing > 0 {
				if currentNode.ParentID != nil {
					childPercent = myPercent
					currentNodeID = *currentNode.ParentID
					continue
				}
				break
			}

			// Insert profit record
			pt := model.AgentProfitTransaction{
				AgentID:      agentID,
				RoundID:      roundID,
				BetID:        bet.ID,
				AgentNodeID:  currentNodeID,
				FromNodeID:   nil,
				MemberID:     bet.MemberID,
				BetAmount:    bet.Amount,
				NetResult:    netResult,
				MyPercent:    myPercent,
				ChildPercent: childPercent,
				DiffPercent:  diffPercent,
				ProfitAmount: profitAmount,
				CreatedAt:    now,
			}

			if err := db.Create(&pt).Error; err != nil {
				log.Printf("[downline_profit] bet %d node %d: %v", bet.ID, currentNodeID, err)
			} else {
				inserted++
			}

			// เดินขึ้น parent
			if currentNode.ParentID != nil {
				childPercent = myPercent
				currentNodeID = *currentNode.ParentID
			} else {
				break
			}
		}
	}

	log.Printf("[downline_profit] round %d: inserted %d profit records", roundID, inserted)
}
