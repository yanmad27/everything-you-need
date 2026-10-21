package zalobot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestEscapeURLs(t *testing.T) {
	in := "see https://a.com/x_y*z~w\\q and _italic_ http://b.io/p_1"
	got := escapeURLs(in)
	want := "see https://a.com/x\\_y\\*z\\~w\\\\q and _italic_ http://b.io/p\\_1"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if unescapeURLs(got) != in {
		t.Errorf("unescape mismatch: %q", unescapeURLs(got))
	}
}

func TestSplitMarkdownKeepsURLsAndPairs(t *testing.T) {
	url := "https://example.com/" + strings.Repeat("ab_", 100)
	text := strings.Repeat("word ", 390) + url + " tail"
	parts := splitMarkdown(escapeURLs(text), 2000)
	if len(parts) < 2 {
		t.Fatalf("want split, got %d", len(parts))
	}
	esc := escapeURLs(url)
	var mds []string
	for _, p := range parts {
		mds = append(mds, p.md)
	}
	joined := strings.Join(mds, "")
	if !strings.Contains(joined, esc) {
		t.Error("URL was split across chunks")
	}
	for _, p := range parts {
		if utf8.RuneCountInString(p.md) > 2000 {
			t.Errorf("chunk too long: %d", utf8.RuneCountInString(p.md))
		}
	}

	bold := "**" + strings.Repeat("lorem ipsum ", 400) + "**"
	parts = splitMarkdown(bold, 2000)
	if len(parts) < 2 {
		t.Fatalf("want split, got %d", len(parts))
	}
	for i, p := range parts {
		if utf8.RuneCountInString(p.md) > 2000 {
			t.Errorf("chunk %d too long", i)
		}
		if len(openPairs([]rune(p.md))) > 0 {
			t.Errorf("chunk %d leaves ** open", i)
		}
	}
}

func TestSendMarkdownEscapesAndFallsBack(t *testing.T) {
	var bodies []sendMessageRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req sendMessageRequest
		_ = json.Unmarshal(b, &req)
		bodies = append(bodies, req)
		if req.ParseMode == "markdown" {
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"bad markdown"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer srv.Close()
	z := NewClient(fakeToken, srv.URL)
	if err := z.SendMarkdown(context.Background(), "c1", "**hi** https://x.com/a_b_c"); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 {
		t.Fatalf("want markdown then plain, got %d", len(bodies))
	}
	if bodies[0].ParseMode != "markdown" || bodies[0].Text != "**hi** https://x.com/a\\_b\\_c" {
		t.Errorf("markdown req = %+v", bodies[0])
	}
	if bodies[1].ParseMode != "" || bodies[1].Text != "**hi** https://x.com/a_b_c" {
		t.Errorf("plain req = %+v", bodies[1])
	}
}

func TestSendMarkdownPlainErrorStillReturned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error_code":500,"description":"boom"}`))
	}))
	defer srv.Close()
	if err := NewClient(fakeToken, srv.URL).SendMarkdown(context.Background(), "c", "x"); err == nil {
		t.Fatal("want error when plain also fails")
	}
}

// --- image webhook ---

func imgBody(msgExtra string) string {
	return `{"event_name":"message.image.received","message":{"from":{"id":"u1","is_bot":false},` +
		`"chat":{"id":"u1","chat_type":"PRIVATE"},"message_id":"i1",` + msgExtra + `}}`
}

func TestWebhookImageShapes(t *testing.T) {
	cases := map[string]struct{ extra, photo, caption string }{
		"docs":        {`"photo":"https://cdn.example/p.jpg?tok=SECRETQ","caption":"cap"`, "https://cdn.example/p.jpg?tok=SECRETQ", "cap"},
		"attachments": {`"attachments":[{"type":"image","payload":{"url":"https://cdn.example/a.png"}}],"text":"txt"`, "https://cdn.example/a.png", "txt"},
		"image_url":   {`"image_url":"http://h.example/i.gif"`, "http://h.example/i.gif", ""},
	}
	for name, c := range cases {
		_, got := serve(t, "s", "s", imgBody(c.extra))
		if len(got) != 1 || got[0].Kind != "image" || got[0].Photo != c.photo || got[0].Caption != c.caption {
			t.Errorf("%s: %+v", name, got)
		}
	}
	_, got := serve(t, "s", "s", strings.Replace(privateText, "m1", "m9", 1))
	if len(got) != 1 || got[0].Kind != "text" {
		t.Errorf("text kind: %+v", got)
	}
}

func TestWebhookImageNoPhotoDropped(t *testing.T) {
	var got []Message
	logs := captureLog(func() { _, got = serve(t, "s", "s", imgBody(`"photo":"not-a-url","caption":"c"`)) })
	if len(got) != 0 || !strings.Contains(logs, "outcome=dropped:no_photo") {
		t.Errorf("got=%+v log=%q", got, logs)
	}
}

func TestWebhookImageLogsHostOnly(t *testing.T) {
	var got []Message
	logs := captureLog(func() {
		_, got = serve(t, "s", "s", imgBody(`"photo":"https://cdn.example/secretpath/p.jpg?tok=SECRETQ","caption":"private caption"`))
	})
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	if !strings.Contains(logs, `kind=image photo_host="cdn.example"`) || !strings.Contains(logs, "structure=") {
		t.Errorf("log = %q", logs)
	}
	for _, bad := range []string{"secretpath", "SECRETQ", "p.jpg", "private caption"} {
		if strings.Contains(logs, bad) {
			t.Errorf("log leaks %q: %q", bad, logs)
		}
	}
}

// --- FetchImage ---

func imageServer(t *testing.T, body []byte, status int) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// loopbackClient returns a service whose image client may reach httptest servers.
func loopbackClient() *ZaloBotService {
	z := NewClient(fakeToken, "http://unused")
	z.imageClient = &http.Client{}
	return z
}

var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)

func TestFetchImageSniffs(t *testing.T) {
	z := loopbackClient()
	for name, c := range map[string]struct {
		b    []byte
		mime string
	}{
		"png":  {pngBytes, "image/png"},
		"jpeg": {append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, make([]byte, 8)...), "image/jpeg"},
		"gif":  {append([]byte("GIF89a"), make([]byte, 8)...), "image/gif"},
		"webp": {append([]byte("RIFF\x00\x00\x00\x00WEBP"), make([]byte, 8)...), "image/webp"},
	} {
		srv := imageServer(t, c.b, 200)
		data, mime, err := z.FetchImage(context.Background(), srv.URL)
		if err != nil || mime != c.mime || len(data) != len(c.b) {
			t.Errorf("%s: mime=%q err=%v", name, mime, err)
		}
	}
	srv := imageServer(t, []byte("<html>not an image</html>"), 200)
	if _, _, err := z.FetchImage(context.Background(), srv.URL); err != errImageType {
		t.Errorf("html: %v", err)
	}
	srv = imageServer(t, pngBytes, 404)
	if _, _, err := z.FetchImage(context.Background(), srv.URL); err != errImageStatus {
		t.Errorf("404: %v", err)
	}
}

func TestFetchImageSizeCap(t *testing.T) {
	z := loopbackClient()
	big := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, maxImageBytes)...)
	srv := imageServer(t, big, 200) // Content-Length known
	if _, _, err := z.FetchImage(context.Background(), srv.URL); err != errImageTooLarge {
		t.Errorf("content-length: %v", err)
	}
	chunked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush() // forces chunked, no Content-Length
		_, _ = w.Write(big)
	}))
	defer chunked.Close()
	if _, _, err := z.FetchImage(context.Background(), chunked.URL); err != errImageTooLarge {
		t.Errorf("chunked: %v", err)
	}
	exact := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, maxImageBytes-8)...)
	srv = imageServer(t, exact, 200)
	if _, _, err := z.FetchImage(context.Background(), srv.URL); err != nil {
		t.Errorf("exactly max: %v", err)
	}
}

func TestFetchImageDefaultClientBlocksLoopback(t *testing.T) {
	srv := imageServer(t, pngBytes, 200)
	z := NewClient(fakeToken, "http://unused")
	_, _, err := z.FetchImage(context.Background(), srv.URL)
	if err != errImageBlocked {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), srv.URL) {
		t.Error("error leaks destination")
	}
	redir := httptest.NewServer(http.RedirectHandler(srv.URL, http.StatusFound))
	defer redir.Close()
	if _, _, err := z.FetchImage(context.Background(), redir.URL); err == nil {
		t.Error("redirect to loopback must fail")
	}
	if _, _, err := z.FetchImage(context.Background(), "file:///etc/passwd"); err != errImageURL {
		t.Errorf("scheme: %v", err)
	}
}

func TestSplitMarkdownTerminatesOnHugeTokens(t *testing.T) {
	for name, text := range map[string]string{
		"bold+url":   "**https://" + strings.Repeat("a", 2500),
		"under+url":  "__https://" + strings.Repeat("a", 2500),
		"escaped":    "**https://" + strings.Repeat("a_", 1500),
		"huge token": strings.Repeat("x", 5000),
		"both":       "**__https://" + strings.Repeat("a", 5000),
	} {
		parts := splitMarkdown(escapeURLs(text), 2000)
		if len(parts) < 2 || len(parts) > 10 {
			t.Errorf("%s: %d chunks", name, len(parts))
		}
		for i, p := range parts {
			if n := utf8.RuneCountInString(p.md); n > 2000 {
				t.Errorf("%s chunk %d: %d runes", name, i, n)
			}
			if strings.HasSuffix(strings.TrimSuffix(p.md, "**"), "\\") && !strings.HasSuffix(p.md, "\\\\") {
				// a dangling single backslash would escape the closer
				t.Errorf("%s chunk %d ends in dangling escape", name, i)
			}
		}
	}
}

func TestEscapeURLsKeepsTrailingMarkers(t *testing.T) {
	got := escapeURLs("**see https://a.com/x_y** and (https://b.io/p_q). ok https://c.io/a_b!")
	want := "**see https://a.com/x\\_y** and (https://b.io/p\\_q). ok https://c.io/a\\_b!"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestSplitMarkdownBothPairsReverseOrder(t *testing.T) {
	text := "**__" + strings.Repeat("lorem ipsum ", 400) + "__**"
	parts := splitMarkdown(text, 2000)
	if len(parts) < 2 {
		t.Fatal("want split")
	}
	first, second := parts[0].md, parts[1].md
	if !strings.HasSuffix(first, "__**") || !strings.HasPrefix(second, "**__") {
		t.Errorf("first ends %q, second starts %q", first[len(first)-6:], second[:6])
	}
	for _, p := range parts {
		if len(openPairs([]rune(p.md))) != 0 {
			t.Error("chunk leaves a pair open")
		}
		if strings.Contains(p.plain, "**") && p.plain != parts[0].plain && strings.HasPrefix(p.plain, "**__") {
			t.Error("plain keeps synthetic reopener")
		}
	}
	if strings.HasPrefix(parts[1].plain, "**") || strings.HasSuffix(parts[0].plain, "**") {
		t.Error("plain must exclude synthetic markers")
	}
}

func TestWebhookImageDumpCapped(t *testing.T) {
	logs := captureLog(func() {
		h := NewWebhookHandler("s", func(context.Context, Message) {})
		for i := 0; i < 8; i++ {
			body := strings.Replace(imgBody(`"photo":"https://h.example/p.jpg"`), `"i1"`, `"i`+strconv.Itoa(i)+`"`, 1)
			req := httptest.NewRequest("POST", "/", strings.NewReader(body))
			req.Header.Set(SecretHeader, "s")
			h.ServeHTTP(httptest.NewRecorder(), req)
		}
	})
	if n := strings.Count(logs, "structure="); n != maxImageDumps {
		t.Errorf("structure dumps = %d, want %d", n, maxImageDumps)
	}
}

func TestSplitMarkdownLongMarkerRunIsBounded(t *testing.T) {
	for _, ch := range []string{"*", "_", `\`} {
		text := strings.Repeat(ch, 2500)
		chunks := splitMarkdown(text, 2000)
		if len(chunks) > 4 {
			t.Errorf("%q run produced %d chunks", ch, len(chunks))
		}
		for _, c := range chunks {
			if n := len([]rune(c.md)); n > 2000 || !utf8.ValidString(c.md) {
				t.Errorf("chunk len=%d valid=%v", n, utf8.ValidString(c.md))
			}
		}
	}
}
