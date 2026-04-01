// Package service — result_service.go
// Business logic สำหรับกรอกผลรางวัล + trigger payout
//
// ⭐ นี่คือ flow สำคัญที่สุดของ admin:
// admin กรอกผล → บันทึก → ดึง bets → lotto-core SettleRound() → จ่ายเงินคนชนะ
//
// ความสัมพันธ์:
// - ใช้ lotto-core: payout.SettleRound(), payout.GroupWinnersByMember()
// - ถูกเรียกโดย: handler → SubmitResult
// - provider-backoffice-api (#9) มี ResultService คล้ายกัน
//   ต่างกันที่: #9 ต้อง callback แจ้ง operator + wallet คุยกับ operator API
package service

// TODO: implement เมื่อมี repository layer ครบ
// Pseudo code:
//
// func (s *ResultService) SubmitResult(roundID int64, top3, top2, bottom2 string) error {
//     // 1. ดึง round → เช็คว่ายัง closed (ยังไม่มีผล)
//     round := s.roundRepo.FindByID(roundID)
//
//     // 2. บันทึกผลลง round
//     round.ResultTop3 = top3
//     round.ResultTop2 = top2
//     round.ResultBottom2 = bottom2
//     round.Status = "resulted"
//     s.roundRepo.Update(round)
//
//     // 3. ดึง bets ทั้งหมดของรอบ (status = pending)
//     bets := s.betRepo.FindPendingByRound(roundID)
//
//     // 4. ⭐ lotto-core: SettleRound — เทียบผลทั้งหมด
//     output := payout.SettleRound(payout.SettleRoundInput{
//         RoundID: roundID,
//         Result:  types.RoundResult{Top3: top3, Top2: top2, Bottom2: bottom2},
//         Bets:    mapBetsToCore(bets),
//     })
//
//     // 5. DB Transaction: อัพเดท bets + จ่ายเงิน
//     tx := s.db.Begin()
//     for _, result := range output.BetResults {
//         s.betRepo.UpdateStatus(tx, result.BetID, string(result.Status), result.WinAmount)
//     }
//
//     // 6. จ่ายเงินคนชนะ (รวมต่อ member)
//     memberPayouts := payout.GroupWinnersByMember(mapBetsToCore(bets), output.BetResults)
//     for memberID, amount := range memberPayouts {
//         s.memberRepo.CreditBalance(tx, memberID, amount)
//         s.txRepo.Create(tx, &Transaction{
//             MemberID: memberID, Type: "win", Amount: amount, ...
//         })
//     }
//
//     tx.Commit()
//     return nil
// }
