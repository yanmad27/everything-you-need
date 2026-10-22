package chat

import (
	"context"
	"errors"
	"net/url"
	"time"

	"everything-you-need/m/services/netguard"
)

// errFetchRefused is the fixed error for destinations the SSRF guard refuses.
var errFetchRefused = errors.New("fetch refused: address not allowed")

// safeHTTP refuses loopback/private/link-local targets at dial time (also on
// redirects and DNS rebinding); see netguard.
var safeHTTP = netguard.NewSafeClient(30 * time.Second)

// fetchPageTextSafe is fetchPageText with the SSRF-guarded client for the
// direct fetch, only http/https URLs, and errors that never contain the URL.
func fetchPageTextSafe(ctx context.Context, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", errUnsupportedURL
	}
	if text, err := getText(ctx, safeHTTP, "https://r.jina.ai/"+rawURL, false); err == nil {
		return text, nil
	}
	text, err := getText(ctx, safeHTTP, rawURL, true)
	if err != nil {
		// Transport errors embed host:port; report a fixed message only.
		if errors.Is(err, netguard.ErrBlocked) {
			return "", errFetchRefused
		}
		return "", errFetchFailed
	}
	return text, nil
}
