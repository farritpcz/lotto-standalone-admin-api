// Package cloudflare — HTTP client สำหรับ Cloudflare API v4
//
// ใช้สำหรับ:
// - สร้าง/ลบ Zone (domain ลูกค้า)
// - เพิ่ม DNS record (A record ชี้ไป server, proxied ซ่อน IP)
// - ดึง zone info + nameservers
//
// Authentication:
//
//	Bearer token (API Token, ไม่ใช่ Global API Key)
//	Header: Authorization: Bearer <token>
//
// ⚠️ SECURITY:
//   - ไม่ log API token
//   - Token อ่านจาก ENV เท่านั้น (CF_API_TOKEN)
//   - ไม่ hardcode ในโค้ด
package cloudflare

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

const baseURL = "https://api.cloudflare.com/client/v4"

// Client เชื่อมต่อ Cloudflare API
type Client struct {
	apiToken  string
	accountID string
	http      *http.Client
}

// NewClient สร้าง Cloudflare client
// apiToken = Bearer token จาก CF dashboard
// accountID = Account ID จาก CF dashboard
func NewClient(apiToken, accountID string) *Client {
	return &Client{
		apiToken:  apiToken,
		accountID: accountID,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// =============================================================================
// Zone Management — สร้าง/ลบ/ดึง zone (domain)
// =============================================================================

// CreateZone สร้าง zone ใหม่ใน Cloudflare
//
// Flow:
//  1. POST /zones → สร้าง zone
//  2. CF จะ return nameservers ที่ลูกค้าต้องไปเปลี่ยน
//  3. เก็บ zone_id ไว้ใน DB สำหรับจัดการภายหลัง
//
// Parameters:
//   - domain: ชื่อ domain เช่น "huay99.com"
//
// Returns:
//   - ZoneResult: zone_id + nameservers
func (c *Client) CreateZone(domain string) (*ZoneResult, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"name":       domain,
		"account":    map[string]string{"id": c.accountID},
		"jump_start": false,  // ไม่ต้อง import DNS records เก่า
		"type":       "full", // full zone (ลูกค้าเปลี่ยน NS มาที่เรา)
	})

	resp, err := c.doRequest("POST", "/zones", body)
	if err != nil {
		return nil, fmt.Errorf("สร้าง zone %s ไม่สำเร็จ: %w", domain, err)
	}

	var result cfResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("parse response ไม่สำเร็จ: %w", err)
	}

	if !result.Success {
		// ⭐ เช็คว่า zone มีอยู่แล้ว (error code 1061)
		for _, e := range result.Errors {
			if e.Code == 1061 {
				log.Printf("[CF] Zone %s มีอยู่แล้ว — ดึง zone info แทน", domain)
				return c.GetZone(domain)
			}
		}
		return nil, fmt.Errorf("CF API error: %v", result.Errors)
	}

	// Parse result.Result → ZoneResult
	zoneData, err := json.Marshal(result.Result)
	if err != nil {
		return nil, fmt.Errorf("marshal zone data ไม่สำเร็จ: %w", err)
	}
	var zone zoneResponse
	if err := json.Unmarshal(zoneData, &zone); err != nil {
		return nil, fmt.Errorf("parse zone data ไม่สำเร็จ: %w", err)
	}

	log.Printf("[CF] ✅ สร้าง zone สำเร็จ: %s (ID: %s) NS: %v", domain, zone.ID, zone.NameServers)

	return &ZoneResult{
		ZoneID:      zone.ID,
		Domain:      zone.Name,
		NameServers: zone.NameServers,
		Status:      zone.Status,
	}, nil
}

// GetZone ดึง zone info จาก domain name
//
// ใช้เมื่อ:
//   - เช็คว่า zone มีอยู่แล้วหรือยัง
//   - ดึง nameservers ของ zone ที่สร้างไปแล้ว
func (c *Client) GetZone(domain string) (*ZoneResult, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/zones?name=%s", domain), nil)
	if err != nil {
		return nil, fmt.Errorf("ดึง zone %s ไม่สำเร็จ: %w", domain, err)
	}

	var result cfListResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("parse response ไม่สำเร็จ: %w", err)
	}

	if !result.Success || len(result.Result) == 0 {
		return nil, fmt.Errorf("ไม่พบ zone: %s", domain)
	}

	zone := result.Result[0]
	return &ZoneResult{
		ZoneID:      zone.ID,
		Domain:      zone.Name,
		NameServers: zone.NameServers,
		Status:      zone.Status,
	}, nil
}

// DeleteZone ลบ zone ออกจาก Cloudflare
//
// ใช้เมื่อ:
//   - ลบเว็บลูกค้า
//   - ปิดเว็บถาวร
func (c *Client) DeleteZone(zoneID string) error {
	_, err := c.doRequest("DELETE", fmt.Sprintf("/zones/%s", zoneID), nil)
	if err != nil {
		return fmt.Errorf("ลบ zone %s ไม่สำเร็จ: %w", zoneID, err)
	}
	log.Printf("[CF] 🗑️ ลบ zone สำเร็จ: %s", zoneID)
	return nil
}

// =============================================================================
// DNS Record Management — เพิ่ม/ลบ DNS records
// =============================================================================

// AddDNSRecord เพิ่ม DNS record ใน zone
//
// Parameters:
//   - zoneID: Zone ID จาก CreateZone
//   - recordType: "A", "AAAA", "CNAME" etc.
//   - name: subdomain เช่น "www", "@" (root), หรือ full domain
//   - content: ค่า เช่น IP address สำหรับ A record
//   - proxied: true = Cloudflare proxy (ซ่อน IP ☁️), false = DNS only
func (c *Client) AddDNSRecord(zoneID, recordType, name, content string, proxied bool) error {
	body, _ := json.Marshal(map[string]interface{}{
		"type":    recordType,
		"name":    name,
		"content": content,
		"proxied": proxied,
		"ttl":     1, // 1 = auto (CF จัดการ)
	})

	resp, err := c.doRequest("POST", fmt.Sprintf("/zones/%s/dns_records", zoneID), body)
	if err != nil {
		return fmt.Errorf("เพิ่ม DNS record ไม่สำเร็จ: %w", err)
	}

	var result cfResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return fmt.Errorf("parse response ไม่สำเร็จ: %w", err)
	}

	if !result.Success {
		// ⭐ เช็คว่า record มีอยู่แล้ว (error code 81057)
		for _, e := range result.Errors {
			if e.Code == 81057 {
				log.Printf("[CF] DNS record %s %s มีอยู่แล้ว — ข้าม", recordType, name)
				return nil
			}
		}
		return fmt.Errorf("CF API error: %v", result.Errors)
	}

	proxyIcon := "☁️"
	if !proxied {
		proxyIcon = "⚡"
	}
	log.Printf("[CF] ✅ เพิ่ม DNS: %s %s → %s %s", recordType, name, content, proxyIcon)
	return nil
}

// =============================================================================
// SSL Settings — ตั้งค่า SSL mode
// =============================================================================

// SetSSLMode ตั้งค่า SSL mode สำหรับ zone
//
// Modes:
//   - "flexible": CF → HTTP → origin (ไม่ต้อง cert บน server)
//   - "full": CF → HTTPS → origin (ต้องมี cert, self-signed OK)
//   - "full_strict": CF → HTTPS → origin (ต้อง valid cert)
func (c *Client) SetSSLMode(zoneID, mode string) error {
	body, _ := json.Marshal(map[string]interface{}{
		"value": mode,
	})

	_, err := c.doRequest("PATCH", fmt.Sprintf("/zones/%s/settings/ssl", zoneID), body)
	if err != nil {
		return fmt.Errorf("ตั้งค่า SSL mode ไม่สำเร็จ: %w", err)
	}

	log.Printf("[CF] ✅ SSL mode → %s (zone: %s)", mode, zoneID)
	return nil
}

// =============================================================================
// Internal — HTTP helpers
// =============================================================================

func (c *Client) doRequest(method, path string, body []byte) ([]byte, error) {
	url := baseURL + path

	var req *http.Request
	var err error
	if body != nil {
		req, err = http.NewRequest(method, url, bytes.NewBuffer(body))
	} else {
		req, err = http.NewRequest(method, url, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("สร้าง request ไม่สำเร็จ: %w", err)
	}

	// ⭐ Bearer token auth (ไม่ log token)
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Content-Type", "application/json")

	log.Printf("[CF] %s %s", method, path)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("CF request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("อ่าน response ไม่สำเร็จ: %w", err)
	}

	log.Printf("[CF] %s %s → %d (%d bytes)", method, path, resp.StatusCode, len(respBody))

	return respBody, nil
}

// =============================================================================
// Types — Cloudflare API response structures
// =============================================================================

// ZoneResult ผลลัพธ์จากการสร้าง/ดึง zone
type ZoneResult struct {
	ZoneID      string   `json:"zone_id"`      // Cloudflare Zone ID
	Domain      string   `json:"domain"`       // Domain name
	NameServers []string `json:"name_servers"` // Nameservers ที่ลูกค้าต้องเปลี่ยน
	Status      string   `json:"status"`       // pending, active, moved, deactivated
}

// cfResponse — generic Cloudflare API response
type cfResponse struct {
	Success bool            `json:"success"`
	Errors  []cfError       `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

// cfListResponse — Cloudflare API response ที่ result เป็น array
type cfListResponse struct {
	Success bool           `json:"success"`
	Errors  []cfError      `json:"errors"`
	Result  []zoneResponse `json:"result"`
}

// cfError — Cloudflare error object
type cfError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// zoneResponse — zone data จาก CF API
type zoneResponse struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	NameServers []string `json:"name_servers"`
}
