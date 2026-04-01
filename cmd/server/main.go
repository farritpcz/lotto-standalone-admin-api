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

	"github.com/gin-gonic/gin"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/farritpcz/lotto-standalone-admin-api/internal/config"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/handler"
)

func main() {
	cfg := config.Load()

	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
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

	// CORS middleware — ให้ admin-web (#6) เรียก API ได้
	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization")
		c.Header("Access-Control-Max-Age", "86400")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	h := handler.NewHandler(cfg.AdminJWTSecret, cfg.AdminJWTExpiryHours)
	h.DB = db // inject DB ให้ handler ใช้
	h.SetupRoutes(r)

	addr := fmt.Sprintf(":%s", cfg.Port)
	log.Printf("🔧 lotto-standalone-admin-api starting on %s (env: %s)", addr, cfg.Env)
	log.Printf("📡 API: http://localhost:%s/api/v1", cfg.Port)

	if err := r.Run(addr); err != nil {
		log.Fatal("failed to start server:", err)
	}
}
