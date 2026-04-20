# Deploy (nginx + Cloudflare) — admin-api

> Last updated: 2026-04-20 (v1 initial — starter rule)
> Related code: `internal/handler/deploy.go`, `internal/cloudflare/`, `internal/handler/downline_handler.go` (CreateDownlineNode triggers deploy)

## 🎯 Purpose
Auto-deploy เว็บใหม่เมื่อสร้าง agent_node พร้อม domain — เขียน nginx config + reload + สร้าง Cloudflare zone (ซ่อน IP จริง)

## 📋 Rules
1. ไม่ใช่ HTTP handler โดยตรง — เป็น utility package ที่ถูกเรียกจาก `CreateDownlineNode`/`NodeCreateChild`
2. Flow: validate domain → write `/etc/nginx/sites-enabled/{domain}.conf` → `nginx -t` → `nginx -s reload`
3. Nginx template ใช้ `X-Forwarded-Host` เพื่อให้ member-api detect agent จาก domain (multi-tenant routing)
4. ห้าม user แก้ไฟล์ nginx config เอง — comment ในไฟล์บอกไว้ว่า auto-generated
5. ถ้ามี `CF_API_TOKEN` ใน env → สร้าง CF zone ด้วย → return nameservers ให้ user ชี้ DNS (ดู `cloudflare_integration.md`)
6. `validDomain()` — regex กันชื่อ domain ไม่ถูกต้อง / path traversal
7. Remove: ต้องลบทั้ง nginx file + CF zone (ปัจจุบัน CF zone ลบเองจาก dashboard — TODO)

## 🌐 Endpoints
- ไม่ expose เป็น REST endpoint โดยตรง — ถูกเรียกผ่าน downline node create/update/delete

## ⚠️ Edge Cases
- `nginx -t` fail → ไม่ reload, return error, rollback file
- Domain ซ้ำ (มีไฟล์อยู่แล้ว) → overwrite (จงใจ — รองรับ re-deploy)
- Server ไม่มี nginx / ไม่มี permission → return friendly error
- WIP — expand เมื่อ deploy pipeline เสถียร

## 🔗 Related
- Cloudflare: [cloudflare_integration.md](./cloudflare_integration.md)
- Nodes: [agent_node_management.md](./agent_node_management.md), [downline.md](./downline.md), [node_portal.md](./node_portal.md)
- Memory: `cloudflare_zone_auto.md` (zone creation flow)

## 📝 Change Log
- 2026-04-20: v1 initial skeleton
