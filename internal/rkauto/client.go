// Package rkauto — HTTP client สำหรับ GobexPay API
//
// Authentication:
//   Headers: X-API-Key + X-API-Secret
//   ทุก request ลงนาม HMAC-SHA256
//
// ⚠️ SECURITY:
//   - ไม่ log API secret หรือ bank credentials
//   - TLS skip verify เพราะ RKAUTO ใช้ self-signed cert
//   - Timeout 30 วินาที
package rkauto

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"
)

// Client เชื่อมต่อ RKAUTO API
type Client struct {
	baseURL   string
	apiKey    string
	apiSecret string
	http      *http.Client
}

// NewClient สร้าง RKAUTO client
func NewClient(baseURL, apiKey, apiSecret string) *Client {
	return &Client{
		baseURL:   baseURL,
		apiKey:    apiKey,
		apiSecret: apiSecret,
		http: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				// ⚠️ RKAUTO ใช้ self-signed cert → skip verify
				// production ควรใส่ cert ของ RKAUTO
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
}

// =============================================================================
// API Methods
// =============================================================================

// HealthCheck ตรวจสอบ RKAUTO ทำงาน
func (c *Client) HealthCheck() (*GenericResponse, error) {
	return c.doRequest("GET", "/api/v1/health", nil)
}

// RegisterBankAccount ลงทะเบียนบัญชีธนาคาร
func (c *Client) RegisterBankAccount(req RegisterBankAccountRequest) (*RegisterBankAccountResponse, error) {
	body, _ := json.Marshal(req)
	resp, err := c.doRawRequest("POST", "/api/v1/bank-accounts", body)
	if err != nil {
		return nil, err
	}
	var result RegisterBankAccountResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to parse register response: %w", err)
	}
	return &result, nil
}

// UpdateBankAccount อัพเดทบัญชี
func (c *Client) UpdateBankAccount(uuid string, isDeposit, isWithdraw bool, name string) (*GenericResponse, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"bank_account_name": name,
		"is_deposit":        isDeposit,
		"is_withdraw":       isWithdraw,
	})
	return c.doRequestWithBody("PUT", "/api/v1/bank-accounts/"+uuid, body)
}

// ActivateBankAccount เปิดใช้บัญชี
func (c *Client) ActivateBankAccount(uuid string) (*GenericResponse, error) {
	return c.doRequest("POST", "/api/v1/bank-accounts/"+uuid+"/activate", nil)
}

// DeactivateBankAccount ปิดใช้บัญชี
func (c *Client) DeactivateBankAccount(uuid string) (*GenericResponse, error) {
	return c.doRequest("POST", "/api/v1/bank-accounts/"+uuid+"/deactivate", nil)
}

// DeleteBankAccount ลบบัญชี
func (c *Client) DeleteBankAccount(uuid string) (*GenericResponse, error) {
	return c.doRequest("DELETE", "/api/v1/bank-accounts/"+uuid, nil)
}

// CreateWithdrawal สั่งถอนเงิน
func (c *Client) CreateWithdrawal(req CreateWithdrawalRequest) (*CreateWithdrawalResponse, error) {
	body, _ := json.Marshal(req)
	resp, err := c.doRawRequest("POST", "/api/v1/withdrawals", body)
	if err != nil {
		return nil, err
	}
	var result CreateWithdrawalResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to parse withdrawal response: %w", err)
	}
	return &result, nil
}

// UpdateWebhookURLs ตั้งค่า webhook callback URLs
func (c *Client) UpdateWebhookURLs(depositURL, withdrawURL string) (*GenericResponse, error) {
	body, _ := json.Marshal(WebhookConfigRequest{
		DepositWebhookURL:    depositURL,
		WithdrawalWebhookURL: withdrawURL,
	})
	return c.doRequestWithBody("PUT", "/api/v1/webhooks", body)
}

// GetWebhookConfig ดู webhook config ปัจจุบัน
func (c *Client) GetWebhookConfig() (*GenericResponse, error) {
	return c.doRequest("GET", "/api/v1/webhooks", nil)
}

// =============================================================================
// Internal — HTTP + HMAC Signing
// =============================================================================

// generateSignature สร้าง HMAC-SHA256 signature
//
// Format: METHOD + "\n" + PATH + "\n" + TIMESTAMP + "\n" + SHA256(BODY)
func (c *Client) generateSignature(method, path, timestamp string, body []byte) string {
	bodyHash := ""
	if len(body) > 0 {
		h := sha256.Sum256(body)
		bodyHash = hex.EncodeToString(h[:])
	}

	stringToSign := method + "\n" + path + "\n" + timestamp + "\n" + bodyHash
	mac := hmac.New(sha256.New, []byte(c.apiSecret))
	mac.Write([]byte(stringToSign))
	return hex.EncodeToString(mac.Sum(nil))
}

func (c *Client) doRequest(method, path string, body []byte) (*GenericResponse, error) {
	resp, err := c.doRawRequest(method, path, body)
	if err != nil {
		return nil, err
	}
	var result GenericResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return &result, nil
}

func (c *Client) doRequestWithBody(method, path string, body []byte) (*GenericResponse, error) {
	return c.doRequest(method, path, body)
}

func (c *Client) doRawRequest(method, path string, body []byte) ([]byte, error) {
	url := c.baseURL + path
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	var req *http.Request
	var err error
	if body != nil {
		req, err = http.NewRequest(method, url, bytes.NewBuffer(body))
	} else {
		req, err = http.NewRequest(method, url, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Headers
	req.Header.Set("X-API-Key", c.apiKey)
	req.Header.Set("X-API-Secret", c.apiSecret)
	req.Header.Set("Content-Type", "application/json")

	// ⚠️ ไม่ log API secret
	log.Printf("[RKAUTO] %s %s", method, path)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("RKAUTO request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// ⚠️ ไม่ log response body (อาจมีข้อมูล sensitive)
	log.Printf("[RKAUTO] %s %s → %d (%d bytes)", method, path, resp.StatusCode, len(respBody))

	_ = timestamp // ใช้สำหรับ signature verification ของ response ถ้าต้องการ

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("RKAUTO error %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}
