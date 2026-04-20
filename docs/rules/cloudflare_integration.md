# Cloudflare Integration (Auto Zone + DNS ซ่อน IP)

> Last updated: 2026-04-20
> Related code: `internal/cloudflare/client.go`, `internal/handler/deploy.go:182` (DeployCloudflareZone), `internal/handler/downline_handler.go:431` (wire-up)
> Related config: `CF_API_TOKEN`, `CF_ACCOUNT_ID` (env) — main.go:96

## 🎯 Purpose
เมื่อสร้าง agent_node พร้อม `domain` → สร้าง Cloudflare Zone อัตโนมัติ + เพิ่ม A record แบบ proxied (ซ่อน IP server จริง) + ตั้ง SSL flexible → บอก nameservers ให้ลูกค้าไปเปลี่ยนที่ registrar

## 📋 Rules
1. ต้องตั้ง env `CF_API_TOKEN` + `CF_ACCOUNT_ID` — ถ้าไม่ตั้ง → `h.CFClient = nil` ระบบ fallback ชี้ DNS ตรงด้วย `server_ip`
2. Token ต้องมี scope: Zone:Edit + DNS:Edit + SSL/TLS:Edit ของ account
3. Flow auto deploy:
   1. `CreateZone(domain)` — idempotent (ถ้ามี zone เดิม API คืน error → client ควรดึง zone เดิมแทน)
   2. `AddDNSRecord("A", domain, serverIP, proxied=true)` — `proxied=true` บังคับ (ไม่เช่นนั้น IP โผล่)
   3. `AddDNSRecord("A", "www."+domain, serverIP, true)`
   4. `SetSSLMode(zoneID, "flexible")` — ไม่ต้องมี cert บน origin
4. Response กลับ frontend: `nameservers` (list) + `cf_zone_id` + ไม่แสดง `server_ip`
5. ถ้า CF deploy fail → ไม่ return error ออก — fallback โชว์ `server_ip` เดิม (ไม่ block การสร้าง node)
6. Zone ID เก็บใน `agent_nodes.cf_zone_id` เพื่อใช้ลบ zone ตอนลบ node (Status: 🚧 ยังไม่ได้ implement `DeleteZone` wiring ใน `DeleteDownlineNode`)
7. การเพิ่ม A record ไม่ critical — ถ้า fail แค่ log warning (zone ยังใช้ได้)
8. Log ออก 3 ระดับ: ✅ success, ⚠️ partial (บางขั้นล้ม), ❌ fail ทั้งหมด

## 🔄 Flow (diagram)
```
CreateDownlineNode(domain != "")
  ↓
DeployNginxConfig (เขียน /etc/nginx/sites-enabled/{domain}.conf + reload)
  ↓
if h.CFClient != nil:
    DeployCloudflareZone(cfClient, domain, serverIP)
      → CreateZone → AddDNSRecord × 2 → SetSSLMode
    → ถ้าสำเร็จ: return nameservers + zone_id (ไม่โชว์ serverIP)
    → ถ้า fail: log + ใช้ flow เดิม (server_ip)
UPDATE agent_nodes.cf_zone_id
```

## 🌐 API Endpoints (ทางอ้อม — ไม่มี endpoint CF ตรง)
- `POST /api/v1/downline/nodes` — สร้าง node + auto CF zone
- `DELETE /api/v1/downline/nodes/:id` — Status: 🚧 planned — ยังไม่ลบ zone อัตโนมัติ ต้องลบใน CF dashboard เอง

## ⚠️ Edge Cases
- Token ผิด/หมดอายุ → log `❌ CF Deploy: สร้าง zone ... ไม่สำเร็จ` → fallback server_ip
- Zone มีอยู่แล้ว (domain เคยสร้าง) → CreateZone error → ปัจจุบัน treated as fail (ควร enhance ให้ fetch zone เดิม)
- Domain หลายชั้น (subdomain) → ต้องสร้าง zone ของ apex domain ก่อน; ปัจจุบันรับ domain ตรงที่ส่งมา
- CF_ACCOUNT_ID มี 8 chars พิมพ์ในลอก (main.go:98) — ไม่ logout full ID

## 🔗 Source of Truth
- Client: `internal/cloudflare/client.go`
- Deploy function: `internal/handler/deploy.go:182`
- Wire-up: `internal/handler/downline_handler.go:431-446`
- Config gate: `cmd/server/main.go:96`
- Memory: `cloudflare_zone_auto.md`

## 📝 Change Log
- 2026-04-20: Initial — CreateZone + DNS proxied + SSL flexible + fallback; DeleteZone wiring marked planned
