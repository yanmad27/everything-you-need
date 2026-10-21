# Zalo Bot Service

The Zalo bot offers the same two features as the Telegram bot, in 1:1 chats and
in Zalo groups the bot has been added to:

1. **Chat assistant "Trợ lý Pink"** — answers every 1:1 message and every
   delivered group message (no wake word; see "Wake word and groups"),
   with clear-context commands and link reading.
2. **Vietnamese natural-language reminders** — create, `/reminders`, `/cancel <id>`
   and the reminder firing back into the same Zalo chat.

Reminder and chat logic is shared with Telegram. Each reminder stores its
`platform` (`telegram` | `zalo`) and `chat_id`; listing/cancelling is scoped to
platform + chat and firing is routed to the reminder's platform.

API reference: <https://bot.zaloplatforms.com/docs/call-api/> ·
[webhook](https://bot.zaloplatforms.com/docs/webhook/) ·
[sendMessage](https://bot.zaloplatforms.com/docs/apis/sendMessage/) ·
[groups](https://bot.zaloplatforms.com/docs/build-bot-interaction-with-group/)

## Configuration

| Env var | Config key | Default | Notes |
|---|---|---|---|
| `ZALO_ENABLED` | `zalo.enabled` | `false` | |
| `ZALO_BOT_TOKEN` | `zalo.bot_token` | `""` | Secret. Never commit it. |
| `ZALO_WEBHOOK_SECRET` | `zalo.webhook_secret` | `""` | 8–256 chars; the webhook is not mounted (and a log line says so) if shorter/longer |
| — | `zalo.webhook_path` | `/zalo-webhook` | |

`reminder.enabled` + `reminder.openai_api_key` (reminders) and `chat.enabled`
(assistant) apply to both platforms. The webhook HTTP server (`server.listen_addr`)
starts when either the Telegram or the Zalo webhook is configured. With no Zalo
config the app behaves exactly as before.

### AI provider for Zalo (optional)

Zalo chat replies **and** Zalo reminder parsing can run on an OpenAI-compatible
endpoint (`POST {base_url}/v1/chat/completions`, `Authorization: Bearer <key>`).
Telegram, news and the default reminder parser keep using Anthropic Claude.

| Env var | Config key | Default |
|---|---|---|
| `LLM_OPENAI_BASE_URL` | `llm_openai.base_url` | `""` (provider off) |
| `LLM_OPENAI_API_KEY` | `llm_openai.api_key` | `""` (secret) |
| `LLM_OPENAI_MODEL` | `llm_openai.model` | `gpt-6-sol` |

(If the server rejects `max_tokens`, the client retries once with `max_completion_tokens`.)


The provider is used only when both base URL and key are set. Otherwise Zalo
falls back to the Anthropic client (`chat.api_key`, else `reminder.openai_api_key`),
exactly like Telegram. Check the model list with `GET {base_url}/v1/models`.
If Telegram is not configured, `reminder.openai_api_key` is not required as long
as the provider above is set.

## Wake word and groups

- **1:1 chats:** the assistant answers **every** text message, no wake word
  needed (a wake word, if present, is stripped). Reminder commands/keywords
  ("nhắc tao…", `/reminders`, `/cancel`) keep priority over chat, and the
  context-clear command ("xóa ngữ cảnh", "clear", …) still works. (Telegram is
  unchanged: it still needs a wake word.)
- **Groups (Beta):** Zalo only delivers messages that @mention the bot or reply
  to one of its messages, so every delivered group message is already addressed
  to the bot. The assistant therefore answers **without** needing a wake word
  (a wake word, if present, is stripped). Reminder commands still take
  priority. This is implemented as a separate `HandleAddressed` path in the chat
  service; the Telegram trigger logic is untouched.
  **Mention stripping (groups only):** a leading `@<bot name>` is removed before
  reminder matching and chat. At startup the bot calls `getMe` and accepts
  `@display_name` or `@account_name` (case-insensitive); if `getMe` fails or the
  name does not match (or continues past the name, e.g. `@Pinky`), a single leading `@token` is removed. Up to 3 leading mentions are stripped (`@Lan @Bot nhắc tao …`). Mentions elsewhere in
  the text, and 1:1 or Telegram messages, are never touched. If Zalo ever starts delivering
  *all* group messages, the bot would answer every one of them; to restore
  wake-word behaviour in groups, make `dispatchZalo` in
  `main.go` pass `addressed=false` for `GROUP` chats.

**Link reading:** the Zalo chat assistant only fetches public http/https links
(loopback, private, link-local, CGNAT, multicast and unspecified addresses are
refused at dial time, including redirects). Telegram's link reader is unchanged.

### Reminders availability

Telegram reminders still require `reminder.openai_api_key` (unchanged). Zalo
reminders need either `llm_openai` (base URL + key) or that same Anthropic key
as fallback. With Telegram on, the Anthropic key empty and `llm_openai` set,
only Zalo reminders are active; Telegram reminders stay off exactly as before
(existing Telegram rows stay pending, untouched).

## Enabling in Dokploy

1. Set env vars: `ZALO_ENABLED=true`, `ZALO_BOT_TOKEN=<TOKEN>`, `ZALO_WEBHOOK_SECRET=<SECRET>`
   and optionally `LLM_OPENAI_BASE_URL`, `LLM_OPENAI_API_KEY`, `LLM_OPENAI_MODEL`
   (e.g. `openssl rand -hex 32`), then redeploy.
2. Register the webhook (Zalo sends a test request immediately and returns the
   result in `verification`):

```bash
curl -X POST "https://bot-api.zaloplatforms.com/bot<TOKEN>/setWebhook" \
  -H "Content-Type: application/json" \
  -d '{"url":"https://<your-domain>/zalo-webhook","secret_token":"<SECRET>"}'
```

3. Check with `getWebhookInfo` / `testWebhook`. Polling (`getUpdates`) and
   webhooks are mutually exclusive; `deleteWebhook` to go back to polling.

## Telegram vs Zalo gaps

| Feature | Telegram | Zalo | What we do |
|---|---|---|---|
| Reply to original message | `reply_to_message_id` | none | Plain message to the same chat |
| Markdown | optional `parse_mode`; the bot sends **plain text** (help/lunar `*bold*` markers show literally on both) | `parse_mode` markdown/html | Plain text, as on Telegram |
| Edit / delete message | yes | no | Not used by the bot |
| Inline buttons | yes | no | Not used by the bot |
| Typing indicator | `sendChatAction` | `sendChatAction` (`typing`) | Not used (bot didn't send one on Telegram) |
| Max text length | 4096 | 2000 chars | Long replies are split into several messages |
| Webhook auth header | `X-Telegram-Bot-Api-Secret-Token` | `X-Bot-Api-Secret-Token` | Constant-time compare |
| Message/update ids | numeric, `update_id` | string hex `message_id`, no update id | Duplicate deliveries dropped by `message_id` (in memory) |
| Groups | bot sees group messages (per privacy mode) | **Beta**: bot only receives messages that @mention it or reply to one of its messages | Wake word / reminder keyword must be inside such a message |
| User handle | `@username` | `display_name` only | Reminders mention the display name |
| Non-text | ignored | `message.image/sticker/voice/unsupported.received` | Ignored |
| Multi-chunk replies | single message | each 2000-char chunk is its own `sendMessage` | If chunk N fails, the reminder sweep retries the whole send, so earlier chunks may be re-sent (accepted) |
| Channel broadcasts (gold, news…) | yes | not in scope | Stay on Telegram |

## Accepted gaps

- **Multi-chunk resend:** a reply longer than 2000 characters is sent as several
  messages; if a later chunk fails, the reminder sweep retries the whole message
  and earlier chunks may be sent again.
- **No per-chat rate limit:** if Zalo ever delivered all group messages, the bot
  would answer all of them with no throttling (see "Wake word and groups").
- **Default model:** `llm_openai.model` defaults to `gpt-6-sol` (verified via `GET /v1/models` and a live completion on https://cpa.lowbit.link). Set `LLM_OPENAI_MODEL`
  explicitly to pin another one; there is no startup warning for the default.

## Webhook diagnostics

Every authenticated delivery logs one secret-free line (no text, no full ids):

```
zalo webhook: shape=wrapped keys=[ok,result] event="message.text.received" chat_type=PRIVATE text_len=5 is_bot=false chat=…4321 outcome=dispatched
zalo dispatch: result=chat chat_type=PRIVATE chat=…4321
```

`outcome` is `dispatched` or `dropped:<reason>` (`unknown_shape`, `event_not_text`,
`no_message`, `empty_text`, `no_chat_id`, `from_bot`, `duplicate`, `decode_error`).
For `shape=unknown` or an undocumented event name an extra `structure=` line shows
JSON keys with value types only. Both the documented `{ok,result:{...}}` shape and a
bare `{event_name,message}` are accepted; ids may be strings or numbers. Each
dispatch runs under a 90s context (the LLM client times out at 60s).
