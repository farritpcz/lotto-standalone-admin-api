// Package config จัดการ configuration ของ standalone-admin-api
//
// ความสัมพันธ์:
// - repo นี้ (#5 lotto-standalone-admin-api) เป็น API สำหรับแอดมินจัดการระบบ
// - คู่กับ: #6 lotto-standalone-admin-web (frontend)
// - share DB กับ: #3 lotto-standalone-member-api (member backend)
// - import: lotto-core (#2) สำหรับ business logic (payout, result calculation)
//
// NOTE: ใช้ MySQL + Redis เดียวกันกับ member-api (#3)
// DB_NAME ต้องตรงกัน = "lotto_standalone"
package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config เก็บ configuration ทั้งหมด
type Config struct {
	Port string
	Env  string

	// Database (MySQL) — ต้องตรงกับ member-api (#3)
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string

	// Redis
	RedisHost     string
	RedisPort     string
	RedisPassword string
	RedisDB       int

	// JWT — admin ใช้ secret แยกจาก member เพื่อความปลอดภัย
	AdminJWTSecret     string
	AdminJWTExpiryHours int

	// Cookie — httpOnly cookie สำหรับ JWT
	CookieDomain string
	CookieSecure bool

	// RKAUTO (GobexPay) — payment gateway อัตโนมัติ
	RKAutoEnabled    bool
	RKAutoBaseURL    string   // https://45.32.117.90/api/v1
	RKAutoAPIKey     string
	RKAutoAPISecret  string
	RKAutoWebhookIPs    string // comma-separated IP whitelist (ว่าง = ไม่เช็ค)
	RKAutoEncryptionKey string // AES-256 key สำหรับเข้ารหัส bank credentials (32 chars)
	WebhookBaseURL      string // URL สาธารณะของเรา สำหรับ callback

	// Cloudflare R2 — image storage
	R2AccountID  string
	R2AccessKey  string
	R2SecretKey  string
	R2Bucket     string
	R2PublicURL  string // เช่น https://pub-xxx.r2.dev หรือ custom domain
}

// Load โหลด config จาก environment variables
func Load() *Config {
	return &Config{
		Port: getEnv("PORT", "8081"), // port 8081 เพื่อไม่ชนกับ member-api (8080)
		Env:  getEnv("ENV", "development"),

		// DB เดียวกันกับ member-api (#3)
		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", "3306"),
		DBUser:     getEnv("DB_USER", "root"),
		DBPassword: getEnv("DB_PASSWORD", "password"),
		DBName:     getEnv("DB_NAME", "lotto_standalone"), // ชื่อเดียวกัน!

		RedisHost:     getEnv("REDIS_HOST", "localhost"),
		RedisPort:     getEnv("REDIS_PORT", "6379"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		RedisDB:       getEnvInt("REDIS_DB", 1), // ใช้ DB 1 (member ใช้ DB 0)

		AdminJWTSecret:      getEnv("ADMIN_JWT_SECRET", "admin-secret-change-in-production"),
		AdminJWTExpiryHours: getEnvInt("ADMIN_JWT_EXPIRY_HOURS", 8),

		CookieDomain: getEnv("COOKIE_DOMAIN", ""),
		CookieSecure: getEnv("COOKIE_SECURE", "false") == "true",

		// RKAUTO
		RKAutoEnabled:    getEnv("RKAUTO_ENABLED", "false") == "true",
		RKAutoBaseURL:    getEnv("RKAUTO_BASE_URL", "https://45.32.117.90/api/v1"),
		RKAutoAPIKey:     getEnv("RKAUTO_API_KEY", ""),
		RKAutoAPISecret:  getEnv("RKAUTO_API_SECRET", ""),
		RKAutoWebhookIPs:    getEnv("RKAUTO_WEBHOOK_IPS", "45.32.117.90"),
		RKAutoEncryptionKey: getEnv("RKAUTO_ENCRYPTION_KEY", "default-key-change-in-prod!!!!"),
		WebhookBaseURL:      getEnv("WEBHOOK_BASE_URL", ""),

		R2AccountID: getEnv("R2_ACCOUNT_ID", ""),
		R2AccessKey: getEnv("R2_ACCESS_KEY", ""),
		R2SecretKey: getEnv("R2_SECRET_KEY", ""),
		R2Bucket:    getEnv("R2_BUCKET", "lotto-images"),
		R2PublicURL: getEnv("R2_PUBLIC_URL", ""),
	}
}

func (c *Config) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName)
}

func (c *Config) RedisAddr() string {
	return fmt.Sprintf("%s:%s", c.RedisHost, c.RedisPort)
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}
