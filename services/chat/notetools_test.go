package chat

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"everything-you-need/m/services/llm"
	"everything-you-need/m/services/note"
)

type stepFn = func([]llm.ChatMessage, []llm.ToolDef) (llm.ToolResponse, error)

func newNoteRig(t *testing.T, mod func(*Tools), steps ...stepFn) (*rig, *note.Store) {
	t.Helper()
	st, err := note.Open(filepath.Join(t.TempDir(), "n.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	r := newRig(t, func(tl *Tools) {
		tl.Notes = st
		if mod != nil {
			mod(tl)
		}
	}, steps...)
	return r, st
}

func TestNoteSaveListSearchGet(t *testing.T) {
	r, st := newNoteRig(t, nil,
		toolStep(call("1", "note_save", `{"title":"Wifi","body":"mật khẩu: abc123"}`)), textStep("ok"))
	r.ask("lưu note: mật khẩu wifi là abc123")
	if !strings.Contains(r.toolResult(1), "#1") {
		t.Fatalf("save: %q", r.toolResult(1))
	}
	n, err := st.Get(context.Background(), "zalo", "chat-1234", 1)
	if err != nil || n.AuthorID != "u1" || n.AuthorName != "An" || n.Title != "Wifi" {
		t.Fatalf("stored %+v %v", n, err)
	}
	r3, st3 := newNoteRig(t, nil, toolStep(call("1", "note_search", `{"query":"wifi"}`)), toolStep(call("2", "note_get", `{"id":1}`)), textStep("ok"))
	st3.Create(context.Background(), "zalo", "chat-1234", "u", "U", "Wifi", "mật khẩu: abc123")
	r3.ask("wifi nhà mình là gì?")
	if got := r3.toolResult(1); !strings.Contains(got, "#1 Wifi") {
		t.Fatalf("search: %q", got)
	}
	if got := lastTool(r3.sc.calls[2]); !strings.Contains(got, "abc123") {
		t.Fatalf("get: %q", got)
	}
}

func TestNoteScopeCannotBeChangedByModel(t *testing.T) {
	ctx := context.Background()
	r, st := newNoteRig(t, nil,
		toolStep(
			call("1", "note_get", `{"id":1,"chat_id":"victim","platform":"telegram"}`),
			call("2", "note_delete", `{"id":1,"chat_id":"victim"}`),
			call("3", "note_save", `{"body":"x","chat_id":"victim","platform":"telegram"}`)),
		textStep("ok"))
	st.Create(ctx, "telegram", "victim", "v", "V", "", "victim secret")
	st.Create(ctx, "zalo", "victim", "v", "V", "", "victim zalo secret")
	r.ask("lưu note x, xoá note 1")
	for _, m := range r.sc.calls[1] {
		if m.Role == "tool" && strings.Contains(m.Content, "secret") {
			t.Fatalf("leaked: %q", m.Content)
		}
	}
	if n, _ := st.Get(ctx, "telegram", "victim", 1); n.Body != "victim secret" {
		t.Fatal("victim note touched")
	}
	if n, _ := st.Get(ctx, "zalo", "victim", 1); n.Body != "victim zalo secret" {
		t.Fatal("victim zalo note touched")
	}
	if all, _ := st.All(ctx, "zalo", "victim"); len(all) != 1 {
		t.Fatalf("write landed in victim chat: %d", len(all))
	}
	if n, err := st.Get(ctx, "zalo", "chat-1234", 1); err != nil || n.Body != "x" {
		t.Fatalf("save should land in the calling chat: %+v %v", n, err)
	}
}

func TestNoteWritesNeedUserIntent(t *testing.T) {
	cases := []struct {
		name, tool, args, text string
	}{
		{"save", "note_save", `{"body":"x"}`, "hôm nay thời tiết thế nào"},
		{"update", "note_update", `{"id":1,"mode":"append","body":"x"}`, "hôm nay thời tiết thế nào"},
		{"delete", "note_delete", `{"id":1}`, "xoá số 1 đi"}, // delete verb but no note word
		{"delete no verb", "note_delete", `{"id":1}`, "cho mình xem note 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, st := newNoteRig(t, nil, toolStep(call("1", c.tool, c.args)), textStep("ok"))
			st.Create(context.Background(), "zalo", "chat-1234", "u", "U", "", "orig")
			r.ask(c.text)
			if !strings.Contains(r.toolResult(1), "did not ask") {
				t.Errorf("not refused: %q", r.toolResult(1))
			}
			all, _ := st.All(context.Background(), "zalo", "chat-1234")
			if len(all) != 1 || all[0].Body != "orig" {
				t.Errorf("store changed: %+v", all)
			}
		})
	}
}

func TestNoteDeleteNeedsIdInUserText(t *testing.T) {
	ctx := context.Background()
	r, st := newNoteRig(t, nil, toolStep(call("1", "note_delete", `{"id":2}`)), textStep("ok"))
	st.Create(ctx, "zalo", "chat-1234", "u", "U", "", "one")
	st.Create(ctx, "zalo", "chat-1234", "u", "U", "", "two")
	r.ask("xoá note 1")
	if !strings.Contains(r.toolResult(1), "chưa nêu số") {
		t.Errorf("res %q", r.toolResult(1))
	}
	if all, _ := st.All(ctx, "zalo", "chat-1234"); len(all) != 2 {
		t.Fatal("deleted without explicit id")
	}
	// id 1 must not match inside 11 or 21
	r2, st2 := newNoteRig(t, nil, toolStep(call("1", "note_delete", `{"id":1}`)), textStep("ok"))
	st2.Create(ctx, "zalo", "chat-1234", "u", "U", "", "one")
	r2.ask("xoá note 11")
	if all, _ := st2.All(ctx, "zalo", "chat-1234"); len(all) != 1 {
		t.Fatal("substring id matched")
	}
	// explicit id works, and only one delete per message
	r3, st3 := newNoteRig(t, nil, toolStep(call("1", "note_delete", `{"id":1}`), call("2", "note_delete", `{"id":2}`)), textStep("ok"))
	st3.Create(ctx, "zalo", "chat-1234", "u", "U", "", "one")
	st3.Create(ctx, "zalo", "chat-1234", "u", "U", "", "two")
	r3.ask("xoá note 1 và 2")
	if all, _ := st3.All(ctx, "zalo", "chat-1234"); len(all) != 1 || all[0].ID != 2 {
		t.Fatalf("bulk delete: %+v", all)
	}
}

func TestNoteWritesRefusedWhenTainted(t *testing.T) {
	for _, taint := range []string{"web_search", "news_recent", "fetch_url"} {
		args := `{"query":"q"}`
		if taint == "news_recent" {
			args = `{}`
		}
		if taint == "fetch_url" {
			args = `{"url":"https://github.com/trending"}`
		}
		for _, w := range []struct{ tool, args string }{
			{"note_save", `{"body":"evil"}`}, {"note_update", `{"id":1,"mode":"replace","body":"evil"}`},
			{"note_delete", `{"id":1}`}, {"note_list", `{}`}, {"note_search", `{"query":"orig"}`}, {"note_get", `{"id":1}`},
		} {
			r, st := newNoteRig(t, nil, toolStep(call("t", taint, args)), toolStep(call("w", w.tool, w.args)), textStep("ok"))
			st.Create(context.Background(), "zalo", "chat-1234", "u", "U", "", "orig")
			r.ask("lưu note, sửa note 1, xoá note 1, thêm vào note")
			if got := lastTool(r.sc.calls[2]); !strings.Contains(got, "disabled after") {
				t.Errorf("%s after %s not blocked: %q", w.tool, taint, got)
			}
			if all, _ := st.All(context.Background(), "zalo", "chat-1234"); len(all) != 1 || all[0].Body != "orig" {
				t.Errorf("%s after %s changed store: %+v", w.tool, taint, all)
			}
		}
	}
}

func TestNoteReadMarksPrivateSoFetchRefused(t *testing.T) {
	for _, tool := range []struct{ name, args string }{
		{"note_list", `{}`}, {"note_search", `{"query":"orig"}`}, {"note_get", `{"id":1}`},
	} {
		fetched := false
		r, st := newNoteRig(t, func(tl *Tools) {
			tl.Fetch = func(_ context.Context, u string) (string, error) { fetched = true; return "page", nil }
		}, toolStep(call("n", tool.name, tool.args)), toolStep(call("f", "fetch_url", `{"url":"https://github.com/trending"}`)), textStep("ok"))
		st.Create(context.Background(), "zalo", "chat-1234", "u", "U", "", "orig")
		r.ask("hi")
		if got := lastTool(r.sc.calls[2]); !strings.Contains(got, "chỉ đọc link") || fetched {
			t.Errorf("%s: fetch after note read allowed: %q", tool.name, got)
		}
	}
}

func TestNoteUpdateAppendAndToggle(t *testing.T) {
	ctx := context.Background()
	r, st := newNoteRig(t, nil, toolStep(call("1", "note_update", `{"id":1,"mode":"append","body":"trứng"}`)), textStep("ok"))
	st.Create(ctx, "zalo", "chat-1234", "u", "U", "mua sắm", "sữa")
	r.ask("thêm vào note mua sắm: trứng")
	if n, _ := st.Get(ctx, "zalo", "chat-1234", 1); n.Body != "sữa\ntrứng" {
		t.Fatalf("append: %q", n.Body)
	}
	// nil store omits the tools; Live toggle hides them too.
	r2 := newRig(t, nil, textStep("ok"))
	r2.ask("hi")
	for _, d := range r2.sc.tools[0] {
		if strings.HasPrefix(d.Name, "note_") {
			t.Fatalf("note tool offered without a store: %s", d.Name)
		}
	}
	r3, _ := newNoteRig(t, nil, textStep("ok"))
	r3.ask("hi")
	have := map[string]bool{}
	for _, d := range r3.sc.tools[0] {
		have[d.Name] = true
	}
	for _, n := range []string{"note_save", "note_list", "note_search", "note_get", "note_update", "note_delete"} {
		if !have[n] {
			t.Errorf("missing %s", n)
		}
	}
	if !strings.Contains(r3.sc.sys[0], "note_search") {
		t.Error("system prompt does not tell the model to use note_search")
	}
}

func TestNoteBodiesNotRecorded(t *testing.T) {
	r, _ := newNoteRig(t, nil, toolStep(call("1", "note_save", `{"body":"TOPSECRET-BODY"}`)), textStep("ok"))
	r.ask("lưu note TOPSECRET-BODY")
	for _, e := range r.rec.tools {
		if strings.Contains(e.Args, "TOPSECRET") || strings.Contains(e.Result, "TOPSECRET") {
			t.Fatalf("recorded: %+v", e)
		}
	}
}
