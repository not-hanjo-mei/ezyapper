# WebUI Reference

> [!IMPORTANT]
> **There is no JSON REST API.** The WebUI is a server-rendered HTML dashboard. Earlier versions of these docs described a `/api/*` JSON surface — that surface never shipped. If you need to manage the bot programmatically, edit `config.yaml` and restart, or use the dashboard's HTML forms.

EZyapper's WebUI is a small admin dashboard built directly on `net/http`. It's optional, disabled by default, and considered experimental — keep `operations.web.enabled: false` for production unless you explicitly need it.

---

## Enabling the WebUI

```yaml
operations:
  web:
    enabled: true
    port: 8080
    username: "admin"
    password: "CHANGE_ME"
    memories_page_limit: 50
    session_ttl_min: 30
    session_cleanup_interval_min: 5
    stats_query_timeout_sec: 5
    log_default_lines: 100
    log_max_lines: 1000
    log_max_read_bytes: 1048576
```

When `enabled: false`, every WebUI field above is skipped during validation, and the HTTP listener never starts. Setting `enabled: true` requires every other field to be present — strict config rules apply here too.

---

## Authentication

Basic Auth username + password from `operations.web.*` are the only credentials. There is no user database, no roles, no per-tenant isolation — the whole dashboard is "admin or nothing."

| Mechanism | Detail |
|-----------|--------|
| Login form | `GET /login` — renders the form. `POST /login` — validates credentials. |
| Session cookie | `__Host-session_id`, HttpOnly + Secure + SameSite=Strict, random 32-byte hex ID. |
| TTL | `session_ttl_min` minutes. Background cleanup runs every `session_cleanup_interval_min`. |
| Login rate limit | 5 attempts per minute per client IP (sliding window, in-memory). |
| Credential check | Constant-time comparison via `crypto/subtle`. |
| Logout | `POST /logout` (CSRF-checked). Deletes session and clears the cookie. |

Sessions live in memory only — restart the bot and everyone is logged out.

---

## CSRF Protection

State-changing requests (`POST`, `PUT`, `DELETE`) require a CSRF token.

- Pattern: Double-Submit Cookie with HMAC-SHA256 signature.
- Cookie: `csrf_token` — non-HttpOnly so JS-driven forms can read it. `Secure` + `SameSite=Strict`.
- Form field: `csrf_token` — must match the cookie. Both raw token and HMAC signature are verified.
- Excluded path: `POST /login` (you can't have a session yet).
- All HTML pages embed a hidden `csrf_token` input automatically.

If a request is rejected for CSRF reasons you'll see a 403 with a brief message.

---

## Routes

The bot registers exactly these routes (`internal/web/server.go:setupRoutes`). Anything else returns 404.

| Path | Methods | Auth | Renders | Notes |
|------|---------|------|---------|-------|
| `/static/...` | GET | none | static asset | CSS/JS served from disk (see "Static Assets") |
| `/login` | GET, POST | none | `login` template | GET shows form. POST authenticates, sets session, redirects to `/`. |
| `/logout` | POST | session + CSRF | redirect | Clears the session cookie. |
| `/` | GET | session | `dashboard` template | Stats overview |
| `/config` | GET, POST | session + CSRF | `config` template | View/update runtime config. POST validates, applies, and persists to `config.yaml`. |
| `/channels` | GET | session | `channels` template | View blacklist + whitelist |
| `/channels/blacklist/add` | POST | session + CSRF | redirect | Add an entry to blacklist |
| `/channels/blacklist/remove` | POST | session + CSRF | redirect | Remove an entry from blacklist |
| `/channels/whitelist/add` | POST | session + CSRF | redirect | Add an entry to whitelist |
| `/channels/whitelist/remove` | POST | session + CSRF | redirect | Remove an entry from whitelist |
| `/memories` | GET | session | `memories` template | List/search a user's memories. Query params: `userID`, `q`. |
| `/memories/delete` | POST | session + CSRF | redirect | Delete one memory by ID (ownership-verified) |
| `/profiles` | GET | session | `profiles` template | View/edit a user's profile. Query param: `userID`. |
| `/profiles/update` | POST | session + CSRF | redirect | Update display name, traits, facts, etc. |
| `/plugins` | GET | session | `plugins` template | List loaded plugins |
| `/plugins/toggle` | POST | session + CSRF | redirect | Enable/disable a plugin (refreshes tool registrations) |
| `/logs` | GET | session | `logs` template | Tail the log file. Query param: `lines`. |

There is **no** `/health` endpoint — health checks should hit the bot's process directly (e.g., supervisor, container probe, or systemd `MainPID`). The Docker image's `HEALTHCHECK` currently does request `/health` and will fail when the WebUI is disabled; treat the WebUI healthcheck as best-effort, not authoritative.

### Middleware Chain

Outer to inner:

1. **`securityHeaders`** — sets `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `X-XSS-Protection: 1; mode=block`, and a CSP allowing `'self'`, inline styles/scripts, and Google Fonts.
2. **`CSRFMiddleware`** — issues tokens on safe methods, validates on unsafe ones.
3. **`SessionMiddleware`** — loads/redirects based on `__Host-session_id`. Excludes `/login`, `/favicon.ico`, and `/static/`.
4. **Mux** — handler dispatch.

---

## Page Data

Each HTML template receives a `PageData` envelope:

```go
type PageData struct {
    Title     string
    ActiveNav string
    CSRFToken string
    Flash     *FlashMessage
    Data      any         // page-specific payload
    NavItems  []NavItem
}
```

The `Data` field shape depends on the page:

| Page | Data type | Key fields |
|------|-----------|-----------|
| `dashboard` | `dashboardData` | `TotalMemories`, `TotalUsers`, `Uptime` (seconds since process start) |
| `config` | `*config.Config` | The full runtime config struct |
| `channels` | `*channelsPageData` | `Blacklist` + `Whitelist`, each with `Users/Channels/Guilds` resolved to display names |
| `memories` | `memoriesPageData` | `UserID`, `[]memoryDisplayEntry`, `Count`, `Searched`, `Error` |
| `profiles` | `profilesPageData` | `UserID`, `*profileDisplayEntry`, `Found`, `Searched`, `EditMode`, `Error` |
| `plugins` | `pluginsPageData` | `[]plugin.InfoExt` (Name, Version, Author, Description, Priority, Enabled) |
| `logs` | `map[string]any` | `Lines`, `Content`, `Stats` ("Showing last N of M") |

### Flash Messages

Cookie-based, self-expiring (60s TTL). The dashboard sets a flash on form-submit redirects so the next page can render success/error banners. No server-side state.

---

## Static Assets

CSS and JS are **not embedded** in the binary. They're served from disk by `http.FileServer(http.Dir(staticDir))`.

`findStaticDir()` probes several candidate paths in order:

1. `./internal/web/static`
2. `../internal/web/static`
3. `./web/static`
4. `./static`
5. Executable-relative paths (`<exe-dir>/web/static`, etc.)

If none exist, `/static/` returns 404. **Make sure your deployment ships the `static/` directory next to the binary** — the Dockerfile copies it to `/app/web/static`.

Templates, on the other hand, **are** embedded via `//go:embed`, so the binary alone is enough to render pages.

---

## Notes for Operators

- **No HTTPS support built in.** Put nginx, Caddy, or similar in front of the WebUI if it's exposed beyond localhost.
- **No streaming endpoints.** No SSE, no WebSocket, no long-polling. Logs page reads a snapshot — refresh for fresh data.
- **No metrics endpoint.** Add Prometheus scraping via labels on the container if you want metrics; the bot itself doesn't expose them.
- **Config persistence:** updates submitted from `/config` are validated, applied to the running process via `atomic.Value`, and written back to the path in `-config`. If the file is read-only (e.g., mounted with `:ro` in Docker Compose), the write fails and the change reverts.
- **Plugin toggles** call `PluginManager.EnablePlugin/DisablePlugin`, which in turn refreshes the AI tool registry. Toggles are not persisted — they reset on restart.
