package zalobot

import (
	"bytes"
	"context"
	"errors"
	"everything-you-need/m/services/netguard"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"
)

const (
	imageTimeout  = 15 * time.Second
	maxImageBytes = 10 << 20
)

// Fixed-string errors: never include the URL or host.
var (
	errImageURL      = errors.New("image fetch: unsupported url")
	errImageFetch    = errors.New("image fetch: request failed")
	errImageBlocked  = errors.New("image fetch: destination not allowed")
	errImageStatus   = errors.New("image fetch: unexpected response status")
	errImageTooLarge = errors.New("image fetch: image too large")
	errImageType     = errors.New("image fetch: unsupported image type")
)

// sniffImage returns the mime type of jpeg/png/webp/gif data by magic bytes.
func sniffImage(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg"
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(b, []byte("GIF87a")), bytes.HasPrefix(b, []byte("GIF89a")):
		return "image/gif"
	case len(b) >= 12 && bytes.HasPrefix(b, []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return "image/webp"
	}
	return ""
}

// FetchImage downloads an image through the SSRF-safe client (15s, max 10 MiB,
// jpeg/png/webp/gif verified by magic bytes). The returned mime comes from
// the sniffed bytes, not the server's header.
func (z *ZaloBotService) FetchImage(ctx context.Context, rawURL string) ([]byte, string, error) {
	data, mime, err := z.fetchImage(ctx, rawURL)
	// Fixed class and size only: never the URL, host or content.
	log.Printf("zalo image fetch: class=%s bytes=%d", fetchClass(err), len(data))
	return data, mime, err
}

func fetchClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, errImageBlocked):
		return "blocked"
	case errors.Is(err, errImageStatus):
		return "status"
	case errors.Is(err, errImageTooLarge):
		return "too_large"
	case errors.Is(err, errImageType):
		return "type"
	}
	return "fetch"
}

func (z *ZaloBotService) fetchImage(ctx context.Context, rawURL string) ([]byte, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, "", errImageURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", errImageURL
	}
	resp, err := z.imageClient.Do(req)
	if err != nil {
		if errors.Is(err, netguard.ErrBlocked) {
			return nil, "", errImageBlocked
		}
		return nil, "", errImageFetch
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", errImageStatus
	}
	if resp.ContentLength > maxImageBytes {
		return nil, "", errImageTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, "", errImageFetch
	}
	if len(data) > maxImageBytes {
		return nil, "", errImageTooLarge
	}
	mime := sniffImage(data)
	if mime == "" {
		return nil, "", errImageType
	}
	return data, mime, nil
}
