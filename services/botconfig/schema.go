package botconfig

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"everything-you-need/m/services/convo"
)

// Kind is a setting's value type.
type Kind string

const (
	KindBool   Kind = "bool"
	KindString Kind = "string"
	KindText   Kind = "text"
	KindInt    Kind = "int"
	KindList   Kind = "list"
	KindPrices Kind = "prices"
	KindSecret Kind = "secret"
)

// Spec describes one editable key.
type Spec struct {
	Key       string
	Kind      Kind
	Label     string
	Secret    bool // stored encrypted, never shown
	Sensitive bool // security-relevant: the UI should warn / confirm
	validate  func(v any) (any, error)
}

// Keys of the zalo bot.
const (
	KeyEnabled       = "enabled"
	KeyLLMBaseURL    = "llm_base_url"
	KeyLLMModel      = "llm_model"
	KeyLLMAPIKey     = "llm_api_key"
	KeyPrices        = "prices"
	KeyToolPrices    = "tools.prices"
	KeyToolLunar     = "tools.lunar"
	KeyToolNews      = "tools.news"
	KeyToolWebSearch = "tools.web_search"
	KeyToolFetch     = "tools.fetch"
	KeyToolReminders = "tools.reminders"
	KeyToolDueDates  = "tools.due_dates"
	KeyToolImages    = "tools.images"
	KeySerperAPIKey  = "serper_api_key"
	KeyFetchAllow    = "fetch_allow_domains"
	KeyPrivateChats  = "private_tool_chats"
	KeyRatePerMin    = "rate_limit_per_min"
	KeyRateBurst     = "rate_limit_burst"
	KeyPersona       = "persona"
	KeyRetentionDays = "retention_days"
)

var (
	modelRe = regexp.MustCompile(`^[A-Za-z0-9._:/-]{1,100}$`)
	chatRe  = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,100}$`)
	labelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

	// sharedHostingSuffixes: anyone can publish under these, so an allowlist entry
	// for them would allow attacker-controlled pages.
	sharedHostingSuffixes = []string{"github.io", "githubusercontent.com", "gitlab.io", "blogspot.com", "blogger.com", "wordpress.com",
		"netlify.app", "vercel.app", "pages.dev", "workers.dev", "herokuapp.com", "appspot.com", "azurewebsites.net",
		"web.app", "firebaseapp.com", "onrender.com", "fly.dev", "surge.sh", "glitch.me", "repl.co", "ngrok.io", "ngrok-free.app"}
)

const (
	maxAllowDomains = 50
	maxPrivateChats = 100
	maxPersona      = 4000
)

func intRange(lo, hi int) func(any) (any, error) {
	return func(v any) (any, error) {
		n := v.(int)
		if n < lo || n > hi {
			return nil, fmt.Errorf("must be between %d and %d", lo, hi)
		}
		return n, nil
	}
}

func okAny(v any) (any, error) { return v, nil }

var zaloSpecs = []Spec{
	{Key: KeyEnabled, Kind: KindBool, Label: "Bot enabled", validate: okAny},
	{Key: KeyLLMBaseURL, Kind: KindString, Label: "LLM base URL (https)", Sensitive: true, validate: validBaseURL},
	{Key: KeyLLMModel, Kind: KindString, Label: "LLM model", validate: func(v any) (any, error) {
		if s := v.(string); s != "" && !modelRe.MatchString(s) {
			return nil, fmt.Errorf("model must match [A-Za-z0-9._:/-] and be at most 100 characters")
		}
		return v, nil
	}},
	{Key: KeyLLMAPIKey, Kind: KindSecret, Label: "LLM API key", Secret: true, Sensitive: true, validate: validSecret},
	{Key: KeyPrices, Kind: KindPrices, Label: "Model prices (model:prompt:completion per 1M, comma-separated)", validate: func(v any) (any, error) {
		s := strings.TrimSpace(v.(string))
		if _, bad := convo.ParsePrices(s); len(bad) > 0 {
			return nil, fmt.Errorf("invalid price entries: %s", strings.Join(bad, "; "))
		}
		return s, nil
	}},
	{Key: KeyToolPrices, Kind: KindBool, Label: "Tool: gold/crypto prices", validate: okAny},
	{Key: KeyToolLunar, Kind: KindBool, Label: "Tool: lunar calendar", validate: okAny},
	{Key: KeyToolNews, Kind: KindBool, Label: "Tool: news", validate: okAny},
	{Key: KeyToolWebSearch, Kind: KindBool, Label: "Tool: web search", Sensitive: true, validate: okAny},
	{Key: KeyToolFetch, Kind: KindBool, Label: "Tool: fetch web pages", Sensitive: true, validate: okAny},
	{Key: KeyToolReminders, Kind: KindBool, Label: "Tool: reminders", validate: okAny},
	{Key: KeyToolDueDates, Kind: KindBool, Label: "Tool: due dates", validate: okAny},
	{Key: KeyToolImages, Kind: KindBool, Label: "Image understanding", validate: okAny},
	{Key: KeySerperAPIKey, Kind: KindSecret, Label: "Serper (web search) API key", Secret: true, Sensitive: true, validate: validSecret},
	{Key: KeyFetchAllow, Kind: KindList, Label: "Fetch allowlist domains", Sensitive: true, validate: validDomains},
	{Key: KeyPrivateChats, Kind: KindList, Label: "Chats allowed private tools", Sensitive: true, validate: func(v any) (any, error) {
		l := v.([]string)
		if len(l) > maxPrivateChats {
			return nil, fmt.Errorf("at most %d chat ids", maxPrivateChats)
		}
		for _, c := range l {
			if !chatRe.MatchString(c) {
				return nil, fmt.Errorf("invalid chat id")
			}
		}
		return l, nil
	}},
	{Key: KeyRatePerMin, Kind: KindInt, Label: "Rate limit (messages per minute)", validate: intRange(1, 120)},
	{Key: KeyRateBurst, Kind: KindInt, Label: "Rate limit burst", validate: intRange(1, 20)},
	{Key: KeyPersona, Kind: KindText, Label: "Persona / system prompt", validate: func(v any) (any, error) {
		s := v.(string)
		if utf8.RuneCountInString(s) > maxPersona {
			return nil, fmt.Errorf("at most %d characters", maxPersona)
		}
		if strings.ContainsRune(s, 0) {
			return nil, fmt.Errorf("contains a NUL byte")
		}
		return s, nil
	}},
	{Key: KeyRetentionDays, Kind: KindInt, Label: "Conversation retention (days)", validate: intRange(1, 3650)},
}

func specFor(platform, key string) (Spec, bool) {
	if platform != PlatformZalo {
		return Spec{}, false
	}
	for _, s := range zaloSpecs {
		if s.Key == key {
			return s, true
		}
	}
	return Spec{}, false
}

// Specs returns the editable keys of platform (nil when not managed).
func Specs(platform string) []Spec {
	if platform != PlatformZalo {
		return nil
	}
	return append([]Spec(nil), zaloSpecs...)
}

func validSecret(v any) (any, error) {
	s := v.(string)
	if s == "" {
		return nil, fmt.Errorf("empty; use reset to remove it")
	}
	if len(s) > 500 || strings.ContainsAny(s, "\r\n\x00") {
		return nil, fmt.Errorf("invalid value")
	}
	return s, nil
}

// publicHost reports whether h looks like a public DNS hostname: not an IP, not
// localhost/internal-looking, at least two valid labels.
func publicHost(h string) error {
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	if h == "" {
		return fmt.Errorf("empty host")
	}
	if net.ParseIP(h) != nil || strings.ContainsAny(h, ":[]") {
		return fmt.Errorf("host must be a name, not an IP address")
	}
	labels := strings.Split(h, ".")
	if len(labels) < 2 {
		return fmt.Errorf("host must be a public domain name")
	}
	for _, l := range labels {
		if !labelRe.MatchString(l) {
			return fmt.Errorf("invalid host name")
		}
	}
	if allDigits(labels[len(labels)-1]) {
		return fmt.Errorf("host must be a name, not an IP address")
	}
	switch last := labels[len(labels)-1]; last {
	case "localhost", "local", "internal", "lan", "home", "corp", "intranet", "private", "test", "invalid", "example":
		return fmt.Errorf("host looks private")
	}
	return nil
}

func allDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

// ValidateBaseURL applies the saved-value rules for llm_base_url (https, public
// DNS name, default port) and returns the normalised URL.
func ValidateBaseURL(s string) (string, error) {
	v, err := validBaseURL(s)
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

func validBaseURL(v any) (any, error) {
	s := strings.TrimSpace(v.(string))
	if s == "" {
		return "", nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("must be an https URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("must not contain credentials, query or fragment")
	}
	if p := u.Port(); p != "" && p != "443" {
		return nil, fmt.Errorf("only the default https port is allowed")
	}
	if err := publicHost(u.Hostname()); err != nil {
		return nil, err
	}
	return strings.TrimRight(s, "/"), nil
}

func validDomains(v any) (any, error) {
	in := v.([]string)
	if len(in) > maxAllowDomains {
		return nil, fmt.Errorf("at most %d domains", maxAllowDomains)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, d := range in {
		d = strings.ToLower(strings.TrimSpace(d))
		if err := publicHost(d); err != nil {
			return nil, fmt.Errorf("domain %q: %v", clipStr(d, 40), err)
		}
		for _, sfx := range sharedHostingSuffixes {
			if d == sfx || strings.HasSuffix(d, "."+sfx) {
				return nil, fmt.Errorf("domain %q is shared hosting (anyone can publish there)", clipStr(d, 40))
			}
		}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out, nil
}

func clipStr(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// parseInput converts the textual form (HTML form / env) into the kind's Go value.
func parseInput(sp Spec, in string) (any, error) {
	switch sp.Kind {
	case KindBool:
		switch strings.ToLower(strings.TrimSpace(in)) {
		case "true", "1", "on", "yes":
			return true, nil
		case "false", "0", "off", "no", "":
			return false, nil
		}
		return nil, fmt.Errorf("must be true or false")
	case KindInt:
		n, err := strconv.Atoi(strings.TrimSpace(in))
		if err != nil {
			return nil, fmt.Errorf("must be a whole number")
		}
		return n, nil
	case KindList:
		return splitList(in), nil
	default:
		if sp.Kind != KindText && sp.Kind != KindSecret {
			in = strings.TrimSpace(in)
		}
		return in, nil
	}
}

func splitList(in string) []string {
	out := []string{}
	for _, p := range strings.FieldsFunc(in, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' || r == ';' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// encode/decode the stored (JSON) form of a non-secret value.
func encodeValue(v any) ([]byte, error) { return json.Marshal(v) }

func decodeValue(sp Spec, b []byte) (any, error) {
	switch sp.Kind {
	case KindBool:
		var v bool
		return v, json.Unmarshal(b, &v)
	case KindInt:
		var v int
		return v, json.Unmarshal(b, &v)
	case KindList:
		var v []string
		err := json.Unmarshal(b, &v)
		if v == nil {
			v = []string{}
		}
		return v, err
	default:
		var v string
		return v, json.Unmarshal(b, &v)
	}
}
