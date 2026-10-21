package pricetracker

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrUnknownSource is returned by CachedPriceReader.Get for a source name that
// is not configured.
var ErrUnknownSource = errors.New("unknown price source")

type cacheEntry struct {
	prices    []Price
	fetchedAt time.Time
}

type inflightFetch struct {
	done   chan struct{}
	prices []Price
	at     time.Time
	err    error
}

// CachedPriceReader serves per-source prices from a short TTL cache, with at
// most one live fetch per source at a time.
type CachedPriceReader struct {
	fetch   func(source string) ([]Price, error)
	names   []string
	ttl     time.Duration
	now     func() time.Time
	mu      sync.Mutex
	cache   map[string]cacheEntry
	pending map[string]*inflightFetch
}

func NewCachedPriceReader(svc *PriceTrackerService, ttl time.Duration) *CachedPriceReader {
	names := make([]string, 0, len(svc.sources))
	for _, s := range svc.sources {
		names = append(names, s.GetSourceName())
	}
	return newCachedPriceReader(names, svc.GetPricesFromSource, ttl)
}

func newCachedPriceReader(names []string, fetch func(string) ([]Price, error), ttl time.Duration) *CachedPriceReader {
	return &CachedPriceReader{
		fetch:   fetch,
		names:   names,
		ttl:     ttl,
		now:     time.Now,
		cache:   map[string]cacheEntry{},
		pending: map[string]*inflightFetch{},
	}
}

// Sources returns the configured source names in configuration order.
func (c *CachedPriceReader) Sources() []string {
	return append([]string(nil), c.names...)
}

// Get returns cached prices for source (fetching when stale) and the time they
// were fetched. Failed fetches are not cached.
func (c *CachedPriceReader) Get(ctx context.Context, source string) ([]Price, time.Time, error) {
	known := false
	for _, n := range c.names {
		if n == source {
			known = true
			break
		}
	}
	if !known {
		return nil, time.Time{}, ErrUnknownSource
	}

	c.mu.Lock()
	if e, ok := c.cache[source]; ok && c.now().Sub(e.fetchedAt) < c.ttl {
		c.mu.Unlock()
		return append([]Price(nil), e.prices...), e.fetchedAt, nil
	}
	f, running := c.pending[source]
	if !running {
		f = &inflightFetch{done: make(chan struct{})}
		c.pending[source] = f
		go c.run(source, f)
	}
	c.mu.Unlock()

	select {
	case <-f.done:
		if f.err != nil {
			return nil, time.Time{}, f.err
		}
		return append([]Price(nil), f.prices...), f.at, nil
	case <-ctx.Done():
		return nil, time.Time{}, ctx.Err()
	}
}

func (c *CachedPriceReader) run(source string, f *inflightFetch) {
	prices, err := c.fetch(source)
	at := c.now()
	c.mu.Lock()
	f.prices, f.at, f.err = prices, at, err
	if err == nil {
		c.cache[source] = cacheEntry{prices: prices, fetchedAt: at}
	}
	delete(c.pending, source)
	c.mu.Unlock()
	close(f.done)
}
