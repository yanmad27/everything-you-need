package botconfig

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"everything-you-need/m/services/convo"
)

const (
	secretA  = "0123456789abcdef0123456789abcdef-A"
	secretB  = "fedcba9876543210fedcba9876543210-B"
	sentinel = "SENTINEL-sk-9f8e7d6c5b4a39281706"
)

func testDefaults() map[string]Defaults {
	return map[string]Defaults{PlatformZalo: {
		KeyEnabled: "true", KeyLLMBaseURL: "https://api.example.com", KeyLLMModel: "m-default", KeyLLMAPIKey: "env-key-1234567890",
		KeyFetchAllow: "a.com, b.org", KeyRatePerMin: "6", KeyRateBurst: "3", KeyRetentionDays: "90", KeyToolFetch: "true",
	}}
}

func newStore(t *testing.T) *convo.Store {
	t.Helper()
	st, err := convo.Open(filepath.Join(t.TempDir(), "b.db"), convo.Options{NoBackground: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func newProv(t *testing.T, st *convo.Store, secret string) *Provider {
	t.Helper()
	p, err := New(context.Background(), st, secret, testDefaults())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func capture(t *testing.T) *bytes.Buffer {
	var b bytes.Buffer
	log.SetOutput(&b)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &b
}

func TestMigrationIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	for i := 0; i < 3; i++ {
		st, err := convo.Open(path, convo.Options{NoBackground: true})
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		if _, err := st.ListBotSettings(context.Background(), "zalo"); err != nil {
			t.Fatal(err)
		}
		st.Close()
	}
}

func TestPrecedenceAndReset(t *testing.T) {
	st := newStore(t)
	p := newProv(t, st, secretA)
	ctx := context.Background()
	if z := p.Snapshot("zalo").Zalo(); z.LLMModel != "m-default" || !z.Enabled || strings.Join(z.FetchAllowDomains, ",") != "a.com,b.org" || p.Snapshot("zalo").Overridden(KeyLLMModel) {
		t.Fatalf("defaults: %v", z)
	}
	if err := p.Save(ctx, "zalo", map[string]string{KeyLLMModel: "m-override", KeyEnabled: "false", KeyFetchAllow: "News.example.com\nx.org"}, "owner"); err != nil {
		t.Fatal(err)
	}
	sn := p.Snapshot("zalo")
	if z := sn.Zalo(); z.LLMModel != "m-override" || z.Enabled || strings.Join(z.FetchAllowDomains, ",") != "news.example.com,x.org" || !sn.Overridden(KeyLLMModel) {
		t.Fatalf("override: %v", z)
	}
	// A restart with a different env default never touches the override.
	d := testDefaults()
	d[PlatformZalo][KeyLLMModel] = "m-new-env"
	d[PlatformZalo][KeyRetentionDays] = "30"
	p2, err := New(ctx, st, secretA, d)
	if err != nil {
		t.Fatal(err)
	}
	z := p2.Snapshot("zalo").Zalo()
	if z.LLMModel != "m-override" || z.RetentionDays != 30 {
		t.Fatalf("env change vs override: %v", z)
	}
	if err := p2.Reset(ctx, "zalo", KeyLLMModel, "owner"); err != nil {
		t.Fatal(err)
	}
	if z := p2.Snapshot("zalo").Zalo(); z.LLMModel != "m-new-env" || p2.Snapshot("zalo").Overridden(KeyLLMModel) {
		t.Fatalf("reset: %v", z)
	}
	if sn.Zalo().LLMModel != "m-override" {
		t.Fatal("snapshot mutated")
	}
}

func TestSecretRoundTripAndMask(t *testing.T) {
	st := newStore(t)
	p := newProv(t, st, secretA)
	ctx := context.Background()
	if err := p.Save(ctx, "zalo", map[string]string{KeyLLMAPIKey: sentinel}, "owner"); err != nil {
		t.Fatal(err)
	}
	sn := p.Snapshot("zalo")
	if sn.Secret(KeyLLMAPIKey).Reveal() != sentinel {
		t.Fatal("round trip")
	}
	rows, _ := st.ListBotSettings(ctx, "zalo")
	if len(rows) != 1 || !rows[0].IsSecret || bytes.Contains(rows[0].Value, []byte(sentinel)) || rows[0].Value[0] != 1 {
		t.Fatalf("stored row: %+v", rows)
	}
	for _, v := range sn.View(p.Defaults("zalo")) {
		if v.Key == KeyLLMAPIKey && (v.Value != nil || v.Default != nil || !v.Masked.Set || v.Masked.Last4 != sentinel[len(sentinel)-4:] || !v.Overridden) {
			t.Fatalf("view: %+v", v)
		}
	}
	_ = p.Save(ctx, "zalo", map[string]string{KeyLLMAPIKey: sentinel}, "owner")
	rows2, _ := st.ListBotSettings(ctx, "zalo")
	if bytes.Equal(rows[0].Value, rows2[0].Value) {
		t.Fatal("nonce reused")
	}
	if err := p.Save(ctx, "zalo", map[string]string{KeyLLMAPIKey: ""}, "o"); err == nil {
		t.Fatal("empty secret accepted")
	}
}

func TestWrongKeyFailsClosedAndAADBinding(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	p := newProv(t, st, secretA)
	_ = p.Save(ctx, "zalo", map[string]string{KeyLLMAPIKey: sentinel, KeySerperAPIKey: "serper-" + sentinel}, "o")
	logs := capture(t)
	pb := newProv(t, st, secretB)
	sn := pb.Snapshot("zalo")
	if sn.Secret(KeyLLMAPIKey).Reveal() != "env-key-1234567890" || len(sn.Problems) != 2 {
		t.Fatalf("wrong key: %v", sn.Problems)
	}
	if strings.Contains(logs.String(), sentinel) {
		t.Fatal("secret in log")
	}
	rows, _ := st.ListBotSettings(ctx, "zalo")
	var a, b convo.BotSettingRow
	for _, r := range rows {
		if r.Key == KeyLLMAPIKey {
			a = r
		} else if r.Key == KeySerperAPIKey {
			b = r
		}
	}
	a2, b2 := a, b
	a2.Value, b2.Value = b.Value, a.Value
	if err := st.ApplyBotSettings(ctx, "zalo", []convo.BotSettingRow{a2, b2}, nil, "attacker"); err != nil {
		t.Fatal(err)
	}
	sn = newProv(t, st, secretA).Snapshot("zalo")
	if len(sn.Problems) != 2 || sn.Secret(KeyLLMAPIKey).Reveal() == sentinel {
		t.Fatalf("swapped rows must fail closed: %v", sn.Problems)
	}
	k, _ := deriveKey(secretA)
	blob, _ := seal(k, "x", "zalo/k")
	if _, err := open(k, blob, "zalo/k"); err != nil {
		t.Fatal(err)
	}
	for name, mod := range map[string]func([]byte){"version": func(b []byte) { b[0] = 2 }, "tag": func(b []byte) { b[len(b)-1] ^= 1 }, "nonce": func(b []byte) { b[3] ^= 1 }} {
		c := append([]byte(nil), blob...)
		mod(c)
		if _, err := open(k, c, "zalo/k"); err == nil {
			t.Errorf("%s tamper accepted", name)
		}
	}
	if _, err := open(k, blob, "zalo/other"); err == nil {
		t.Fatal("aad ignored")
	}
}

func TestSecretsDisabledWithoutSecret(t *testing.T) {
	for _, s := range []string{"", "short-secret", secretA[:31]} {
		st := newStore(t)
		p := newProv(t, st, s)
		if !p.SecretsDisabled() {
			t.Fatalf("%q enabled", s)
		}
		if err := p.Save(context.Background(), "zalo", map[string]string{KeyLLMAPIKey: sentinel}, "o"); err != ErrSecretsDisabled {
			t.Fatalf("%q: %v", s, err)
		}
		if rows, _ := st.ListBotSettings(context.Background(), "zalo"); len(rows) != 0 {
			t.Fatal("row written")
		}
		if err := p.Save(context.Background(), "zalo", map[string]string{KeyLLMModel: "ok-model"}, "o"); err != nil {
			t.Fatal(err)
		}
	}
	if newProv(t, newStore(t), secretA).SecretsDisabled() {
		t.Fatal("valid secret reported disabled")
	}
}

func TestValidationRejects(t *testing.T) {
	st := newStore(t)
	p := newProv(t, st, secretA)
	ctx := context.Background()
	doms := make([]string, 51)
	for i := range doms {
		doms[i] = fmt.Sprintf("d%d.example.org", i)
	}
	chats := make([]string, 101)
	for i := range chats {
		chats[i] = fmt.Sprint("c", i)
	}
	bad := map[string]string{
		KeyLLMBaseURL: "http://api.example.com", KeyLLMModel: "bad model!", KeyRatePerMin: "0", KeyRateBurst: "21", KeyRetentionDays: "5000",
		KeyPersona: strings.Repeat("a", 4001), KeyPrices: "m:1", KeyEnabled: "maybe", KeyFetchAllow: "github.io", KeyPrivateChats: "bad id",
	}
	for k, v := range bad {
		err := p.Save(ctx, "zalo", map[string]string{k: v}, "o")
		if _, ok := err.(*ValidationError); !ok {
			t.Errorf("%s=%q accepted (%v)", k, clipStr(v, 20), err)
		}
	}
	for _, u := range []string{"https://127.0.0.1", "https://10.0.0.5/v1", "https://localhost", "https://[::1]", "https://intranet", "https://x.internal", "https://u:p@api.example.com",
		"https://api.example.com:8443", "https://api.example.com?x=1", "https://1.2.3.4.5", "ftp://api.example.com", "https://box.local"} {
		if err := p.Save(ctx, "zalo", map[string]string{KeyLLMBaseURL: u}, "o"); err == nil {
			t.Errorf("base url %q accepted", u)
		}
	}
	for _, d := range []string{"10.0.0.1", "foo.github.io", "my.blogspot.com", "a.netlify.app", "x.vercel.app", "y.pages.dev", "z.herokuapp.com", "q.appspot.com", "r.azurewebsites.net", "localhost", "*.example.com", "-bad.com", "a.com,b.github.io"} {
		if err := p.Save(ctx, "zalo", map[string]string{KeyFetchAllow: d}, "o"); err == nil {
			t.Errorf("allowlist %q accepted", d)
		}
	}
	if err := p.Save(ctx, "zalo", map[string]string{KeyFetchAllow: strings.Join(doms, ",")}, "o"); err == nil {
		t.Error("51 domains accepted")
	}
	if err := p.Save(ctx, "zalo", map[string]string{KeyPrivateChats: strings.Join(chats, ",")}, "o"); err == nil {
		t.Error("101 chats accepted")
	}
	if err := p.Save(ctx, "zalo", map[string]string{"nope": "1"}, "o"); err == nil {
		t.Error("unknown key accepted")
	}
	if err := p.Save(ctx, "telegram", map[string]string{KeyLLMModel: "x"}, "o"); err == nil {
		t.Error("unmanaged platform accepted")
	}
	if err := p.Save(ctx, "zalo", map[string]string{KeyLLMModel: "fine", KeyRatePerMin: "999"}, "o"); err == nil {
		t.Fatal("batch accepted")
	}
	if rows, _ := st.ListBotSettings(ctx, "zalo"); len(rows) != 0 {
		t.Fatalf("partial write: %+v", rows)
	}
	if ev, _ := st.AdminAudit(ctx); len(ev) != 0 {
		t.Fatal("audit on rejected save")
	}
	ok := map[string]string{KeyLLMBaseURL: "https://api.example.com/v1/", KeyLLMModel: "gpt-6-sol", KeyRatePerMin: "120", KeyRateBurst: "20", KeyRetentionDays: "3650",
		KeyPrices: "gpt-6-sol:2:10, ns:m:1:2:3", KeyFetchAllow: "Docs.Example.com, docs.example.com", KeyPrivateChats: "abc_1, def-2", KeyPersona: strings.Repeat("é", 4000)}
	if err := p.Save(ctx, "zalo", ok, "o"); err != nil {
		t.Fatal(err)
	}
	z := p.Snapshot("zalo").Zalo()
	if z.LLMBaseURL != "https://api.example.com/v1" || len(z.FetchAllowDomains) != 1 || len(z.Prices) != 2 || z.Prices[1].Model != "ns:m:1" {
		t.Fatalf("%v %+v", z, z.Prices)
	}
}

func TestAuditRowsWithoutValues(t *testing.T) {
	st := newStore(t)
	p := newProv(t, st, secretA)
	ctx := context.Background()
	_ = p.Save(ctx, "zalo", map[string]string{KeyLLMModel: "audit-model-value", KeyLLMAPIKey: sentinel}, "alice")
	_ = p.Reset(ctx, "zalo", KeyLLMModel, "bob")
	_ = p.Reset(ctx, "zalo", KeyLLMModel, "bob") // no row: no audit
	ev, _ := st.AdminAudit(ctx)
	var got []string
	for _, e := range ev {
		got = append(got, e.Event+" "+e.Detail+" "+e.Actor)
		if strings.Contains(fmt.Sprint(e), sentinel) || strings.Contains(fmt.Sprint(e), "audit-model-value") || e.At == 0 {
			t.Fatalf("bad audit row %+v", e)
		}
	}
	want := "setting_changed zalo/llm_api_key alice,setting_changed zalo/llm_model alice,setting_reset zalo/llm_model bob"
	if strings.Join(got, ",") != want {
		t.Fatalf("audit: %v", got)
	}
}

func TestSentinelNeverPrinted(t *testing.T) {
	st := newStore(t)
	logs := capture(t)
	p := newProv(t, st, secretA)
	ctx := context.Background()
	_ = p.Save(ctx, "zalo", map[string]string{KeyLLMAPIKey: sentinel, KeySerperAPIKey: sentinel + "2"}, "o")
	sn := p.Snapshot("zalo")
	z := sn.Zalo()
	var sb strings.Builder
	h := slog.New(slog.NewTextHandler(&sb, nil))
	h.Info("x", "z", z, "sn", sn, "key", z.LLMAPIKey)
	for _, v := range []any{sn, z, &z, z.LLMAPIKey, sn.Secret(KeySerperAPIKey), sn.View(p.Defaults("zalo"))} {
		fmt.Fprintf(&sb, "%v|%+v|%#v|%s|", v, v, v, v)
		log.Printf("%v %+v", v, v)
	}
	js, _ := (Secret(sentinel)).MarshalJSON()
	sb.Write(js)
	if strings.Contains(sb.String(), sentinel) || strings.Contains(logs.String(), sentinel) {
		t.Fatalf("sentinel leaked:\n%s\n%s", sb.String(), logs.String())
	}
	if !strings.Contains(sb.String(), "[redacted]") {
		t.Fatal("no redaction marker")
	}
}

func TestSnapshotSwapAtomicUnderConcurrency(t *testing.T) {
	st := newStore(t)
	p := newProv(t, st, secretA)
	ctx := context.Background()
	var stop atomic.Bool
	var wg sync.WaitGroup
	var bad atomic.Int64
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				z := p.Snapshot("zalo").Zalo()
				// each Save sets both keys to a matching pair; a torn snapshot would differ
				if z.LLMModel != "m-default" && z.LLMModel != fmt.Sprint("m", z.RateLimitPerMin) {
					bad.Add(1)
				}
			}
		}()
	}
	for i := 1; i <= 60; i++ {
		if err := p.Save(ctx, "zalo", map[string]string{KeyLLMModel: fmt.Sprint("m", i), KeyRatePerMin: fmt.Sprint(i)}, "o"); err != nil {
			t.Error(err)
		}
	}
	stop.Store(true)
	wg.Wait()
	if bad.Load() != 0 {
		t.Fatalf("%d torn snapshots", bad.Load())
	}
}

type failBackend struct{ Backend }

func (failBackend) ApplyBotSettings(context.Context, string, []convo.BotSettingRow, []string, string) error {
	return fmt.Errorf("disk full")
}

func TestFailedSaveKeepsOldSnapshot(t *testing.T) {
	st := newStore(t)
	p := newProv(t, st, secretA)
	_ = p.Save(context.Background(), "zalo", map[string]string{KeyLLMModel: "before"}, "o")
	old := p.Snapshot("zalo")
	p.be = failBackend{st}
	if err := p.Save(context.Background(), "zalo", map[string]string{KeyLLMModel: "after"}, "o"); err == nil {
		t.Fatal("expected error")
	}
	if p.Snapshot("zalo") != old || p.Snapshot("zalo").Zalo().LLMModel != "before" {
		t.Fatal("snapshot changed after failure")
	}
}

func TestRotate(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	p := newProv(t, st, secretA)
	_ = p.Save(ctx, "zalo", map[string]string{KeyLLMAPIKey: sentinel, KeyLLMModel: "keep"}, "o")
	if _, err := Rotate(ctx, st, secretB, secretA, "x"); err == nil {
		t.Fatal("wrong old secret accepted")
	}
	n, err := Rotate(ctx, st, secretA, secretB, "rotate")
	if err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	sn := newProv(t, st, secretB).Snapshot("zalo")
	if sn.Secret(KeyLLMAPIKey).Reveal() != sentinel || sn.Zalo().LLMModel != "keep" || len(sn.Problems) != 0 {
		t.Fatal("rotated secret unreadable")
	}
	if len(newProv(t, st, secretA).Snapshot("zalo").Problems) != 1 {
		t.Fatal("old secret still works")
	}
}

func TestBotsRegistry(t *testing.T) {
	var tg *Bot
	for _, b := range Bots() {
		if b.Platform == "telegram" {
			b := b
			tg = &b
		}
	}
	if tg == nil || tg.Managed || Specs("telegram") != nil || newProv(t, newStore(t), "").Snapshot("telegram") != nil {
		t.Fatal("telegram must be listed, unmanaged")
	}
}

func TestEmptyBaseURLAndDuplicatePricesRejected(t *testing.T) {
	st := newStore(t)
	p := newProv(t, st, secretA)
	ctx := context.Background()
	for k, v := range map[string]string{KeyLLMBaseURL: "  ", KeyLLMAPIKey: "", KeyPrices: "a:1:2, A:3:4"} {
		if err := p.Save(ctx, "zalo", map[string]string{k: v}, "o"); err == nil {
			t.Errorf("%s=%q accepted", k, v)
		}
	}
	// an empty prices override is valid: it means "built-in defaults only"
	if err := p.Save(ctx, "zalo", map[string]string{KeyPrices: ""}, "o"); err != nil {
		t.Fatal(err)
	}
	if !p.Snapshot("zalo").Overridden(KeyPrices) || len(p.Snapshot("zalo").Prices()) != 0 {
		t.Fatal("empty prices override")
	}
	for _, sp := range Specs("zalo") {
		want := map[string]bool{KeyPersona: true, KeyRetentionDays: true, KeyLLMBaseURL: true, KeyLLMAPIKey: true, KeySerperAPIKey: true, KeyFetchAllow: true,
			KeyPrivateChats: true, KeyToolFetch: true, KeyToolWebSearch: true}[sp.Key]
		if sp.Sensitive != want {
			t.Errorf("%s sensitive=%v want %v", sp.Key, sp.Sensitive, want)
		}
	}
}

func TestApplyChangesAndResetsAtomically(t *testing.T) {
	st := newStore(t)
	p := newProv(t, st, secretA)
	ctx := context.Background()
	_ = p.Save(ctx, "zalo", map[string]string{KeyLLMModel: "m1", KeyRatePerMin: "10"}, "o")
	before := p.Snapshot("zalo")
	if err := p.Apply(ctx, "zalo", map[string]string{KeyRatePerMin: "20"}, []string{KeyLLMModel}, "o"); err != nil {
		t.Fatal(err)
	}
	after := p.Snapshot("zalo")
	if after == before || after.Zalo().RateLimitPerMin != 20 || after.Overridden(KeyLLMModel) {
		t.Fatal("apply")
	}
	// invalid change: the reset must not happen either
	_ = p.Save(ctx, "zalo", map[string]string{KeyLLMModel: "m2"}, "o")
	if err := p.Apply(ctx, "zalo", map[string]string{KeyRatePerMin: "0"}, []string{KeyLLMModel}, "o"); err == nil || !p.Snapshot("zalo").Overridden(KeyLLMModel) {
		t.Fatal("partial apply")
	}
	// a key in both lists is reset, not written
	if err := p.Apply(ctx, "zalo", map[string]string{KeyLLMModel: "m3"}, []string{KeyLLMModel}, "o"); err != nil || p.Snapshot("zalo").Overridden(KeyLLMModel) {
		t.Fatal("reset wins")
	}
}
