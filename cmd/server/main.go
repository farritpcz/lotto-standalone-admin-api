// Package main — entry point ของ lotto-standalone-admin-api
//
// Repo: #5 lotto-standalone-admin-api
// คู่กับ: #6 lotto-standalone-admin-web (frontend)
// Share DB กับ: #3 lotto-standalone-member-api (DB: lotto_standalone)
// Import: #2 lotto-core (payout, result calculation)
//
// Port: 8081 (member-api ใช้ 8080)
package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/redis/go-redis/v9"

	"github.com/farritpcz/lotto-standalone-admin-api/internal/config"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/handler"
	mw "github.com/farritpcz/lotto-standalone-admin-api/internal/middleware"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/rkauto"
)

func main() {
	cfg := config.Load()

	// ⚠️ บังคับ JWT secret ใน production — ห้ามใช้ default
	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
		if cfg.AdminJWTSecret == "admin-secret-change-in-production" {
			log.Fatal("❌ ADMIN_JWT_SECRET must be set in production (cannot use default)")
		}
		if cfg.DBPassword == "password" {
			log.Fatal("❌ DB_PASSWORD must be set in production (cannot use default)")
		}
	}

	// =================================================================
	// เชื่อมต่อ MySQL — ⭐ share DB "lotto_standalone" กับ member-api (#3)
	// =================================================================
	gormConfig := &gorm.Config{}
	if cfg.Env != "production" {
		gormConfig.Logger = logger.Default.LogMode(logger.Info)
	}

	db, err := gorm.Open(mysql.Open(cfg.DSN()), gormConfig)
	if err != nil {
		log.Fatal("❌ Failed to connect to MySQL:", err)
	}
	log.Println("✅ Connected to MySQL:", cfg.DBName)

	// =================================================================
	// สร้าง Router + Handler
	// =================================================================
	r := gin.Default()

	// CORS middleware — whitelist เฉพาะ domain ที่อนุญาต
	allowedOrigins := strings.Split(getEnv("ALLOWED_ORIGINS", "http://localhost:3000,http://localhost:3001,http://localhost:3002"), ",")
	r.Use(mw.CORS(allowedOrigins))

	// Global rate limit — 10 req/sec, burst 30 (ป้องกัน DoS)
	r.Use(mw.RateLimit(10, 30))

	// Redis สำหรับ cache dashboard stats
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr(),
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	log.Println("✅ Redis connected:", cfg.RedisAddr())

	h := handler.NewHandler(cfg.AdminJWTSecret, cfg.AdminJWTExpiryHours)
	h.DB = db
	h.Redis = rdb
	h.EncryptionKey = cfg.RKAutoEncryptionKey
	h.SetupRoutes(r)

	// ⚠️ RKAUTO Webhook Routes (PUBLIC — signature verified)
	if cfg.RKAutoEnabled {
		rkautoClient := rkauto.NewClient(cfg.RKAutoBaseURL, cfg.RKAutoAPIKey, cfg.RKAutoAPISecret)
		h.RKAutoClient = rkautoClient

		webhookIPs := strings.Split(cfg.RKAutoWebhookIPs, ",")
		h.SetupWebhookRoutes(r, mw.WebhookSecurityConfig{
			APISecret:  cfg.RKAutoAPISecret,
			AllowedIPs: webhookIPs,
			RateLimit:  100,
		})

		// ตั้ง webhook URLs ถ้ามี WebhookBaseURL
		if cfg.WebhookBaseURL != "" {
			go func() {
				_, err := rkautoClient.UpdateWebhookURLs(
					cfg.WebhookBaseURL+"/webhooks/rkauto/deposit-notify",
					cfg.WebhookBaseURL+"/webhooks/rkauto/withdraw-notify",
				)
				if err != nil {
					log.Printf("⚠️ Failed to set RKAUTO webhook URLs: %v", err)
				} else {
					log.Println("✅ RKAUTO webhook URLs configured")
				}
			}()
		}

		log.Println("✅ RKAUTO enabled — webhook routes registered")
	} else {
		log.Println("ℹ️ RKAUTO disabled (set RKAUTO_ENABLED=true to enable)")
	}

	addr := fmt.Sprintf(":%s", cfg.Port)
	log.Printf("🔧 lotto-standalone-admin-api starting on %s (env: %s)", addr, cfg.Env)
	log.Printf("📡 API: http://localhost:%s/api/v1", cfg.Port)

	if err := r.Run(addr); err != nil {
		log.Fatal("failed to start server:", err)
	}
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
