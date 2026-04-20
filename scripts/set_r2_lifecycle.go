// set_r2_lifecycle.go — ตั้ง Lifecycle rule บน R2 bucket
//
// Rule: ลบไฟล์ใน folder "slip/" อัตโนมัติหลัง 30 วัน
//
// ⭐ ทำไมต้องตั้ง?
//   - สลิปฝากเงิน = ข้อมูลละเอียดอ่อน (PDPA) — ไม่ควรเก็บเกินความจำเป็น
//   - 30 วัน = เพียงพอสำหรับ dispute/audit
//   - folder อื่น (avatar, logo, banner ฯลฯ) เก็บตลอดไป (ตามความต้องการ)
//
// Usage:
//
//	go run scripts/set_r2_lifecycle.go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func main() {
	// Load env
	accountID := mustEnv("R2_ACCOUNT_ID")
	accessKey := mustEnv("R2_ACCESS_KEY")
	secretKey := mustEnv("R2_SECRET_KEY")
	bucket := getEnv("R2_BUCKET", "lotto-system")

	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountID)

	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
		config.WithRegion("auto"),
	)
	if err != nil {
		log.Fatal(err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	// ⭐ Lifecycle rule: slip/ → delete หลัง 30 วัน
	// Rule เดียวนี้ครอบเฉพาะ folder slip/ — ไฟล์อื่น (avatar, banner ฯลฯ) ไม่โดน
	input := &s3.PutBucketLifecycleConfigurationInput{
		Bucket: aws.String(bucket),
		LifecycleConfiguration: &types.BucketLifecycleConfiguration{
			Rules: []types.LifecycleRule{
				{
					ID:     aws.String("delete-slips-after-30-days"),
					Status: types.ExpirationStatusEnabled,
					Filter: &types.LifecycleRuleFilter{
						Prefix: aws.String("slip/"),
					},
					Expiration: &types.LifecycleExpiration{
						Days: aws.Int32(30),
					},
				},
			},
		},
	}

	_, err = client.PutBucketLifecycleConfiguration(context.Background(), input)
	if err != nil {
		log.Fatalf("❌ Failed to set lifecycle: %v", err)
	}
	log.Printf("✅ Lifecycle rule set: slip/ folder → delete after 30 days")

	// Verify
	out, err := client.GetBucketLifecycleConfiguration(context.Background(), &s3.GetBucketLifecycleConfigurationInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		log.Printf("⚠️ Verify failed: %v", err)
		return
	}
	log.Printf("📋 Active rules on bucket '%s':", bucket)
	for _, rule := range out.Rules {
		prefix := ""
		if rule.Filter != nil && rule.Filter.Prefix != nil {
			prefix = *rule.Filter.Prefix
		}
		days := int32(0)
		if rule.Expiration != nil && rule.Expiration.Days != nil {
			days = *rule.Expiration.Days
		}
		log.Printf("  - %s: prefix=%s, expire=%d days, status=%s",
			aws.ToString(rule.ID), prefix, days, rule.Status)
	}
}

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("❌ env %s not set", k)
	}
	return v
}
