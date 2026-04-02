// Package storage — Cloudflare R2 upload/delete
//
// R2 ใช้ S3-compatible API → ใช้ AWS SDK ได้เลย
//
// Config ที่ต้องการ (env):
//   R2_ACCOUNT_ID    — Cloudflare Account ID
//   R2_ACCESS_KEY    — R2 Access Key ID
//   R2_SECRET_KEY    — R2 Secret Access Key
//   R2_BUCKET        — Bucket name (เช่น "lotto-images")
//   R2_PUBLIC_URL    — Public URL (เช่น "https://pub-xxx.r2.dev" หรือ custom domain)
package storage

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
)

// R2Client จัดการ upload/delete ไฟล์บน Cloudflare R2
type R2Client struct {
	client    *s3.Client
	bucket    string
	publicURL string // URL สาธารณะสำหรับเข้าถึงไฟล์
}

// NewR2Client สร้าง R2 client
func NewR2Client(accountID, accessKey, secretKey, bucket, publicURL string) (*R2Client, error) {
	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountID)

	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
		config.WithRegion("auto"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load R2 config: %w", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	// ทดสอบ connection
	_, err = client.HeadBucket(context.Background(), &s3.HeadBucketInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		log.Printf("⚠️ R2 bucket '%s' check failed (may not exist yet): %v", bucket, err)
	}

	return &R2Client{
		client:    client,
		bucket:    bucket,
		publicURL: strings.TrimRight(publicURL, "/"),
	}, nil
}

// Upload อัพโหลดไฟล์ไป R2
//
// folder: subfolder เช่น "lottery", "banner"
// filename: ชื่อไฟล์เดิม (จะถูก rename เป็น UUID)
// contentType: MIME type เช่น "image/png"
// body: io.Reader ของไฟล์
//
// Returns: public URL ของไฟล์ที่อัพโหลด
func (r *R2Client) Upload(folder, filename, contentType string, body io.Reader) (string, error) {
	// สร้างชื่อไฟล์ใหม่ (UUID) ป้องกัน path traversal + collision
	ext := ""
	if idx := strings.LastIndex(filename, "."); idx >= 0 {
		ext = filename[idx:]
	}
	key := fmt.Sprintf("%s/%s_%d%s", folder, uuid.New().String()[:12], time.Now().Unix(), ext)

	_, err := r.client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      aws.String(r.bucket),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return "", fmt.Errorf("R2 upload failed: %w", err)
	}

	publicURL := fmt.Sprintf("%s/%s", r.publicURL, key)
	log.Printf("[R2] Uploaded: %s → %s", filename, publicURL)
	return publicURL, nil
}

// Delete ลบไฟล์จาก R2
func (r *R2Client) Delete(key string) error {
	// แปลง URL กลับเป็น key
	if strings.HasPrefix(key, r.publicURL) {
		key = strings.TrimPrefix(key, r.publicURL+"/")
	}

	_, err := r.client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("R2 delete failed: %w", err)
	}

	log.Printf("[R2] Deleted: %s", key)
	return nil
}

// IsConfigured ตรวจว่า R2 ถูก config ครบหรือไม่
func (r *R2Client) IsConfigured() bool {
	return r != nil && r.client != nil && r.bucket != ""
}
