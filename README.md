# La-Cale Exporter

A Go-based Prometheus exporter that scrapes your upload/download totals from the [La-Cale](https://la-cale.space) API and exposes them via the Prometheus HTTP interface.

La-Cale sits behind Cloudflare, so a [FlareSolverr](https://github.com/FlareSolverr/FlareSolverr) sidecar provides the `cf_clearance` cookie used for every request.

## Architecture

```
┌──────────────────┐     ┌──────────────┐     ┌─────────────┐
│ la-cale.space    │────▶│ Exporter     │◀────│ Prometheus  │
│ (/api/internal)  │     │ (scrapes &   │     │ (collects)  │
└──────────────────┘     │ exposes /    │     └─────────────┘
         ▲               │ metrics      │
         │               └──────┬───────┘
         │  cf_clearance        │
┌────────┴─────────┐            │
│ FlareSolverr     │◀───────────┘
└──────────────────┘
```

## Requirements

- Go 1.22+
- Docker (recommended, for the FlareSolverr sidecar)

## Configuration

Set these in a `.env` file next to the compose file:

| Variable | Description | Default |
|----------|-------------|---------|
| `LACALE_EMAIL` | Account email | *required* |
| `LACALE_PASSWORD` | Account password | *required* |
| `LACALE_API_BASE_URL` | Site host | `la-cale.space` |
| `FLARESOLVER_URL` | FlareSolverr API URL | `http://localhost:8191` |
| `PORT` | Server listen port | `9090` |
| `METRICS_PATH` | Metrics endpoint path | `/metrics` |
| `SCRAPE_INTERVAL` | Refresh rate | `5m` |

## Quick Start

```bash
# Prebuilt image (ghcr.io/rombond/lacale-exporter:latest) + FlareSolverr
docker compose up -d

# Dev build
docker compose -f docker-compose.dev.yml up --build

# Local (FlareSolverr must be reachable on FLARESOLVER_URL)
export LACALE_EMAIL=<email> LACALE_PASSWORD=<pass>
go build -o lacale_exporter . && ./lacale_exporter
```

## Endpoints

- `GET /metrics` - Prometheus metrics
- `GET /health` - Health check

```text
# TYPE lacale_total_uploaded_bytes gauge
lacale_total_uploaded_bytes 5.58255848392e+11
# TYPE lacale_total_downloaded_bytes gauge
lacale_total_downloaded_bytes 1.073741824e+09
```

## Prometheus Configuration

```yaml
scrape_configs:
  - job_name: 'lacale_exporter'
    static_configs:
      - targets: ['localhost:9090']
    metrics_path: /metrics
```

## Authentication Flow

1. Wait for FlareSolverr, then ask it for the site to get a `cf_clearance` cookie and its User-Agent
2. `POST /api/internal/auth/login` with email/password (no CSRF token), the `cf_clearance` cookie and the same User-Agent
3. Keep the session cookies from the response
4. `GET /api/internal/me` returns uploaded/downloaded bytes

The User-Agent must match the one FlareSolverr used, or Cloudflare rejects the cookie.

## License

[MIT](./LICENSE)
