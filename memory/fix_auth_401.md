---
name: fix_auth_401
description: Fix auth issues by using browser-like headers matching the real client
type: feedback
---

Rule: Fix "401 Unauthorized" error by using browser-like headers (User-Agent, Origin, Referer, Sec-Fetch-*) matching the real web client.

**Why:** La-Cale API requires browser-like headers on all requests. No CSRF token needed — auth is via direct POST to /api/internal/auth/login with email/password.

**How to apply:**
1. POST to /api/internal/auth/login with JSON body: `{"email": "...", "password": "...", "formLoadedAt": <unix_ms>}`
2. Use browser-like headers (User-Agent, Content-Type, Origin, Referer, Sec-Fetch-*)
3. Store session cookies from response
4. Include session cookies on /api/internal/me requests
