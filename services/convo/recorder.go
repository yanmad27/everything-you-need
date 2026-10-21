package convo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"everything-you-need/m/services/chat"
)

// op is one queued write.
type op struct {
	flush chan struct{} // barrier: closed once everything queued before it is written
	msg   *msgRow
	tool  *toolRow
	use   *usageRow
}

type msgRow struct {
	at                                                 int64
	platform, chatID, chatType, userID, userName       string
	direction, kind, text, imageMime, turnID, errorTag string
	imageBytes                                         int
}

type toolRow struct {
	at                                     int64
	platform, chatID, userID, turnID, name string
	args, result, outcome                  string
	latencyMS                              int64
}

type usageRow struct {
	at                                      int64
	platform, chatID, userID, turnID, model string
	rounds, prompt, completion, total       int
}

var (
	_ chat.Recorder      = (*Store)(nil)
	_ chat.ConvoRecorder = (*Store)(nil)
)

// enqueue never blocks: when the queue is full (or the store is closed) the
// event is dropped and counted.
func (s *Store) enqueue(o op) {
	defer func() { _ = recover() }()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return
	}
	select {
	case s.ch <- o:
	default:
		s.dropped.Add(1)
	}
}

// Flush blocks until every event queued before the call is written (or ctx
// ends). Intended for tests and shutdown paths, never the reply path.
func (s *Store) Flush(ctx context.Context) error {
	done := make(chan struct{})
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil
	}
	select {
	case s.ch <- op{flush: done}:
		s.mu.RUnlock()
	case <-ctx.Done():
		s.mu.RUnlock()
		return ctx.Err()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// RecordConvo stores one inbound/outbound message (text, image metadata or a
// fixed system reply).
func (s *Store) RecordConvo(_ context.Context, e chat.ConvoEvent) {
	dir := e.Direction
	if dir != "in" && dir != "out" {
		return
	}
	kind := e.Kind
	if kind != "text" && kind != "image" && kind != "system" {
		kind = "text"
	}
	uid := e.UserID
	if dir == "out" {
		uid = ""
	}
	s.enqueue(op{msg: &msgRow{at: s.now().UnixMilli(), platform: e.Platform, chatID: e.ChatID, chatType: normChatType(e.ChatType),
		userID: uid, userName: truncRunes(e.UserName, 120), direction: dir, kind: kind,
		text: truncRunes(e.Text, maxTextRunes), imageMime: truncRunes(e.ImageMime, 60), imageBytes: e.ImageBytes,
		turnID: e.TurnID, errorTag: truncRunes(e.ErrTag, 40)}})
}

// RecordMessage stores the LLM usage of one turn.
func (s *Store) RecordMessage(_ context.Context, e chat.MessageEvent) {
	s.enqueue(op{use: &usageRow{at: s.now().UnixMilli(), platform: e.Platform, chatID: e.ChatID, userID: e.UserID, turnID: e.TurnID,
		model: truncRunes(e.Model, 80), rounds: e.Rounds, prompt: e.PromptTokens, completion: e.CompletionTokens, total: e.Total}})
}

// RecordToolCall stores one tool invocation with summarised arguments/result.
func (s *Store) RecordToolCall(_ context.Context, e chat.ToolEvent) {
	s.enqueue(op{tool: &toolRow{at: s.now().UnixMilli(), platform: e.Platform, chatID: e.ChatID, userID: e.UserID, turnID: e.TurnID,
		name: truncRunes(e.Name, 40), args: SummarizeArgs(e.Name, e.Args), result: SummarizeResult(e.Name, e.Outcome, e.Result),
		outcome: e.Outcome, latencyMS: e.Duration.Milliseconds()}})
}

func normChatType(t string) string {
	switch t = strings.ToUpper(strings.TrimSpace(t)); t {
	case "PRIVATE", "GROUP":
		return t
	}
	return ""
}

func truncRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}

// SummarizeArgs reduces raw tool arguments to a short, non-sensitive summary:
// fetch_url keeps only the host, web_search only the query.
func SummarizeArgs(name, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) != nil {
		return "(invalid args)"
	}
	switch name {
	case "fetch_url":
		s, _ := m["url"].(string)
		return truncRunes(hostOnly(s), maxSummaryRunes)
	case "web_search":
		q, _ := m["query"].(string)
		return truncRunes(strings.Join(strings.Fields(q), " "), maxSummaryRunes)
	}
	b, _ := json.Marshal(m)
	return truncRunes(string(b), maxSummaryRunes)
}

func hostOnly(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return "(invalid url)"
	}
	return strings.ToLower(u.Hostname())
}

// SummarizeResult keeps a short result; fetch_url/web_search (web content) and
// failures store only a size/outcome tag.
func SummarizeResult(name, outcome, raw string) string {
	if outcome != "ok" {
		return outcome
	}
	if name == "fetch_url" || name == "web_search" {
		return fmt.Sprintf("ok (%d chars)", utf8.RuneCountInString(raw))
	}
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, ">\n"); strings.HasPrefix(raw, "<tool_result") && i >= 0 {
		raw = strings.TrimSuffix(strings.TrimSpace(raw[i+2:]), "</tool_result>")
	}
	return truncRunes(strings.Join(strings.Fields(raw), " "), maxSummaryRunes)
}

// writer drains the queue in batches, one transaction per batch.
func (s *Store) writer() {
	defer close(s.done)
	batch := make([]op, 0, s.opt.BatchSize)
	t := time.NewTicker(s.opt.FlushEvery)
	defer t.Stop()
	for {
		select {
		case o, ok := <-s.ch:
			if !ok {
				s.flush(batch)
				return
			}
			if o.flush != nil {
				s.flush(batch)
				batch = batch[:0]
				close(o.flush)
				continue
			}
			batch = append(batch, o)
			if len(batch) >= s.opt.BatchSize {
				s.flush(batch)
				batch = batch[:0]
			}
		case <-t.C:
			if len(batch) > 0 {
				s.flush(batch)
				batch = batch[:0]
			}
		}
	}
}

func (s *Store) flush(batch []op) {
	if len(batch) == 0 {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("convo: writer panic recovered")
		}
	}()
	if err := s.write(batch); err != nil {
		log.Printf("convo: write failed (batch=%d): %v", len(batch), err)
	}
}

func (s *Store) write(batch []op) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, o := range batch {
		switch {
		case o.msg != nil:
			err = insertMsg(ctx, tx, o.msg)
		case o.tool != nil:
			t := o.tool
			_, err = tx.ExecContext(ctx, `INSERT INTO tool_calls(turn_id,platform,chat_id,user_id,name,args_summary,result_summary,outcome,latency_ms,created_at)
				VALUES(?,?,?,?,?,?,?,?,?,?)`, t.turnID, t.platform, t.chatID, t.userID, t.name, t.args, t.result, t.outcome, t.latencyMS, t.at)
		case o.use != nil:
			u := o.use
			_, err = tx.ExecContext(ctx, `INSERT INTO usage(turn_id,platform,chat_id,user_id,rounds,prompt_tokens,completion_tokens,total_tokens,model,created_at)
				VALUES(?,?,?,?,?,?,?,?,?,?)`, u.turnID, u.platform, u.chatID, u.userID, u.rounds, u.prompt, u.completion, u.total, u.model, u.at)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func insertMsg(ctx context.Context, tx *sql.Tx, m *msgRow) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO chats(platform,chat_id,chat_type,title,first_seen,last_seen) VALUES(?,?,?,'',?,?)
		ON CONFLICT(platform,chat_id) DO UPDATE SET last_seen=MAX(last_seen,excluded.last_seen),
		chat_type=CASE WHEN excluded.chat_type!='' THEN excluded.chat_type ELSE chats.chat_type END`,
		m.platform, m.chatID, m.chatType, m.at, m.at); err != nil {
		return err
	}
	var uid any
	if m.userID != "" {
		uid = m.userID
		if _, err := tx.ExecContext(ctx, `INSERT INTO users(platform,user_id,display_name,first_seen,last_seen) VALUES(?,?,?,?,?)
			ON CONFLICT(platform,user_id) DO UPDATE SET last_seen=MAX(last_seen,excluded.last_seen),
			display_name=CASE WHEN excluded.display_name!='' THEN excluded.display_name ELSE users.display_name END`,
			m.platform, m.userID, m.userName, m.at, m.at); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO messages(platform,chat_id,user_id,direction,kind,text,image_mime,image_bytes,created_at,turn_id,error_tag)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`, m.platform, m.chatID, uid, m.direction, m.kind, m.text, m.imageMime, m.imageBytes, m.at, m.turnID, m.errorTag)
	return err
}
