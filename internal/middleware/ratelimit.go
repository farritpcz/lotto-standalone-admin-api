// Package middleware — rate limiting
//
// ใช้ in-memory token bucket per IP
// ป้องกัน brute force, spam request, DoS
package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type visitor struct {
	tokens    float64
	lastSeen  time.Time
}

type rateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rate     float64 // tokens per second
	burst    int     // max tokens
}

func newRateLimiter(rate float64, burst int) *rateLimiter {
	rl := &rateLimiter{
		visitors: make(map[string]*visitor),
		rate:     rate,
		burst:    burst,
	}
	// cleanup ทุก 5 นาที — ลบ visitor ที่ไม่ได้เข้ามานานกว่า 10 นาที
	go func() {
		for {
			time.Sleep(5 * time.Minute)
			rl.mu.Lock()
			for ip, v := range rl.visitors {
				if time.Since(v.lastSeen) > 10*time.Minute {
					delete(rl.visitors, ip)
				}
			}
			rl.mu.Unlock()
		}
	}()
	return rl
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[ip]
	now := time.Now()

	if !exists {
		rl.visitors[ip] = &visitor{tokens: float64(rl.burst) - 1, lastSeen: now}
		return true
	}

	// เติม token ตามเวลาที่ผ่านไป
	elapsed := now.Sub(v.lastSeen).Seconds()
	v.tokens += elapsed * rl.rate
	if v.tokens > float64(rl.burst) {
		v.tokens = float64(rl.burst)
	}
	v.lastSeen = now

	if v.tokens < 1 {
		return false
	}

	v.tokens--
	return true
}

// RateLimit middleware — จำกัด request rate per IP
//
// rate: จำนวน request ต่อวินาที
// burst: จำนวน request สูงสุดที่เก็บสะสมได้ (burst capacity)
//
// ตัวอย่าง:
//   - RateLimit(1, 5) → 1 req/sec, burst 5
//   - RateLimit(0.1, 3) → 1 req ทุก 10 วินาที, burst 3 (สำหรับ login)
func RateLimit(rate float64, burst int) gin.HandlerFunc {
	limiter := newRateLimiter(rate, burst)

	return func(c *gin.Context) {
		ip := c.ClientIP()

		if !limiter.allow(ip) {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"success": false,
				"error":   "rate limit exceeded, please try again later",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
