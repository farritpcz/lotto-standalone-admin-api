// Package rkauto — webhook signature verification
//
// ⚠️ SECURITY CRITICAL:
// - ตรวจ HMAC-SHA256 signature ทุก webhook
// - ป้องกัน replay attack (timestamp tolerance 5 นาที)
// - ใช้ crypto.timingSafeEqual (hmac.Equal) ป้องกัน timing attack
package rkauto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"time"
)

// VerifyWebhookSignature ตรวจสอบ webhook signature จาก RKAUTO
//
// Signature format: HMAC-SHA256(TIMESTAMP + "." + JSON_PAYLOAD)
//
// Headers ที่ RKAUTO ส่งมา:
//   - X-Webhook-Event: "deposit.completed" / "withdrawal.completed"
//   - X-Webhook-Timestamp: unix timestamp
//   - X-Webhook-Signature: HMAC-SHA256 hex string
//
// Parameters:
//   - payload: raw JSON body bytes
//   - timestamp: X-Webhook-Timestamp header value
//   - signature: X-Webhook-Signature header value
//   - secret: API Secret
//
// Returns:
//   - valid: signature ถูกต้อง
//   - error: เหตุผลที่ไม่ถูกต้อง (สำหรับ log)
func VerifyWebhookSignature(payload []byte, timestamp, signature, secret string) (bool, error) {
	if timestamp == "" {
		return false, fmt.Errorf("missing timestamp")
	}
	if signature == "" {
		return false, fmt.Errorf("missing signature")
	}
	if len(payload) == 0 {
		return false, fmt.Errorf("empty payload")
	}

	// ── 1. ตรวจ timestamp (anti-replay attack) ──
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false, fmt.Errorf("invalid timestamp format: %s", timestamp)
	}

	now := time.Now().Unix()
	diff := math.Abs(float64(now - ts))
	if diff > 300 { // 5 นาที tolerance
		return false, fmt.Errorf("timestamp too old: %d seconds difference", int(diff))
	}

	// ── 2. สร้าง expected signature ──
	// Format: TIMESTAMP + "." + PAYLOAD_JSON
	signatureString := timestamp + "." + string(payload)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signatureString))
	expectedSignature := hex.EncodeToString(mac.Sum(nil))

	// ── 3. เปรียบเทียบ (timing-safe) ──
	if !hmac.Equal([]byte(expectedSignature), []byte(signature)) {
		return false, fmt.Errorf("signature mismatch")
	}

	return true, nil
}
