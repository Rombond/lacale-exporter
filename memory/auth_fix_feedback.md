---
name: auth_fix_feedback
description: Auth requires browser-like headers and direct POST to internal auth endpoint
type: feedback
---

Rule: Fix "401 Unauthorized" by using browser-like headers (User-Agent, Origin, Referer, Sec-Fetch-*) matching the real web client.

Why: The La-Cale API requires browser-like headers on all requests. Auth is via direct POST to /api/internal/auth/login — no CSRF token needed.

How to apply: When fixing authentication issues, implement: 1) POST /api/internal/auth/login with JSON body {email, password, formLoadedAt} and browser headers, 2) Store session cookies from response, 3) Include cookies on subsequent API calls. Never make API calls without proper headers and stored session cookies.
