// Package middleware — webhook security สำหรับ RKAUTO callbacks
//
// ⚠️ SECURITY CRITICAL — endpoints เหล่านี้เปิดให้ภายนอกยิงเข้ามา
//
// ป้องกัน 5 ชั้น:
// 1. IP Whitelist — เฉพาะ IP ของ RKAUTO เท่านั้น
// 2. Rate Limiting — 100 req/min per IP
// 3. Body Size Limit — max 10KB (ป้องกัน DoS)
// 4. Signature Verification — HMAC-SHA256
// 5. Anti-Replay — timestamp ต้องไม่เก่ากว่า 5 นาที
package middleware

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/farritpcz/lotto-standalone-admin-api/internal/rkauto"
)

// WebhookSecurityConfig config สำหรับ webhook security
type WebhookSecurityConfig struct {
	APISecret    string   // RKAUTO API Secret สำหรับ verify signature
	AllowedIPs   []string // IP whitelist (ว่าง = ไม่เช็ค)
	RateLimit    int      // max requests per minute per IP (default 100)
	MaxBodySize  int64    // max body size in bytes (default 10240 = 10KB)
}

// WebhookSecurity middleware ป้องกัน webhook endpoints
func WebhookSecurity(cfg WebhookSecurityConfig) gin.HandlerFunc {
	if cfg.RateLimit == 0 {
		cfg.RateLimit = 100
	}
	if cfg.MaxBodySize == 0 {
		cfg.MaxBodySize = 10240
	}

	// Rate limiter per IP
	type ipCounter struct {
		count    int
		resetAt  time.Time
	}
	var (
		mu       sync.Mutex
		counters = make(map[string]*ipCounter)
	)

	return func(c *gin.Context) {
		ip := c.ClientIP()

		// ── 1. IP Whitelist ──────────────────────────────────
		if len(cfg.AllowedIPs) > 0 {
			allowed := false
			for _, aip := range cfg.AllowedIPs {
				if strings.TrimSpace(aip) == ip {
					allowed = true
					break
				}
			}
			if !allowed {
				log.Printf("[WEBHOOK SECURITY] IP blocked: %s (not in whitelist)", ip)
				c.JSON(http.StatusForbidden, gin.H{"error": "IP not allowed"})
				c.Abort()
				return
			}
		}

		// ── 2. Rate Limiting ─────────────────────────────────
		mu.Lock()
		counter, exists := counters[ip]
		now := time.Now()
		if !exists || now.After(counter.resetAt) {
			counters[ip] = &ipCounter{count: 1, resetAt: now.Add(time.Minute)}
		} else {
			counter.count++
			if counter.count > cfg.RateLimit {
				mu.Unlock()
				log.Printf("[WEBHOOK SECURITY] Rate limit exceeded: %s (%d/min)", ip, counter.count)
				c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
				c.Abort()
				return
			}
		}
		mu.Unlock()

		// ── 3. Body Size Limit ───────────────────────────────
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, cfg.MaxBodySize)

		// ── 4. Read body สำหรับ signature verification ──────
		bodyBytes, err := io.ReadAll(c.Request.Body)
		if err != nil {
			log.Printf("[WEBHOOK SECURITY] Body read error from %s: %v", ip, err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			c.Abort()
			return
		}
		// คืน body กลับให้ handler ใช้ต่อ
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		// ── 5. Signature Verification + Anti-Replay ─────────
		timestamp := c.GetHeader("X-Webhook-Timestamp")
		signature := c.GetHeader("X-Webhook-Signature")

		valid, verifyErr := rkauto.VerifyWebhookSignature(bodyBytes, timestamp, signature, cfg.APISecret)
		if !valid {
			log.Printf("[WEBHOOK SECURITY] Signature verification failed from %s: %v", ip, verifyErr)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
			c.Abort()
			return
		}

		// เก็บ event type ใน context
		c.Set("webhook_event", c.GetHeader("X-Webhook-Event"))
		c.Set("webhook_body", bodyBytes)

		log.Printf("[WEBHOOK] Verified: %s from %s (%d bytes)", c.GetHeader("X-Webhook-Event"), ip, len(bodyBytes))

		c.Next()
	}
}
