package news

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWebSearcherShapeParseClamp(t *testing.T) {
	var gotPath, gotKey string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey = r.URL.Path, r.Header.Get("X-API-KEY")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"organic":[{"title":"T","link":"http://a","snippet":"S"}],"news":[{"title":"N","link":"http://n","snippet":"s","source":"src","date":"1 hour ago"}]}`))
	}))
	defer srv.Close()
	w := NewWebSearcher("secret-key")
	w.baseURL = srv.URL

	res, err := w.Search(context.Background(), strings.Repeat("é", 300), 99)
	if err != nil || len(res) != 1 || res[0].Title != "T" || res[0].URL != "http://a" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if gotPath != "/search" || gotKey != "secret-key" || gotBody["gl"] != "vn" || gotBody["hl"] != "vi" || gotBody["num"].(float64) != 5 {
		t.Fatalf("path=%s key=%s body=%v", gotPath, gotKey, gotBody)
	}
	if n := len([]rune(gotBody["q"].(string))); n != 200 {
		t.Fatalf("query runes=%d", n)
	}
	res, err = w.SearchNews(context.Background(), "q", 0)
	if err != nil || len(res) != 1 || res[0].Source != "src" || res[0].Date != "1 hour ago" {
		t.Fatalf("news res=%+v err=%v", res, err)
	}
	if gotPath != "/news" || gotBody["num"].(float64) != 1 {
		t.Fatalf("path=%s body=%v", gotPath, gotBody)
	}
}

func TestWebSearcherKeyRedaction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte("bad key secret-key"))
	}))
	w := NewWebSearcher("secret-key")
	w.baseURL = srv.URL
	_, err := w.Search(context.Background(), "q", 1)
	if err == nil || strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("err=%v", err)
	}
	srv.Close()
	// transport error must not leak the key either (key embedded in base URL path).
	w.baseURL = "http://secret-key.invalid:1"
	_, err = w.Search(context.Background(), "q", 1)
	if err == nil || strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("err=%v", err)
	}
}

func TestRecentPosted(t *testing.T) {
	ctx := context.Background()
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.MarkPosted(ctx, []FeedItem{{Title: "one", URL: "u1"}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := s.MarkPosted(ctx, []FeedItem{{Title: "two", URL: "u2"}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.RecentPosted(ctx, 1)
	if err != nil || len(got) != 1 || got[0].Title != "two" || got[0].URL != "u2" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if got, _ := s.RecentPosted(ctx, 10); len(got) != 2 {
		t.Fatalf("got=%d", len(got))
	}
}

func TestWebSearcherErrorChain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	}))
	defer srv.Close()
	w := NewWebSearcher("secret-key")
	w.baseURL = srv.URL
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := w.Search(ctx, "q", 1)
	if !errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("err=%v", err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	if _, err := w.Search(ctx2, "q", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
