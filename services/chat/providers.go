package chat

import (
	"log"
	"reflect"
	"sync"
	"time"
)

// Provider states reported to the admin Bot page.
const (
	ProviderOK       = "ok"
	ProviderNoCredit = "no_credit"
	ProviderAuth     = "auth"
	ProviderError    = "error"
	ProviderUnknown  = "unknown"

	providerSerper   = "serper"
	providerCooldown = 30 * time.Minute
)

// ProviderStatus is the health of an external provider as last seen by the
// chat tools. Detail is fixed text, never upstream body text or a key.
type ProviderStatus struct {
	Name   string
	State  string // ok | no_credit | auth | error | unknown
	Since  time.Time
	Detail string
}

var providerDetail = map[string]string{
	ProviderOK:       "Hoạt động bình thường.",
	ProviderNoCredit: "Hết credit; tạm tắt web_search.",
	ProviderAuth:     "Khoá API bị từ chối; tạm tắt web_search.",
	ProviderError:    "Lần gọi gần nhất bị lỗi.",
	ProviderUnknown:  "Chưa có lần gọi nào.",
}

// providerHealth tracks the web-search provider. One per process, as there is
// one search backend; a changed backend (new key in bot config) resets it.
type providerHealth struct {
	mu     sync.Mutex
	client WebSearch
	state  string
	since  time.Time
	logged bool // provider_unavailable logged for this cooldown
}

var searchHealth = &providerHealth{}

// ProviderStatuses returns the current provider states for the admin UI.
func ProviderStatuses() []ProviderStatus {
	return []ProviderStatus{searchHealth.status()}
}

func (p *providerHealth) status() ProviderStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.state
	if st == "" {
		st = ProviderUnknown
	}
	return ProviderStatus{Name: providerSerper, State: st, Since: p.since, Detail: providerDetail[st]}
}

// track reports whether c is the backend already tracked; a different one
// (key changed) clears the state. Callers hold p.mu.
func (p *providerHealth) track(c WebSearch) {
	if c == nil {
		return
	}
	same := p.client != nil && reflect.TypeOf(c) == reflect.TypeOf(p.client) &&
		reflect.TypeOf(c).Comparable() && c == p.client
	if !same {
		p.client, p.state, p.since, p.logged = c, "", time.Time{}, false
	}
}

// classifyProviderErr maps a search reason tag to a state worth a cooldown.
func classifyProviderErr(reason string) string {
	switch reason {
	case "serper_status_400", "serper_status_402":
		return ProviderNoCredit
	case "serper_status_401", "serper_status_403":
		return ProviderAuth
	}
	return ProviderError
}

func (p *providerHealth) record(c WebSearch, err error, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.track(c)
	st := ProviderOK
	if err != nil {
		st = classifyProviderErr(reasonOf(err))
	}
	if st != p.state {
		p.state, p.since, p.logged = st, now, false
	}
}

// unavailable reports whether web_search should not be offered: the provider
// failed with a credit/auth class less than a cooldown ago. After the cooldown
// the tool is offered again as a probe. logOnce is true on the first refusal
// of a cooldown.
func (p *providerHealth) unavailable(c WebSearch, now time.Time) (down, logOnce bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.track(c)
	if p.state != ProviderNoCredit && p.state != ProviderAuth {
		return false, false
	}
	if now.Sub(p.since) >= providerCooldown {
		return false, false
	}
	first := !p.logged
	p.logged = true
	return true, first
}

// searchDown decides whether web_search is withheld this turn.
func (s *Service) searchDown(c WebSearch, chatID string) bool {
	if c == nil {
		return false
	}
	down, first := searchHealth.unavailable(c, s.tools.Now())
	if down && first {
		log.Printf("chat tool: name=web_search dur=0ms outcome=blocked reason=provider_unavailable chat=%s", maskChat(chatID))
	}
	return down
}
