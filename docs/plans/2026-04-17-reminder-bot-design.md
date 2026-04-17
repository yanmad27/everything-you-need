# Vietnamese Natural-Language Reminder Bot — Design

**Date:** 2026-04-17
**Status:** Design approved, implementation pending
**Owner:** @yanmad27

## 1. Problem

The Telegram bot currently only pushes scheduled notifications (prices, lunar).
Users want to *tell* the bot, in natural Vietnamese, to remind them later:

- `@bot 2h nữa nhắc tao làm gì đó`
- `@bot 19h ngày mai nhắc tôi họp với Huy`
- `@bot thứ 2 tới nhắc mình nộp báo cáo`

The bot must parse the time expression, store the reminder, and fire it in the
same channel at the right moment.

## 2. Scope and decisions

| # | Decision | Choice | Reason |
|---|---|---|---|
| 1 | Deployment target | Channel mode (not DM / not single-user) | Matches existing `tele-noti` usage |
| 2 | Trigger detection | Keyword heuristic (`nhắc (tao\|tôi\|mình\|…)`) | Most natural; zero LLM cost on unrelated messages |
| 3 | NL parsing | Gemini 2.5 Flash (free tier, JSON mode) | Vietnamese time phrasing is too varied for regex |
| 4 | Transport | Telegram webhook via smee.io | User-supplied public URL; no reverse proxy needed |
| 5 | Storage | SQLite via `modernc.org/sqlite` (pure Go, no CGO) | Cheap list/cancel queries, survives restart |
| 6 | Command set (v1) | Create + List + Cancel | Minimum useful; skip edit/snooze |
| 7 | Reminder fire format | Reply-to-original + @-mention | Best context + guaranteed notification |
| 8 | Scheduling resolution | 1 minute (reuse existing `JobScheduler`) | Matches how users phrase time |

## 3. Architecture

```
Telegram channel
      │  (message)
      ▼
https://smee.io/2iKk7zcT8arhn5WB
      │  (SSE)
      ▼
smee-client container  ──▶  Go app :8080/telegram-webhook
                                        │
                                        │ 1. secret_token check
                                        │ 2. update_id dedupe
                                        │ 3. keyword prefilter
                                        │ 4. Gemini parse → {when, task}
                                        │ 5. INSERT into reminders
                                        ▼
                                  SQLite (data/reminders.db)
                                        ▲
                                        │ every 60s sweep:
                                        │ SELECT fire_at <= now()
                                        │
                             JobScheduler cron "* * * * *"
                                        │
                                        ▼
                             Telegram sendMessage
                             (reply_to_message_id + @mention)
```

### Package layout

```
services/
├── reminder/
│   ├── reminder.service.go      # Create / List / Cancel / Sweep
│   ├── parser.go                # Gemini NL → {when, task}
│   ├── store.go                 # SQLite layer
│   └── reminder.service_test.go
├── tele-bot/                    # RENAMED from tele-noti
│   ├── tele-bot.service.go      # send + receive
│   └── webhook.go               # HTTP handler + dedupe
```

## 4. Data model

```sql
CREATE TABLE reminders (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  chat_id         TEXT NOT NULL,
  user_id         TEXT NOT NULL,
  user_name       TEXT NOT NULL,
  original_msg    TEXT NOT NULL,
  original_msg_id INTEGER NOT NULL,
  task            TEXT NOT NULL,
  fire_at         DATETIME NOT NULL,    -- UTC
  created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  fired_at        DATETIME,
  canceled_at     DATETIME,
  attempts        INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_reminders_pending
  ON reminders(fire_at)
  WHERE fired_at IS NULL AND canceled_at IS NULL;

CREATE TABLE processed_updates (
  update_id   INTEGER PRIMARY KEY,
  received_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

Retention: fired/canceled rows deleted nightly after 30 days.
`processed_updates` pruned after 14 days.

## 5. Parser contract

Gemini 2.5 Flash, JSON mode (`response_mime_type: application/json`).

System prompt:

```
You are a parser. Given a Vietnamese reminder request and the current time
(Asia/Ho_Chi_Minh), return JSON:
  {"ok": true,  "when": "<ISO 8601 with +07:00 offset>", "task": "<string>"}
  {"ok": false, "reason": "<short Vietnamese explanation>"}
Return ok=false if ambiguous or if the resolved time is in the past.
```

User content per call:

```
NOW=2026-04-17T14:32:00+07:00
MESSAGE=<raw user text>
```

**Prefilter:** `(?i)nhắc\s+(tao|tôi|mình|t|ae|mọi người|anh em)`.
Non-matches are dropped without any LLM call.

## 6. Receive path

HTTP server on `:8080` inside the `app` container.
Webhook handler:

1. Validate `X-Telegram-Bot-Api-Secret-Token` header against configured secret.
2. Parse body; return 200 immediately.
3. Dispatch asynchronously:
   - If `update_id` already in `processed_updates` → drop.
   - If text starts with `/reminders` / `danh sách nhắc` → `List`.
   - If text starts with `/cancel N` / `hủy nhắc N` → `Cancel`.
   - If keyword prefilter matches → `Create`.
   - Otherwise: silently ignored.

## 7. Fire path

`reminder-dispatch` job at `* * * * *` (reuses `JobScheduler`):

```
SELECT * FROM reminders
 WHERE fired_at IS NULL AND canceled_at IS NULL
   AND fire_at <= now()
 LIMIT 50;
```

For each row:
- `teleBot.SendReply(chatID, originalMsgID, mention + "⏰ Nhắc: " + task)`.
- On success: `UPDATE SET fired_at = now()`.
- On failure: `UPDATE SET attempts = attempts + 1`. Give up after 3 attempts
  (mark `fired_at = now()` with a log line).

Late-fire (bot was down): prepend `(trễ X phút) ` if `now - fire_at > 5 min`.

## 8. Config additions

```yaml
telegram:
  bot_token: "…"
  channel_id: "…"
  webhook_secret: "<openssl rand -hex 32>"
  webhook_path: "/telegram-webhook"

reminder:
  enabled: true
  db_path: "data/reminders.db"
  llm_provider: "gemini"
  gemini_api_key: "…"
  gemini_model: "gemini-2.5-flash"
  retention_days: 30
  max_lookahead_days: 365

server:
  listen_addr: ":8080"
```

## 9. docker-compose changes

```yaml
services:
  app:
    build: .
    ports: ["8080:8080"]
    volumes:
      - ./data:/app/data          # NEW — SQLite persistence
      - ./logs:/app/logs
      - ./config.yaml:/app/config.yaml

  smee-client:                    # NEW
    image: deltaprojects/smee-client
    command:
      - "-u"
      - "https://smee.io/2iKk7zcT8arhn5WB"
      - "-t"
      - "http://app:8080/telegram-webhook"
    restart: unless-stopped
    depends_on: [app]
```

## 10. One-time setup

```bash
# 1. Generate a webhook secret and put it in config.yaml
openssl rand -hex 32

# 2. Register the webhook with Telegram
curl -X POST "https://api.telegram.org/bot$BOT_TOKEN/setWebhook" \
     -d url=https://smee.io/2iKk7zcT8arhn5WB \
     -d secret_token=$WEBHOOK_SECRET
```

## 11. Edge cases

| Case | Behavior |
|---|---|
| "2h nữa" at 23:30 | Resolves to next day (UTC) — fine |
| Resolved time in past | Gemini returns `ok=false`; bot replies "Thời điểm đã qua…" |
| Gemini rate-limited / offline | Reply "Mình đang bận, thử lại sau 1 phút nhé." |
| Restart mid-sweep | No state lost; next tick picks up |
| Reminder overdue on restart | Fires on next tick with `(trễ X phút)` prefix |
| Duplicate webhook delivery | `processed_updates` dedupe |
| Cancel an already-fired reminder | "Nhắc #N đã chạy rồi." |

## 12. Implementation order

1. `services/reminder/store.go` + migration + tests
2. `services/reminder/parser.go` + tests (stubbed Gemini client in tests)
3. `services/reminder/reminder.service.go` + tests
4. Rename `tele-noti` → `tele-bot`; add `webhook.go` + update_id dedupe
5. Wire into `main.go` + `register-jobs.go`
6. `docker-compose.yml` + `config.yaml` + `config.example.yaml` + README
7. Smoke test: `@bot 1 phút nữa nhắc tao kiểm tra` → verify firing

## 13. Non-goals (v1)

- Edit / snooze commands
- Recurring reminders ("mỗi thứ 2 nhắc tôi…")
- Per-user timezone (everyone uses Asia/Ho_Chi_Minh)
- Reminder-sharing / delegation
- Slack / other channels
