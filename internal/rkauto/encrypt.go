// Package rkauto — AES-256 encryption สำหรับ bank credentials
//
// ⚠️ SECURITY: bank username/password ต้องเข้ารหัสก่อนเก็บ DB
// ใช้ AES-256-GCM (authenticated encryption)
package rkauto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

// Encrypt เข้ารหัส plaintext ด้วย AES-256-GCM
//
// key: 32 bytes (hex string จาก env RKAUTO_ENCRYPTION_KEY)
// Returns: base64 encoded ciphertext
func Encrypt(plaintext, key string) (string, error) {
	if len(key) < 32 {
		// pad key to 32 bytes
		for len(key) < 32 {
			key += "0"
		}
	}

	block, err := aes.NewCipher([]byte(key[:32]))
	if err != nil {
		return "", fmt.Errorf("cipher error: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("gcm error: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("nonce error: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt ถอดรหัส ciphertext ด้วย AES-256-GCM
func Decrypt(encrypted, key string) (string, error) {
	if len(key) < 32 {
		for len(key) < 32 {
			key += "0"
		}
	}

	data, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", fmt.Errorf("base64 decode error: %w", err)
	}

	block, err := aes.NewCipher([]byte(key[:32]))
	if err != nil {
		return "", fmt.Errorf("cipher error: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("gcm error: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt error: %w", err)
	}

	return string(plaintext), nil
}
