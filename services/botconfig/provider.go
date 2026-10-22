package botconfig

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"sync/atomic"

	"everything-you-need/m/services/convo"
)

// Backend is the persistence the provider needs (implemented by *convo.Store).
type Backend interface {
	ListBotSettings(ctx context.Context, platform string) ([]convo.BotSettingRow, error)
	ApplyBotSettings(ctx context.Context, platform string, puts []convo.BotSettingRow, deletes []string, actor string) error
}

// ErrSecretsDisabled is returned when saving a secret without a valid BOTCONFIG_SECRET.
var ErrSecretsDisabled = errors.New("botconfig: secrets disabled (BOTCONFIG_SECRET missing or shorter than 32 characters)")

// ValidationError lists the problems of a rejected Save, by key.
type ValidationError struct{ Problems map[string]string }

func (e *ValidationError) Error() string {
	keys := make([]string, 0, len(e.Problems))
	for k := range e.Problems {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	msg := "botconfig: invalid settings:"
	for _, k := range keys {
		msg += fmt.Sprintf(" %s (%s);", k, e.Problems[k])
	}
	return msg
}

// Provider serves immutable snapshots and applies saves atomically.
type Provider struct {
	be       Backend
	key      []byte // nil = secrets disabled
	defaults map[string]Defaults
	snaps    map[string]*atomic.Pointer[Snapshot]
	mu       sync.Mutex // serialises Save/Reset
}

// New builds a provider for the managed bots and loads their snapshots. An empty
// or short secret is not an error: secrets are then disabled.
func New(ctx context.Context, be Backend, secret string, defaults map[string]Defaults) (*Provider, error) {
	p := &Provider{be: be, defaults: defaults, snaps: map[string]*atomic.Pointer[Snapshot]{}}
	if k, err := deriveKey(secret); err == nil {
		p.key = k
	} else if secret != "" {
		log.Printf("botconfig: BOTCONFIG_SECRET is shorter than %d characters: secret settings disabled", MinSecretLen)
	}
	for _, b := range Bots() {
		if !b.Managed {
			continue
		}
		p.snaps[b.Platform] = new(atomic.Pointer[Snapshot])
		if err := p.reload(ctx, b.Platform); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// SecretsDisabled reports whether secret settings cannot be saved.
func (p *Provider) SecretsDisabled() bool { return p.key == nil }

// Defaults returns the env/config defaults of platform (for UIs).
func (p *Provider) Defaults(platform string) Defaults { return p.defaults[platform] }

// Snapshot returns the current snapshot, or nil for an unmanaged platform.
func (p *Provider) Snapshot(platform string) *Snapshot {
	if a := p.snaps[platform]; a != nil {
		return a.Load()
	}
	return nil
}

func (p *Provider) reload(ctx context.Context, platform string) error {
	rows, err := p.be.ListBotSettings(ctx, platform)
	if err != nil {
		return err
	}
	p.snaps[platform].Store(p.build(platform, rows))
	return nil
}

func (p *Provider) build(platform string, rows []convo.BotSettingRow) *Snapshot {
	sn := &Snapshot{platform: platform, vals: map[string]any{}, override: map[string]bool{}, updated: map[string]convo.BotSettingRow{}}
	for _, sp := range Specs(platform) {
		txt := p.defaults[platform][sp.Key]
		v, err := parseInput(sp, txt) // defaults come from trusted config: parsed, not policy-checked
		if err != nil {
			v, _ = parseInput(sp, "")
		}
		if sp.Secret {
			v = Secret(v.(string))
		}
		sn.vals[sp.Key] = v
	}
	for _, r := range rows {
		sp, ok := specFor(platform, r.Key)
		if !ok {
			continue // key no longer in the schema
		}
		var v any
		if sp.Secret {
			if p.key == nil || !r.IsSecret {
				sn.Problems = append(sn.Problems, r.Key)
				continue
			}
			pt, err := open(p.key, r.Value, platform+"/"+r.Key)
			if err != nil {
				sn.Problems = append(sn.Problems, r.Key)
				continue
			}
			v = Secret(pt)
		} else {
			dv, err := decodeValue(sp, r.Value)
			if err != nil || r.IsSecret {
				sn.Problems = append(sn.Problems, r.Key)
				continue
			}
			v = dv
		}
		sn.vals[r.Key], sn.override[r.Key], sn.updated[r.Key] = v, true, r
	}
	if len(sn.Problems) > 0 {
		log.Printf("botconfig: %s: %d stored setting(s) unusable, defaults apply: %v", platform, len(sn.Problems), sn.Problems)
	}
	return sn
}

// Save validates every change (key → textual value), writes them in one
// transaction and then swaps the snapshot. On any failure nothing changes.
func (p *Provider) Save(ctx context.Context, platform string, changes map[string]string, actor string) error {
	return p.Apply(ctx, platform, changes, nil, actor)
}

// Reset deletes the override of key so the env/config default applies again.
func (p *Provider) Reset(ctx context.Context, platform, key, actor string) error {
	return p.Apply(ctx, platform, nil, []string{key}, actor)
}

// Apply validates every change, then writes the changes and the resets in ONE
// transaction followed by ONE snapshot swap. A key in both lists is reset.
func (p *Provider) Apply(ctx context.Context, platform string, changes map[string]string, resets []string, actor string) error {
	if p.snaps[platform] == nil {
		return fmt.Errorf("botconfig: platform %q is not managed", platform)
	}
	bad := map[string]string{}
	reset := map[string]bool{}
	for _, k := range resets {
		if _, ok := specFor(platform, k); !ok {
			bad[k] = "unknown setting"
			continue
		}
		reset[k] = true
	}
	var puts []convo.BotSettingRow
	keys := make([]string, 0, len(changes))
	for k := range changes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if reset[k] {
			continue
		}
		sp, ok := specFor(platform, k)
		if !ok {
			bad[k] = "unknown setting"
			continue
		}
		v, err := parseInput(sp, changes[k])
		if err == nil {
			v, err = sp.validate(v)
		}
		if err != nil {
			bad[k] = err.Error()
			continue
		}
		row := convo.BotSettingRow{Platform: platform, Key: k, IsSecret: sp.Secret}
		if sp.Secret {
			if p.key == nil {
				return ErrSecretsDisabled
			}
			row.Value, err = seal(p.key, v.(string), platform+"/"+k)
		} else {
			row.Value, err = encodeValue(v)
		}
		if err != nil {
			bad[k] = "cannot encode"
			continue
		}
		puts = append(puts, row)
	}
	if len(bad) > 0 {
		return &ValidationError{bad}
	}
	var dels []string
	for k := range reset {
		dels = append(dels, k)
	}
	sort.Strings(dels)
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.be.ApplyBotSettings(ctx, platform, puts, dels, actor); err != nil {
		return err
	}
	return p.reload(ctx, platform)
}

// Rotate re-encrypts every stored secret of the managed bots from oldSecret to
// newSecret (both ≥32 chars). Rows that cannot be decrypted with oldSecret abort
// the rotation before anything is written.
func Rotate(ctx context.Context, be Backend, oldSecret, newSecret, actor string) (int, error) {
	ok, err := deriveKey(oldSecret)
	if err != nil {
		return 0, err
	}
	nk, err := deriveKey(newSecret)
	if err != nil {
		return 0, err
	}
	type job struct {
		platform string
		puts     []convo.BotSettingRow
	}
	var jobs []job
	n := 0
	for _, b := range Bots() {
		if !b.Managed {
			continue
		}
		rows, err := be.ListBotSettings(ctx, b.Platform)
		if err != nil {
			return 0, err
		}
		j := job{platform: b.Platform}
		for _, r := range rows {
			if !r.IsSecret {
				continue
			}
			pt, err := open(ok, r.Value, b.Platform+"/"+r.Key)
			if err != nil {
				return 0, fmt.Errorf("botconfig: %s/%s does not decrypt with the old secret; nothing changed", b.Platform, r.Key)
			}
			r.Value, err = seal(nk, pt, b.Platform+"/"+r.Key)
			if err != nil {
				return 0, err
			}
			j.puts = append(j.puts, r)
			n++
		}
		jobs = append(jobs, j)
	}
	for _, j := range jobs {
		if len(j.puts) == 0 {
			continue
		}
		if err := be.ApplyBotSettings(ctx, j.platform, j.puts, nil, actor); err != nil {
			return 0, err
		}
	}
	return n, nil
}
