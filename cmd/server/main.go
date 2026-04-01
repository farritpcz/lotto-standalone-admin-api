// Package main — entry point ของ lotto-standalone-admin-api
//
// Repo: #5 lotto-standalone-admin-api
// คู่กับ: #6 lotto-standalone-admin-web (frontend)
// Share DB กับ: #3 lotto-standalone-member-api
// Import: #2 lotto-core (payout, result calculation)
//
// Port: 8081 (member-api ใช้ 8080)
package main

import (
	"fmt"
	"log"

	"github.com/gin-gonic/gin"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/config"
	"github.com/farritpcz/lotto-standalone-admin-api/internal/handler"
)

func main() {
	cfg := config.Load()

	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.Default()

	h := handler.NewHandler(cfg.AdminJWTSecret, cfg.AdminJWTExpiryHours)
	h.SetupRoutes(r)

	addr := fmt.Sprintf(":%s", cfg.Port)
	log.Printf("🔧 lotto-standalone-admin-api starting on %s (env: %s)", addr, cfg.Env)
	log.Printf("📡 API: http://localhost:%s/api/v1", cfg.Port)

	if err := r.Run(addr); err != nil {
		log.Fatal("failed to start server:", err)
	}
}
