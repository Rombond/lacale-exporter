# TODO

## 1. Use `json.Marshal` for login request body

Replace `fmt.Sprintf` string interpolation with a proper struct and `json.Marshal` to correctly escape special characters in email/password.

- Add a `loginRequest` struct
- Replace `fmt.Sprintf(`{"email":"%s",...}`)` with `json.Marshal(loginRequest{...})`
- Check the marshal error

## 2. Handle errors properly (don't discard with `_`)

Fix all ignored errors from `io.ReadAll` and `json.Marshal`:

- `doFlare()`: `body, _ := io.ReadAll(resp.Body)` and `payload, _ := json.Marshal(req)`
- `Login()`: `body, _ := io.ReadAll(resp.Body)`
- `FetchMetrics()`: `body, _ := io.ReadAll(resp.Body)`

## 3. Fix ticker goroutine leak

`startMetricsRefresher` creates a `time.NewTicker` that is never stopped — the goroutine leaks on shutdown.

- Call `defer ticker.Stop()` inside the goroutine
- Optionally add a `context.Context` for cancellation

## 4. Fix race conditions

Ensure consistent mutex usage for all shared fields accessed across goroutines:

- `server.lastMetrics` — locked in `scrapeAndRefresh`, but consider locking in `healthHandler` if exposed
- `LACALEClient` fields (`cfCookie`, `sessionCookie`, `userAgent`, `authenticated`) — mostly locked but verify all read/write paths are consistent under the race detector
- Run `go run -race` to confirm no data races

## 5. Replace custom `parseDuration` with `time.ParseDuration`

The custom suffix-matching parser is unnecessary — `time.ParseDuration("5m")`, `time.ParseDuration("1h")` work natively.

- Remove the custom loop with suffix structs
- Use `time.ParseDuration(s)` with the existing fallback

## 6. Improve cookie parsing robustness

The current `IndexByte('=')` approach for parsing `Set-Cookie` headers is fragile (doesn't handle whitespace, attributes before value, etc.).

- Split on `;` first, then split the first part on `=` with `SplitN(..., 2)`
- `strings.TrimSpace` both name and value
- Keep the prefix check for `session` / `__Host-`

## 7. Add graceful shutdown

Currently `SIGTERM` (Docker stop) kills the server and ticker goroutine abruptly.

- Import `context`, `os/signal`, `syscall`
- Add signal handler for `SIGINT` / `SIGTERM`
- Stop the ticker and gracefully shut down the Gin router
