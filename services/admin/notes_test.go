package admin

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"everything-you-need/m/services/note"
)

type fakeNotes struct{}

func (fakeNotes) Chats(context.Context) ([]note.ChatSummary, error) {
	return []note.ChatSummary{{Platform: "zalo", ChatID: "g<1>", Count: 2, UpdatedAt: time.Date(2026, 10, 1, 9, 0, 0, 0, ict)}}, nil
}
func (fakeNotes) ChatNotes(_ context.Context, p, c string) ([]note.Note, error) {
	t := time.Date(2026, 10, 1, 9, 0, 0, 0, ict)
	return []note.Note{{ID: 2, Platform: p, ChatID: c, Title: "<i>t</i>", Body: "<script>alert(1)</script> wifi", AuthorName: "An", CreatedAt: t, UpdatedAt: t}}, nil
}

func TestNotesPage(t *testing.T) {
	c := &clk{t: time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)}
	h, err := New(Config{Username: "owner", PasswordHash: testHash}, fixture{}, Options{Now: c.now, Notes: fakeNotes{}})
	if err != nil {
		t.Fatal(err)
	}
	// auth required
	w := do(h, "GET", "/admin/notes", nil, nil)
	if w.Code != http.StatusFound || !strings.Contains(w.Header().Get("Location"), "/admin/login") {
		t.Fatalf("unauthenticated: %d %s", w.Code, w.Header().Get("Location"))
	}
	ck := loginSession(t, h)
	w = do(h, "GET", "/admin/notes", nil, ck)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "g&lt;1&gt;") || strings.Contains(body, "g<1>") {
		t.Fatalf("chat list: %d %s", w.Code, body)
	}
	if !strings.Contains(body, `href="/admin/notes"`) || !strings.Contains(body, `aria-current="page"`) {
		t.Error("nav entry missing")
	}
	w = do(h, "GET", "/admin/notes?platform=zalo&id=g1", nil, ck)
	body = w.Body.String()
	if w.Code != 200 || strings.Contains(body, "<script>alert") || !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt; wifi") || strings.Contains(body, "<i>t</i>") {
		t.Fatalf("notes escaping: %d %s", w.Code, body)
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("csp %q", csp)
	}
	if strings.Contains(body, " style=") {
		t.Error("inline style attribute")
	}
	// read-only: POST is refused
	if w := do(h, "POST", "/admin/notes", map[string][]string{"csrf": {"x"}}, ck); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("post: %d", w.Code)
	}
	// without a note store the page is off
	h2, _ := New(Config{Username: "owner", PasswordHash: testHash}, fixture{}, Options{Now: c.now})
	if w := do(h2, "GET", "/admin/notes", nil, loginSession(t, h2)); w.Code != http.StatusNotFound {
		t.Errorf("disabled: %d", w.Code)
	}
}
