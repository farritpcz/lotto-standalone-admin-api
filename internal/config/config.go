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
		AdminJWTExpiryHours: getEnvInt("ADMIN_JWT_EXPIRY_HOURS", 8), // admin token อายุสั้นกว่า
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
