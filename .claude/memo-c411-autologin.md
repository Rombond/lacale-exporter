---
name: lacale_auto_login
description: La-Cale auto-login using email/password POST to internal auth endpoint
type: user
---

## Goal: Auto-login without human/browser interaction

## Auth Flow: Direct POST to internal API

La-Cale uses a simpler auth flow — no CSRF token needed:

### Steps:
1. **POST /api/internal/auth/login** with:
   - JSON body: `{"email": "...", "password": "...", "formLoadedAt": <unix_ms>}`
   - Browser-like headers (User-Agent, Origin, Referer, Sec-Fetch-*)
2. **Store session cookies** (JWT + cf_clearance) from response

### Code pattern:
```go
// Build login payload
payload, _ := json.Marshal(map[string]interface{}{
    "email": email,
    "password": password,
    "formLoadedAt": time.Now().UnixMilli(),
})

// POST to internal auth endpoint
req, _ := http.NewRequest(http.MethodPost, cfg.BaseURL+"/api/internal/auth/login", bytes.NewReader(payload))
req.Header.Set("Content-Type", "application/json")
req.Header.Set("User-Agent", "Mozilla/5.0 ...")

resp, _ := client.Do(req)
// Store cookies from response for subsequent /api/internal/me calls
```

## Metrics Endpoint

- **GET /api/internal/me** with session cookies
- Response format varies — code handles multiple possible structures
- Extracts `uploaded`, `downloaded`, `username` fields

## Files Modified

- `main.go`: LACALEClient with cookie jar, direct auth (no CSRF)
- `Dockerfile`: Alpine-based with Prometheus exporter
- `docker-compose.dev.yml`: Dev compose config
- `CLAUDE.md`: Updated architecture docs
