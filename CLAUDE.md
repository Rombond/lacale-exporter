# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# Architecture Overview

## High-Level Summary
This is a **Go-based Prometheus exporter** that scrapes user metrics from the La-Cale API and exposes them via the Prometheus HTTP interface. The application uses:
- Gin for HTTP routing
- Prometheus client library for metrics exposition
- **FlareSolverr** (Docker sidecar) to bypass Cloudflare protection
- Direct HTTP requests with `cf_clearance` cookie for API calls

## Key Components

### 1. FlareSolverr — Cloudflare Bypass (Docker sidecar)
- Runs as a separate container alongside the exporter
- Uses a real Chromium browser to get a valid `cf_clearance` cookie
- The exporter calls FlareSolverr once at startup to get the cookie + User-Agent
- Cookie is reused for all subsequent API calls

### 2. Authentication Layer (`main.go`)
- `LACALEClient` struct manages `cf_clearance` cookie and HTTP sessions
- **No CSRF token** — La-Cale uses a direct POST auth flow
- POST directly to `/api/internal/auth/login` with email/password
- Uses `cf_clearance` cookie from FlareSolverr + matching User-Agent
- Stores session cookies from login response
- Uses mutex for thread-safe cookie access
- Auto-authenticates on startup if credentials are provided via environment variables

### 3. Metrics Collection (`main.go`)
- `FetchMetrics()` calls `/api/internal/me` with session + `cf_clearance` cookies
- Returns `UserMetrics` struct (uploaded/downloaded bytes, username)
- Handles multiple possible response formats from the API
- Prometheus gauges registered to track these metrics over time

### 4. HTTP Server (`main.go`)
- Serves `/metrics` endpoint that exposes Prometheus gauges via promhttp.Handler()
- Health check at `/health` for monitoring tools
- Runs as Gin server on configurable port (default: 9090)

### 5. Configuration (via `.env`)
```
LACALE_API_BASE_URL   # La-Cale API host (e.g., la-cale.space)
LACALE_EMAIL          # API credentials (required for auth)
LACALE_PASSWORD       # API credentials (required for auth)
PORT                  # Server port (default: 9090)
METRICS_PATH          # Metrics path (default: /metrics)
SCRAPE_INTERVAL       # How often to refresh metrics (e.g., 5m)
FLARESOLVER_URL       # FlareSolverr API URL (default: http://localhost:8191)
```

## Authentication Flow (AUTO-LOGIN)

### Required Steps:
1. **Wait for FlareSolverr** — poll `/v1` until it responds
2. **Get cf_clearance** — call FlareSolverr `request.get` for `/login` page
3. **Login** — POST to `/api/internal/auth/login` with:
   - `cf_clearance` cookie from step 2
   - Matching User-Agent from FlareSolverr
   - JSON body: `{"email": "...", "password": "...", "formLoadedAt": <unix_ms>}`
4. **Store session cookies** from login response for subsequent API calls

### Why FlareSolverr?
La-Cale is protected by Cloudflare. The `cf_clearance` cookie is required to pass Cloudflare's security challenge. FlareSolverr uses a real Chromium browser to solve the challenge and returns the cookie, which is then used for all API requests.

### Important Details:
- The User-Agent from FlareSolverr must match the one used in API calls
- The `cf_clearance` cookie is used for ALL requests (login + metrics)
- Login response may set session cookies (captured from `Set-Cookie` headers)
- No TLS fingerprinting issues — plain `http.Client` works with the cookie

## Data Flow
1. Startup: Load config → wait for FlareSolverr → get `cf_clearance` → login → start server
2. Background: Periodically scrape `/api/internal/me` → update Prometheus gauges
3. `/metrics` requests: Expose current gauge values via promhttp.Handler()
4. External Prometheus server scrapes `/metrics` to collect data

## API Endpoints

### FlareSolverr
```
POST http://flaresolverr:8191/v1
Body: {"cmd": "request.get", "url": "https://la-cale.space/login"}
```

### Login
```
POST https://<base>/api/internal/auth/login
Content-Type: application/json
Cookie: cf_clearance=<from flaresolverr>
Body: {"email": "user@example.com", "password": "...", "formLoadedAt": <unix_ms>}
```

### User Metrics
```
GET https://<base>/api/internal/me
Cookie: cf_clearance=<token>; session=<jwt>
```

## Docker Services

| Service | Image | Port | Purpose |
|---------|-------|------|---------|
| app | Built from Dockerfile | 9090 | Prometheus exporter |
| flaresolverr | ghcr.io/flaresolverr/flaresolverr:latest | 8191 | Cloudflare bypass |

## Prometheus Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `lacale_total_uploaded_bytes` | Gauge | Total bytes uploaded by the user |
| `lacale_total_downloaded_bytes` | Gauge | Total bytes downloaded by the user |

# Commands

## Building and Running

```bash
# In development, rebuild after code changes
docker compose -f docker-compose.dev.yml up --build -d

# View logs
docker compose -f docker-compose.dev.yml logs -f app

# Stop
docker compose -f docker-compose.dev.yml down
```

## Common Tasks

### View metrics locally
```bash
curl http://localhost:9090/metrics
```

### Health check
```bash
curl http://localhost:9090/health
```

### Access logs
```bash
docker compose -f docker-compose.dev.yml logs -f app
docker compose -f docker-compose.dev.yml logs -f flaresolverr
```
