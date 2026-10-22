# Per-bot settings (foundation)

Package `services/botconfig` stores per-bot settings that the owner edits live in
`/admin` (UI: `/admin/bots`, documented in `docs/admin.md`; Zalo reads one snapshot per inbound message).
Telegram is listed in the registry (`botconfig.Bots()`) as **not managed**: no editable keys.

## Precedence

1. env/config value = **default** (`botconfig.ZaloDefaults(cfg, pricesSpec)`).
2. A row in `bot_settings` **overrides** it.
3. **Reset** deletes the row → the default applies again.
4. A later change of the env never touches an existing override.

Table `bot_settings(platform, key, value, is_secret, updated_at, updated_by, PRIMARY KEY(platform,key))`
in the convo SQLite DB (idempotent migration). Plain values are JSON; secrets are ciphertext.

## Keys (platform `zalo`)

| key | type | rule / default source |
|---|---|---|
| `enabled` | bool | `zalo.enabled` |
| `llm_base_url` ★ | string | cannot be saved empty (reset instead);  https only, default port, no credentials/query; host must be a public DNS name (no IP, `localhost`, single-label, `.local/.internal/.lan/...`); `llm_openai.base_url` |
| `llm_model` | string | ≤100, `[A-Za-z0-9._:/-]`; `llm_openai.model` |
| `llm_api_key` ★ | secret | `llm_openai.api_key` |
| `prices` | prices | `CONVO_PRICES` format `model:prompt:completion` per 1M tokens, validated by `convo.ParsePrices` (duplicates rejected); an empty override = built-in default prices only |
| `tools.prices` `tools.lunar` `tools.news` `tools.reminders` `tools.due_dates` `tools.images` | bool | default true |
| `tools.web_search` ★ `tools.fetch` ★ | bool | default true |
| `serper_api_key` ★ | secret | `news.serper_api_key` |
| `fetch_allow_domains` ★ | list | ≤50 hostnames; no IPs; no shared-hosting suffix (github.io, blogspot.com, netlify.app, vercel.app, pages.dev, herokuapp.com, appspot.com, azurewebsites.net, …); lowercased, deduplicated; `chat.fetch_allow_domains` |
| `private_tool_chats` ★ | list | ≤100 ids `[A-Za-z0-9_.:-]`; `zalo.private_tool_chats` |
| `rate_limit_per_min` | int | 1..120 (default 6) |
| `rate_limit_burst` | int | 1..20 (default 3) |
| `persona` ★ | text | ≤4000 chars (empty = built-in character); replaces only the character part, fixed safety/tool/format rules still follow |
| `retention_days` ★ | int | 1..3650; `convo.retention_days` |

★ security-relevant (`Spec.Sensitive`): the UI should warn/confirm. Defaults from env are trusted
and not policy-checked; only saved values are validated.

## Runtime API

`botconfig.New(ctx, store, os.Getenv("BOTCONFIG_SECRET"), map[string]botconfig.Defaults{"zalo": botconfig.ZaloDefaults(cfg, spec)})`
returns a `*Provider`:
`Snapshot(platform) *Snapshot` (immutable; capture one per request), `Save(ctx, platform, map[key]text, actor)`
(validates **all**, one transaction, then swaps the snapshot; any failure keeps the old one; `*ValidationError` lists
problems per key), `Reset(ctx, platform, key, actor)`, `SecretsDisabled()`, `Defaults(platform)`, `Specs(platform)`.
`Snapshot.Zalo()` gives typed `ZaloSettings`; `Snapshot.View(defaults)` gives `[]SettingView` for the page
(secrets only as `Masked{Set, Last4}`; `Last4` is empty for secrets shorter than 12 characters).

Every save/reset writes `admin_audit` rows (`setting_changed` / `setting_reset`, `detail` = `platform/key`,
`actor`, `at`) — never values. Stored rows that cannot be used (undecryptable secret) are skipped, the default
applies and the key name is listed in `Snapshot.Problems` and logged.

## Secrets and encryption

`BOTCONFIG_SECRET` (≥32 characters, e.g. `openssl rand -base64 48`) is the only key material; it is **never**
derived from `ADMIN_SESSION_SECRET`. Absent or shorter → `SecretsDisabled()`: secret keys cannot be saved
(`ErrSecretsDisabled`); non-secret keys still work, and env secrets keep applying.

Key = HKDF-SHA256(secret, info `eyn-botconfig-v1`) → 32 bytes. Each write: AES-256-GCM, random 12-byte nonce,
AAD = `platform/key` (a ciphertext copied to another key fails), stored as `0x01 | nonce | ciphertext+tag`
(the first byte is the key/format version for future rotation). Decryption failure fails closed (default
applies). `Secret` values print as `[redacted]` in `%v/%+v/%#v`, JSON, slog and `String()`.

## Rotation / loss

- Rotate: stop the bot, then
  `BOTCONFIG_OLD_SECRET=<old> BOTCONFIG_SECRET=<new> go run ./cmd/botconfig-rotate -db data/convo.db`
  (re-encrypts every secret row; nothing is written if any row fails to decrypt with the old secret), set the new
  `BOTCONFIG_SECRET` in the deployment and start the bot.
- Lost secret: stored secrets are unrecoverable. Start with the new secret and re-enter the keys in the UI (or clear
  stale rows: `sqlite3 data/convo.db "DELETE FROM bot_settings WHERE is_secret = 1;"`); the env values apply meanwhile.
