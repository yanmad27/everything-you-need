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
| `ZALO_WEBHOOK_SECRET` | `zalo.webhook_secret` | `""` | 8–256 chars; required to mount the webhook |
| — | `zalo.webhook_path` | `/zalo-webhook` | |

`reminder.enabled` + `reminder.openai_api_key` (reminders) and `chat.enabled`
(assistant) apply to both platforms. The webhook HTTP server (`server.listen_addr`)
starts when either the Telegram or the Zalo webhook is configured. With no Zalo
config the app behaves exactly as before.

## Enabling in Dokploy

1. Set env vars: `ZALO_ENABLED=true`, `ZALO_BOT_TOKEN=<TOKEN>`, `ZALO_WEBHOOK_SECRET=<SECRET>`
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
| Markdown | optional `parse_mode` (reminder/chat use plain text) | `parse_mode` markdown/html | Plain text, as on Telegram |
| Edit / delete message | yes | no | Not used by the bot |
| Inline buttons | yes | no | Not used by the bot |
| Typing indicator | `sendChatAction` | `sendChatAction` (`typing`) | Not used (bot didn't send one on Telegram) |
| Max text length | 4096 | 2000 chars | Long replies are split into several messages |
| Webhook auth header | `X-Telegram-Bot-Api-Secret-Token` | `X-Bot-Api-Secret-Token` | Constant-time compare |
| Message/update ids | numeric, `update_id` | string hex `message_id`, no update id | Duplicate deliveries dropped by `message_id` (in memory) |
| Groups | bot sees group messages (per privacy mode) | **Beta**: bot only receives messages that @mention it or reply to one of its messages | Wake word / reminder keyword must be inside such a message |
| User handle | `@username` | `display_name` only | Reminders mention the display name |
| Non-text | ignored | `message.image/sticker/voice/unsupported.received` | Ignored |
| Channel broadcasts (gold, news…) | yes | not in scope | Stay on Telegram |
