package main

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"everything-you-need/m/services/config"
	"everything-you-need/m/services/memory"
)

func TestMemoryDBPath(t *testing.T) {
	for in, want := range map[string]string{
		"": "data/memory.db", "convo.db": "data/memory.db", "data/convo.db": "data/memory.db", "/var/app/c.db": "/var/app/memory.db",
	} {
		if got := memoryDBPath(in); got != want {
			t.Errorf("memoryDBPath(%q)=%q want %q", in, got, want)
		}
	}
}

func TestBuildMemoryStore(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Convo.DBPath = filepath.Join(dir, "convo.db")
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	st := buildMemoryStore(cfg)
	if st == nil {
		t.Fatalf("store not built: %s", buf.String())
	}
	defer st.Close()
	fi, err := os.Stat(filepath.Join(dir, "memory.db"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("memory.db: %v %v", fi, err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("enabled=true")) || !bytes.Contains(buf.Bytes(), []byte("memory.db")) {
		t.Fatalf("log: %s", buf.String())
	}
	if m := memorySettings(); m.MaxPerUser != 200 || m.RetentionDays != 365 || !m.Enabled {
		t.Fatalf("defaults %+v", m)
	}
}

type fakePruner struct {
	mu   sync.Mutex
	rets []time.Duration
	ch   chan struct{}
}

func (f *fakePruner) Prune(_ context.Context, _ time.Time, r time.Duration) (int, error) {
	f.mu.Lock()
	f.rets = append(f.rets, r)
	f.mu.Unlock()
	f.ch <- struct{}{}
	return 0, nil
}

func TestMemoryPruneLoopHonoursRetention(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := &fakePruner{ch: make(chan struct{}, 10)}
	days := 100
	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		runMemoryPrune(ctx, f, 10*time.Millisecond, func() time.Duration {
			mu.Lock()
			defer mu.Unlock()
			return time.Duration(days) * 24 * time.Hour
		}, time.Now)
		close(done)
	}()
	<-f.ch // immediate run
	mu.Lock()
	days = 30
	mu.Unlock()
	for i := 0; i < 3; i++ { // later ticks see the new setting
		select {
		case <-f.ch:
		case <-time.After(5 * time.Second):
			t.Fatal("no tick")
		}
	}
	cancel()
	<-done
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rets[0] != 100*24*time.Hour || f.rets[len(f.rets)-1] != 30*24*time.Hour {
		t.Fatalf("retentions %v", f.rets)
	}
	var _ pruner = (*memory.Store)(nil)
	startMemoryPrune(context.Background(), nil, time.Hour) // nil store: no-op
}
