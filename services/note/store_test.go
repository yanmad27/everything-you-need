package note

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "notes.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

var ctx = context.Background()

func TestSequencePerChatAndNoReuse(t *testing.T) {
	s := open(t)
	a1, _ := s.Create(ctx, "zalo", "A", "u", "U", "", "một")
	a2, _ := s.Create(ctx, "zalo", "A", "u", "U", "", "hai")
	b1, _ := s.Create(ctx, "zalo", "B", "u", "U", "", "b")
	if a1 != 1 || a2 != 2 || b1 != 1 {
		t.Fatalf("ids %d %d %d", a1, a2, b1)
	}
	if err := s.Delete(ctx, "zalo", "A", 2); err != nil {
		t.Fatal(err)
	}
	a3, _ := s.Create(ctx, "zalo", "A", "u", "U", "", "ba")
	if a3 != 3 {
		t.Fatalf("seq reused after delete: %d", a3)
	}
}

func TestIsolation(t *testing.T) {
	s := open(t)
	id, _ := s.Create(ctx, "zalo", "A", "u", "U", "t", "secret-A")
	if _, err := s.Get(ctx, "zalo", "B", id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get other chat: %v", err)
	}
	if _, err := s.Get(ctx, "telegram", "A", id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get other platform: %v", err)
	}
	x := "x"
	if err := s.Update(ctx, "zalo", "B", id, &x, "hacked", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update other chat: %v", err)
	}
	if err := s.Delete(ctx, "zalo", "B", id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete other chat: %v", err)
	}
	if l, _, _ := s.List(ctx, "zalo", "B", 10); len(l) != 0 {
		t.Fatal("list leaks")
	}
	if r, _ := s.Search(ctx, "zalo", "B", "secret", 10); len(r) != 0 {
		t.Fatal("search leaks")
	}
	n, err := s.Get(ctx, "zalo", "A", id)
	if err != nil || n.Body != "secret-A" {
		t.Fatalf("original changed: %+v %v", n, err)
	}
}

func TestLimits(t *testing.T) {
	s := open(t)
	if _, err := s.Create(ctx, "zalo", "A", "", "", "", "  "); !errors.Is(err, ErrEmpty) {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "zalo", "A", "", "", "", strings.Repeat("é", MaxBodyRunes+1)); !errors.Is(err, ErrBodyLong) {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "zalo", "A", "", "", strings.Repeat("t", MaxTitleRunes+1), "x"); !errors.Is(err, ErrTitleLong) {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "zalo", "A", "", "", "", strings.Repeat("é", MaxBodyRunes)); err != nil {
		t.Fatalf("exact limit: %v", err)
	}
	for i := 1; i < MaxPerChat; i++ {
		if _, err := s.Create(ctx, "zalo", "A", "", "", "", "n"); err != nil {
			t.Fatalf("note %d: %v", i, err)
		}
	}
	if _, err := s.Create(ctx, "zalo", "A", "", "", "", "n"); !errors.Is(err, ErrTooMany) {
		t.Fatalf("cap: %v", err)
	}
	if _, err := s.Create(ctx, "zalo", "B", "", "", "", "n"); err != nil {
		t.Fatalf("other chat unaffected: %v", err)
	}
	l, total, _ := s.List(ctx, "zalo", "A", 500)
	if len(l) != MaxResults || total != MaxPerChat || l[0].ID != MaxPerChat {
		t.Fatalf("list %d total %d first %d", len(l), total, l[0].ID)
	}
}

func TestUpdateAppendReplace(t *testing.T) {
	s := open(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return t0 })
	id, _ := s.Create(ctx, "zalo", "A", "u", "U", "mua sắm", "sữa")
	s.SetClock(func() time.Time { return t0.Add(time.Hour) })
	if err := s.Update(ctx, "zalo", "A", id, nil, "trứng", true); err != nil {
		t.Fatal(err)
	}
	n, _ := s.Get(ctx, "zalo", "A", id)
	if n.Body != "sữa\ntrứng" || n.Title != "mua sắm" || !n.UpdatedAt.After(n.CreatedAt) {
		t.Fatalf("append: %+v", n)
	}
	if err := s.Update(ctx, "zalo", "A", id, nil, "chỉ còn", false); err != nil {
		t.Fatal(err)
	}
	n, _ = s.Get(ctx, "zalo", "A", id)
	if n.Body != "chỉ còn" {
		t.Fatalf("replace: %q", n.Body)
	}
	// Append may not exceed the body limit.
	s.Update(ctx, "zalo", "A", id, nil, strings.Repeat("a", MaxBodyRunes-7), false)
	if err := s.Update(ctx, "zalo", "A", id, nil, "bbbbbbbb", true); !errors.Is(err, ErrBodyLong) {
		t.Fatalf("append over limit: %v", err)
	}
	if err := s.Update(ctx, "zalo", "A", id, nil, "", true); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty append: %v", err)
	}
}

func TestSearchFoldsDiacritics(t *testing.T) {
	s := open(t)
	s.Create(ctx, "zalo", "A", "", "", "Wifi nhà", "Mật khẩu: abc123")
	s.Create(ctx, "zalo", "A", "", "", "", "danh sách mua sắm")
	r, _ := s.Search(ctx, "zalo", "A", "mat khau WIFI", 10)
	if len(r) != 1 || r[0].ID != 1 {
		t.Fatalf("%+v", r)
	}
	r, _ = s.Search(ctx, "zalo", "A", "mua sam", 10)
	if len(r) != 1 || r[0].ID != 2 {
		t.Fatalf("%+v", r)
	}
	if r, _ := s.Search(ctx, "zalo", "A", "   ", 10); len(r) != 0 {
		t.Fatal("blank query")
	}
}

func TestMigrateIdempotentAndPersistent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "n.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	s.Create(ctx, "zalo", "A", "", "", "", "keep")
	s.Close()
	s, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, err := s.Get(ctx, "zalo", "A", 1); err != nil || n.Body != "keep" {
		t.Fatalf("%+v %v", n, err)
	}
}

func TestAdminListing(t *testing.T) {
	s := open(t)
	s.Create(ctx, "zalo", "A", "", "", "", "a1")
	s.Create(ctx, "zalo", "A", "", "", "", "a2")
	s.Create(ctx, "zalo", "B", "", "", "", "b1")
	cs, err := s.Chats(ctx)
	if err != nil || len(cs) != 2 {
		t.Fatalf("%v %v", cs, err)
	}
	ns, _ := s.ChatNotes(ctx, "zalo", "A")
	if len(ns) != 2 || ns[0].ID != 2 {
		t.Fatalf("%+v", ns)
	}
}

func TestSnippet(t *testing.T) {
	if got := Snippet(strings.Repeat("ắ", 500)); len([]rune(got)) != SnippetRunes {
		t.Fatalf("len %d", len([]rune(got)))
	}
	if Snippet("a\n\n b") != "a b" {
		t.Fatal("whitespace")
	}
}
