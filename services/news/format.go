package news

import (
	"fmt"
	"strings"
	"time"
)

const telegramMaxLen = 4096

// dedupeByURL removes duplicate URLs (feeds often syndicate the same story),
// keeping the first occurrence.
func dedupeByURL(items []FeedItem) []FeedItem {
	seen := make(map[string]bool, len(items))
	var out []FeedItem
	for _, item := range items {
		if seen[item.URL] {
			continue
		}
		seen[item.URL] = true
		out = append(out, item)
	}
	return out
}

func escapeMarkdown(text string) string {
	// Telegram legacy Markdown: escape the special chars that break parsing.
	replacer := strings.NewReplacer("*", " ", "_", " ", "`", " ", "[", "(", "]", ")")
	return replacer.Replace(text)
}

// FormatDigest renders the ranked items into a single bilingual Telegram
// Markdown message, truncated to fit Telegram's length limit.
func FormatDigest(ranked []RankedItem, now time.Time, sourceCount int) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "📰 *Tin nổi bật hôm nay*\n🕐 %s\n\n", now.Format("15:04 02/01/2006"))

	for i, item := range ranked {
		viTitle := escapeMarkdown(item.VietnameseTitle)
		if viTitle == "" {
			viTitle = escapeMarkdown(item.EnglishTitle)
		}
		fmt.Fprintf(&builder, "*%d. %s*\n", i+1, viTitle)
		if item.VietnameseSummary != "" {
			fmt.Fprintf(&builder, "%s\n", escapeMarkdown(item.VietnameseSummary))
		}
		if item.URL != "" {
			fmt.Fprintf(&builder, "🔗 %s\n", item.URL)
		}
		builder.WriteString("\n")
	}

	fmt.Fprintf(&builder, "🤖 _AI-curated · %d nguồn_", sourceCount)
	return truncate(builder.String(), telegramMaxLen)
}

// FormatFallback renders a plain titles-only digest when OpenAI is unavailable.
func FormatFallback(items []FeedItem, now time.Time) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "📰 *Tin mới (bản rút gọn)*\n🕐 %s\n\n", now.Format("15:04 02/01/2006"))
	for i, item := range items {
		fmt.Fprintf(&builder, "%d. %s\n🔗 %s\n", i+1, escapeMarkdown(item.Title), item.URL)
	}
	builder.WriteString("\n🤖 _Tự động · AI tạm thời không khả dụng_")
	return truncate(builder.String(), telegramMaxLen)
}

func truncate(text string, max int) string {
	if len(text) <= max {
		return text
	}
	const ellipsis = "…" // 3 bytes
	// Reserve room for the ellipsis, then trim back to a rune boundary so we
	// never split a multi-byte UTF-8 sequence or exceed the limit.
	trimmed := text[:max-len(ellipsis)]
	for len(trimmed) > 0 && !isUTF8Start(trimmed[len(trimmed)-1]) {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return trimmed + ellipsis
}

func isUTF8Start(b byte) bool {
	// continuation bytes are 10xxxxxx; anything else can start/stand alone
	return b&0xC0 != 0x80
}
