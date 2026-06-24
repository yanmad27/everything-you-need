package news

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"
)

// Service builds and sends the daily news digest.
type Service struct {
	store       *Store
	ranker      *Ranker
	searcher    *Searcher
	feeds       []string
	maxItems    int
	window      time.Duration
	dedupWindow time.Duration
	nowFunc     func() time.Time
}

// SendFunc delivers the rendered digest message (markdown) to a destination.
type SendFunc func(message string) error

func NewService(store *Store, ranker *Ranker, searcher *Searcher, feeds []string, maxItems, windowHours, dedupDays int, nowFunc func() time.Time) *Service {
	if maxItems <= 0 {
		maxItems = 7
	}
	if windowHours <= 0 {
		windowHours = 24
	}
	if dedupDays <= 0 {
		dedupDays = 7
	}
	if nowFunc == nil {
		nowFunc = time.Now
	}
	return &Service{
		store:       store,
		ranker:      ranker,
		searcher:    searcher,
		feeds:       feeds,
		maxItems:    maxItems,
		window:      time.Duration(windowHours) * time.Hour,
		dedupWindow: time.Duration(dedupDays) * 24 * time.Hour,
		nowFunc:     nowFunc,
	}
}

// RunDigest performs the full pipeline: fetch → window → dedup → rank → send →
// record. Best-effort: on OpenAI failure it sends a titles-only fallback. The
// rendering timezone is Asia/Ho_Chi_Minh.
func (s *Service) RunDigest(ctx context.Context, send SendFunc) error {
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		loc = time.FixedZone("UTC+7", 7*60*60)
	}
	now := s.nowFunc().In(loc)

	items, failed := FetchFeeds(ctx, s.feeds, 0)
	if len(failed) > 0 {
		log.Printf("news: %d/%d feeds failed: %v", len(failed), len(s.feeds), failed)
	}

	if s.searcher != nil {
		searchItems := s.searcher.FetchAll(ctx)
		if len(searchItems) > 0 {
			log.Printf("news: serper returned %d items", len(searchItems))
			items = append(items, searchItems...)
		}
	}

	if len(items) == 0 {
		return fmt.Errorf("news: no items fetched from any feed")
	}

	items = dedupeByURL(items)
	recent := FilterRecent(items, now, s.window)
	if len(recent) == 0 {
		recent = items // window too tight / no dates → fall back to all
	}

	// Newest first so the fallback and the candidate list lead with fresh items.
	sort.SliceStable(recent, func(a, b int) bool {
		return recent[a].PublishedAt.After(recent[b].PublishedAt)
	})

	unseen, err := s.store.FilterUnseen(ctx, recent, now.Add(-s.dedupWindow))
	if err != nil {
		log.Printf("news: dedup lookup failed, using all recent items: %v", err)
		unseen = recent
	}
	if len(unseen) == 0 {
		log.Printf("news: all %d recent items already posted; skipping", len(recent))
		return nil
	}

	candidates := unseen
	if len(candidates) > 60 {
		candidates = candidates[:60] // cap tokens sent to OpenAI
	}

	message, posted := s.buildMessage(ctx, candidates, now, len(s.feeds))
	if message == "" {
		log.Printf("news: no relevant items found, skipping digest")
		return nil
	}

	if err := send(message); err != nil {
		return fmt.Errorf("news: send failed: %w", err)
	}

	if err := s.store.MarkPosted(ctx, posted); err != nil {
		log.Printf("news: mark posted failed: %v", err)
	}
	if purged, err := s.store.PurgeOld(ctx, now.Add(-2*s.dedupWindow)); err != nil {
		log.Printf("news: purge failed: %v", err)
	} else if purged > 0 {
		log.Printf("news: purged %d old records", purged)
	}

	log.Printf("news: digest sent with %d items", len(posted))
	return nil
}

// buildMessage tries OpenAI ranking and falls back to a titles-only digest.
// Returns the message plus the FeedItems that were actually included (to record).
func (s *Service) buildMessage(ctx context.Context, candidates []FeedItem, now time.Time, sourceCount int) (string, []FeedItem) {
	ranked, err := s.rankWithRetry(ctx, candidates)
	if err != nil {
		log.Printf("news: ranking failed: %v", err)
		return "", nil
	}
	if len(ranked) == 0 {
		log.Printf("news: no items matched topic filter")
		return "", nil
	}

	posted := make([]FeedItem, 0, len(ranked))
	for _, item := range ranked {
		posted = append(posted, FeedItem{URL: item.URL, Title: item.EnglishTitle})
	}
	return FormatDigest(ranked, now, sourceCount), posted
}

func (s *Service) rankWithRetry(ctx context.Context, candidates []FeedItem) ([]RankedItem, error) {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		ranked, err := s.ranker.Rank(ctx, candidates, s.maxItems)
		if err == nil {
			return ranked, nil
		}
		lastErr = err
		log.Printf("news: rank attempt %d failed: %v", attempt, err)
		if attempt < 3 {
			time.Sleep(time.Duration(attempt) * 3 * time.Second)
		}
	}
	return nil, lastErr
}
