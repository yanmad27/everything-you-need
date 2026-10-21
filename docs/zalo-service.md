# Zalo Bot Service

The Zalo bot offers the same two features as the Telegram bot, in 1:1 chats and
in Zalo groups the bot has been added to:

1. **Chat assistant "Trợ lý Pink"** — same wake words (`pink`, `trợ lý`, `troly`),
   clear-context commands and link reading.
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
| `LLM_OPENAI_MODEL` | `llm_openai.model` | `codex-auto-review` |

(If the server rejects `max_tokens`, the client retries once with `max_completion_tokens`.)


The provider is used only when both base URL and key are set. Otherwise Zalo
falls back to the Anthropic client (`chat.api_key`, else `reminder.openai_api_key`),
exactly like Telegram. Check the model list with `GET {base_url}/v1/models`.
If Telegram is not configured, `reminder.openai_api_key` is not required as long
as the provider above is set.

## Wake word and groups

- **1:1 chats:** same trigger as Telegram — the chat assistant answers messages
  containing a wake word (`pink`, `trợ lý`, `troly`). Reminder keywords
  ("nhắc tao…") and `/reminders`, `/cancel` always work.
- **Groups (Beta):** Zalo only delivers messages that @mention the bot or reply
  to one of its messages, so every delivered group message is already addressed
  to the bot. The assistant therefore answers **without** needing a wake word
  (a wake word, if present, is stripped). Reminder commands still take
  priority. This is implemented as a separate `HandleAddressed` path in the chat
  service; the Telegram trigger logic is untouched. If Zalo ever starts delivering
  *all* group messages, the bot would answer every one of them; to restore
  wake-word behaviour in groups, change `dispatchIncoming(..., addressed)` in
  `main.go` to pass `false` for Zalo groups.

**Link reading:** the Zalo chat assistant only fetches public http/https links
(loopback, private, link-local, CGNAT, multicast and unspecified addresses are
refused at dial time, including redirects). Telegram's link reader is unchanged.

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
