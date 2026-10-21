# Conversation store (admin data)

The Zalo chat service records conversations into SQLite (`convo.db_path`, default
`data/convo.db`, on the `/root/data` volume) so the admin UI can show timelines
and insights. Telegram is **not** recorded.

Stored: chats (platform, id, PRIVATE/GROUP, first/last seen), users (id, display
name), messages (in/out, text up to 8000 chars, image mime + size only, turn id,
fixed error tag), tool calls (name, ≤300-char args/result summary, outcome,
latency; `fetch_url` keeps only the host, `web_search` only the query, web
content is never stored) and per-turn LLM usage (rounds, tokens, model).
Never stored: tokens/secrets, image bytes or URLs, webhook payloads.

Behaviour: recording is fail-open and asynchronous (1000-event queue, single
batching writer; when full events are dropped and a counter is logged, no
content). `Close` flushes on shutdown (5s cap). Rows older than
`convo.retention_days` (default 90) are pruned daily.

Config (env): `CONVO_ENABLED`, `CONVO_DB_PATH`, `CONVO_RETENTION_DAYS`,
`CONVO_PRICE_PROMPT_PER_1K`, `CONVO_PRICE_COMPLETION_PER_1K` (0 = tokens only).

Query API (`services/convo`, read-only): `ListChats`, `Timeline`, `UserStats`,
`ChatStats`, `ToolStats`. Reminder-regex commands handled before the chat
assistant are not recorded (only chat/tool turns).

## Admin UI (`/admin`, read-only)

Enabled only when `ADMIN_USERNAME` **and** `ADMIN_PASSWORD_HASH` (bcrypt, cost ≥ 10)
are set and the convo store is up; otherwise `/admin` is not mounted (404) and the
startup log says why. No default password. Optional `ADMIN_SESSION_SECRET` (HMAC
key for login CSRF tokens; random per process if empty).

Create the hash (password read from stdin, 12–72 bytes, never logged):
`printf '%s' 'your password' | go run ./cmd/adminhash` — paste the output into the
env **without quotes** (in Dokploy and similar UIs the value is passed verbatim).
The tool prints a second line, `ADMIN_PASSWORD_HASH_B64=…`: the same hash base64-encoded.

**Recommended on Dokploy/compose: set `ADMIN_PASSWORD_HASH_B64`.** A bcrypt hash
contains `$` characters, and compose interpolation treats `$NAME` as a variable
reference, silently eating part of the hash (a 60-char hash arrives as ~55 chars and
is rejected). The base64 form has no `$`, so it survives intact. It accepts standard
or URL-safe base64, padded or not, and takes precedence over `ADMIN_PASSWORD_HASH`.
Invalid base64 disables the admin UI (`admin: ADMIN_PASSWORD_HASH_B64 is not valid
base64`). When a hash is rejected the log shows only its shape (source, length,
`$2` prefix, `$` count, quoted), never the value.

`ADMIN_PASSWORD_HASH` (raw bcrypt) remains an alternative; it needs every `$`
written as `$$` when the platform interpolates.
Quote handling (applies to the raw form):
One pair of surrounding single/double quotes and surrounding whitespace are
tolerated and stripped from `ADMIN_USERNAME`, `ADMIN_PASSWORD_HASH` and
`ADMIN_SESSION_SECRET`.

Routes (all need a session except login; logged out: HTML → 302 `/admin/login`,
`/admin/api/*` → 401):
- `GET /admin`, `/admin/chats` — conversations (filters: platform, type, q, from, to; paging)
- `GET /admin/chat?platform=&id=` — timeline (messages, tool calls, images as metadata)
- `GET /admin/users`, `/admin/chat-stats` — user / chat insights
- `GET /admin/tools` — tool success rate, p50/p95, recent failures
- `GET /admin/api/{chats,timeline,users,chat-stats,tools}` — same data as JSON
- `GET|POST /admin/login`, `POST /admin/logout` — the only state-changing routes (CSRF)

Security: login CSRF (HMAC, 30 min) + 5 attempts / 15 min per client IP
(`CF-Connecting-IP`, else the last `X-Forwarded-For` hop, else the peer address) → 429,
and a global cap of 300 *failed* attempts / 15 min (successful logins never count); bcrypt always
runs; server-side sessions (32-byte random id, cookie `__Host-eyn_admin`, HttpOnly,
Secure, SameSite=Strict), idle 2h, absolute 12h; no-store, nosniff, no-referrer,
X-Frame-Options DENY, CSP `default-src 'none'` with a per-response style nonce and
no scripts; html/template escaping everywhere. Sessions live in memory (restart logs
everyone out). **Deployment note:** the client-IP headers are trusted as sent, so the origin must be
reachable only through Cloudflare (firewall the origin to Cloudflare's ranges or use a
tunnel); otherwise the per-IP limit can be evaded by spoofing them.

Data notes: the DB file is created `0600` (its directory `0700` only if we create it).
Prune runs ~10s after start and daily, then truncates the WAL; `Dropped()` also counts
events lost to failed writes. `due_dates`, `reminder_list`, `news_recent`, `fetch_url`
and `web_search` store only `ok (N chars)` as the result. Insights: **Errors** = tool
calls with outcome error/timeout + failed bot replies (attributed to the turn's user);
rate-limit / image-too-large replies are **Notices**. Tool success rate =
ok/(ok+error+timeout), blocked shown separately, p50/p95 over ok calls. Timeline pages
count from the newest (page 1 = latest 100 entries).
