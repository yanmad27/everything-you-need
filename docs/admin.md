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
