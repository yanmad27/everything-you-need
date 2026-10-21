package duedate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUpcomingReadOnly(t *testing.T) {
	csv := "Part,g,Item,Content,Place,PIC,Due Date,Status\n" +
		",,Today task,,,A,\"T4, 15/07/2026\",Not yet\n" +
		",,Tomorrow task,,,A,\"T5, 16/07/2026\",Not yet\n" +
		",,Later task,,,A,\"T4, 22/07/2026\",Not yet\n" +
		",,Far task,,,A,\"T4, 30/09/2026\",Not yet\n" +
		",,Done task,,,A,\"T5, 16/07/2026\",Done\n" +
		",,Past task,,,A,\"T2, 13/07/2026\",Not yet\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(csv)) }))
	defer srv.Close()
	state := filepath.Join(t.TempDir(), "state.txt")
	svc := NewService(srv.URL, state, func() time.Time { return time.Date(2026, 7, 15, 3, 0, 0, 0, ict) })

	got, err := svc.Upcoming(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []DueItem{
		{"Today task", "2026-07-15", "today"},
		{"Tomorrow task", "2026-07-16", "tomorrow"},
		{"Later task", "2026-07-22", "in 7 days"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %+v want %+v", i, got[i], want[i])
		}
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("state file written: %v", err)
	}
}
