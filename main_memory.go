package main

import (
	"context"
	"log"
	"time"

	"everything-you-need/m/services/botconfig"
	"everything-you-need/m/services/config"
	"everything-you-need/m/services/memory"
)

// Memory wiring. The single construction site is buildMemoryStore; main()
// keeps the result in the variable memoryStore (nil when it could not be
// opened or Zalo is off). Downstream wiring is one line each:
//
//	chat (T2):  pass memoryStore to the chat service / zaloTools
//	admin (T3): pass memoryStore in admin.Options (a nil *memory.Store must be
//	            converted to a nil interface, like noteData does for notes)
//
// Telegram has no memory path and never receives the store.

const (
	defaultMemoryMaxPerUser    = 200
	defaultMemoryRetentionDays = 365
	memoryPruneEvery           = 24 * time.Hour
)

// memoryDBPath puts memory.db in the convo database's directory (same data
// volume as notes.db), never the working directory itself.
func memoryDBPath(convoPath string) string { return siblingDBPath(convoPath, "memory.db") }

// memorySettings is the live memory.* configuration, with the documented
// defaults until the bot settings provider is up.
func memorySettings() botconfig.MemorySettings {
	if p := botProvider.Load(); p != nil {
		if m := p.Snapshot(botconfig.PlatformZalo).Zalo().Memory; m.MaxPerUser > 0 && m.RetentionDays > 0 {
			return m
		}
	}
	return botconfig.MemorySettings{Enabled: true, MaxPerUser: defaultMemoryMaxPerUser, RetentionDays: defaultMemoryRetentionDays}
}

// buildMemoryStore opens the per-user memory database next to the convo
// database. It is a separate file on purpose (convo retention never touches
// it). The store is opened even when tools.memory is off so the admin can still
// review and delete stored data; whether memory is used is decided per request
// from the live setting. A failure disables memory only. Only the path and the
// enabled flag are logged.
func buildMemoryStore(cfg *config.Config) *memory.Store {
	path := memoryDBPath(cfg.Convo.DBPath)
	st, err := memory.Open(path)
	if err != nil {
		log.Printf("Memory store open failed: %v (memory disabled)", err)
		return nil
	}
	m := memorySettings()
	st.SetMaxPerUser(m.MaxPerUser)
	log.Printf("Memory store initialized (db=%s, enabled=%t)", path, m.Enabled)
	return st
}

// pruner is the part of *memory.Store the prune loop needs.
type pruner interface {
	Prune(ctx context.Context, now time.Time, retention time.Duration) (int, error)
}

// startMemoryPrune prunes once now and then every `every` until ctx ends,
// honouring the live memory.retention_days at each run. Safe with a nil store.
func startMemoryPrune(ctx context.Context, st *memory.Store, every time.Duration) {
	if st == nil {
		return
	}
	go runMemoryPrune(ctx, st, every, func() time.Duration {
		return time.Duration(memorySettings().RetentionDays) * 24 * time.Hour
	}, time.Now)
}

func runMemoryPrune(ctx context.Context, st pruner, every time.Duration, retention func() time.Duration, now func() time.Time) {
	once := func() {
		if _, err := st.Prune(ctx, now(), retention()); err != nil && ctx.Err() == nil {
			log.Printf("Memory prune failed: %v", err)
		}
	}
	once()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			once()
		}
	}
}
