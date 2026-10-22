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

Config (env): `CONVO_ENABLED`, `CONVO_DB_PATH`, `CONVO_RETENTION_DAYS`, `CONVO_PRICES`,
`CONVO_CURRENCY`, and the legacy `CONVO_PRICE_PROMPT_PER_1K` / `CONVO_PRICE_COMPLETION_PER_1K`.

### Usage prices

Prices are **per model, per 1,000,000 tokens**, prompt and completion separately, labelled
with `convo.currency` / `CONVO_CURRENCY` (default `USD`, label only). Two equivalent forms:

- env: `CONVO_PRICES="gpt-6-sol:1.25:10,claude-haiku-4-5:1:5"` — comma-separated
  `model:prompt:completion` (model match is case-insensitive). A malformed value is logged
  and ignored (file prices still apply).
- yaml: `convo.prices: [{model: gpt-6-sol, prompt_per_1m: 1.25, completion_per_1m: 10}]`.

The env entry overrides the yaml entry for the same model. The old
`price_prompt_per_1k` / `price_completion_per_1k` keys still work as a **fallback for models
with no entry** (per-1K × 1000 = per-1M). **There are no default prices.** A model with no
price (and no legacy fallback) has cost *nil* — shown as "price not configured", never `0`.

Aggregation rule: a group's `Cost` is the cost of its **priced** tokens; `PartialPricing` is
true when some tokens in the group belong to an unpriced model (so `Cost` is a lower bound),
and `Cost` is nil when nothing in the group could be priced. A group with zero tokens has cost 0.
Token counts come from the recorded per-turn `usage` rows; the upstream (OpenAI-compatible)
`usage` object carries no cost field (verified for `cpa.lowbit.link`), so cost is always computed
from the configured prices at query time (changing a price re-prices history).

Query API (`services/convo`, read-only): `ListChats`, `Timeline`, `UserStats`,
`ChatStats`, `ToolStats`, `UsageTotals`, `UsageByDay`, `UsageByChat`, `UsageByUser`,
`UsageByModel` (days are Vietnam time, UTC+7; `From` inclusive, `To` exclusive). Reminder-regex commands handled before the chat
assistant are not recorded (only chat/tool turns).

## Admin UI (`/admin`)

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
- `GET /admin/usage` (+ `GET /admin/api/usage`) — tokens and cost per day / model / chat / user
  and totals; filters `platform`, `from`, `to` (YYYY-MM-DD, Vietnam days, `to` inclusive),
  `chat_page`, `user_page`
- `GET|POST /admin/password` — change the admin password (needs a session)
- `GET|POST /admin/login`, `POST /admin/logout` — with `/admin/password`, the only state-changing routes (CSRF)

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

## Changing the password

`/admin/password`: current password, new password, confirmation (per-session CSRF token).
Rules: ≥12 characters and ≤72 bytes; different from the current password; at least 3 of
{lowercase, uppercase, digit, symbol} **or** ≥20 characters; confirmation must match.

On success a bcrypt (cost 12) hash is stored in the convo SQLite DB (`admin_credentials`, one
row) and takes precedence over the env hash from then on (the env hash stays as the
bootstrap/fallback; the username always comes from `ADMIN_USERNAME`). **All** sessions are
invalidated and the current one gets a new id/cookie. Attempts are limited to 5 failures per
15 minutes per session and per client IP (→ 429); a wrong current password counts. Env
`ADMIN_USERNAME` / hash must still be set for the UI to mount.

Audit: table `admin_audit(at, event, ip)` with `login_ok`, `login_fail`, `password_changed`,
`password_change_fail`, `logout`, and a log line `admin audit: event=… ip=…`. Passwords and
hashes are never stored in it or logged. Rows are pruned with the retention window.

### Lockout recovery

Set `ADMIN_PASSWORD_RESET=true` and restart: the stored hash is deleted at startup (logged as
`admin: ADMIN_PASSWORD_RESET is set…`) and the env hash (`ADMIN_PASSWORD_HASH_B64` /
`ADMIN_PASSWORD_HASH`) applies again. Unset the flag afterwards (while it is set, every restart
clears a password changed in the UI). Without a restart, with sqlite3 on the data volume:
`sqlite3 data/convo.db "DELETE FROM admin_credentials;"` (takes effect at the next login).

## Template data contract

Every template receives `page` (`services/admin/views.go`): `.Title`, `.Nav` (legacy key:
`chats|users|chatstats|tools|usage|password`), **`.Active`** (nav key: `chats|users|chat-stats|tools|usage|password`;
empty on login), **`.Username`** (signed-in admin; empty on login), `.CSRF` (session token, empty on
login page where it is the login token), `.Nonce`, `.CSS`, `.Q` (`url.Values` of the current
filters), `.Data`. Template funcs: `ftime`, `kb`, `pct`, `cost`, `money *float64 partial`
("price not configured" for nil, `≥ x (partial)` when partial), `num int` (1,234,567).

`password.html` — `.Data` is `PasswordView`: `.Saved` bool (show success), `.Error` string
(empty = none), `.MinLen` int (chars), `.MaxLen` int (bytes), `.Hint` string. Form: POST
`/admin/password` with fields `csrf` (= `.CSRF`), `current`, `new`, `confirm`.

`usage.html` — `.Data` is `UsageView`:
- `.Currency`, `.Platform`, `.From`, `.To` (filter echo), `.Prices` (`[]convo.Price{Model, PromptPer1M, CompletionPer1M}`)
- `.Totals` — `convo.UsageSum`
- `.Days` `[]convo.UsageDay{Day "YYYY-MM-DD"}` ascending, zero-filled when both dates are set;
  `.MaxDayTokens` int and `.MaxDayCost` float64 to scale bars
- `.Models` `[]convo.UsageModel{Model, Priced}`
- `.Chats` `[]convo.UsageChat{Platform, ChatID, Name}`, `.ChatsTotal`, `.ChatsPage`, `.ChatsPager` (`Prev/Next/HasPrev/HasNext/PrevLabel/NextLabel/Page/Pages`; query param `chat_page`)
- `.Users` `[]convo.UsageUser{Platform, UserID, Name}`, `.UsersTotal`, `.UsersPage`, `.UsersPager` (param `user_page`)
- every row embeds `convo.UsageSum`: `Turns`, `PromptTokens`, `CompletionTokens`, `TotalTokens`,
  `Cost *float64` (nil = price not configured), `PartialPricing bool`.

`GET /admin/api/usage` returns the same `UsageView` as JSON (snake_case keys, `cost: null` when unpriced).
`stats.html` rows (`convo.Stat`) gain `PartialPricing`; `EstCost` is now the per-model priced cost.
