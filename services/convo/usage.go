package convo

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// UsageFilter narrows the usage reports. Zero values mean "any"; From is
// inclusive and To exclusive (Vietnam-day boundaries are chosen by the caller).
type UsageFilter struct {
	Platform string
	From, To time.Time
}

type UsageDay struct {
	Day string `json:"day"` // YYYY-MM-DD, Vietnam time (UTC+7)
	UsageSum
}

type UsageChat struct {
	Platform string `json:"platform"`
	ChatID   string `json:"chat_id"`
	Name     string `json:"name"`
	UsageSum
}

type UsageUser struct {
	Platform string `json:"platform"`
	UserID   string `json:"user_id"`
	Name     string `json:"name"`
	UsageSum
}

type UsageModel struct {
	Model  string `json:"model"`
	Priced bool   `json:"priced"` // a price is configured for this model
	UsageSum
}

// usageGroup runs the aggregate grouped by dim (a fixed SQL expression chosen in
// this file) and model, folding prices in Go so unpriced models are visible.
func (s *Store) usageGroup(ctx context.Context, f UsageFilter, dim string, extra string, fold func(k [3]string, model string, price Price, ok bool, turns, p, c, t int)) error {
	var a []any
	w := "1=1"
	if f.Platform != "" {
		w += " AND platform = ?"
		a = append(a, f.Platform)
	}
	w += timeClause("created_at", f.From, f.To, &a)
	rows, err := s.db.QueryContext(ctx, `SELECT `+dim+`, model, COUNT(*), SUM(prompt_tokens), SUM(completion_tokens), SUM(total_tokens)
		FROM usage WHERE `+w+extra+` GROUP BY 1, 2, 3, model`, a...)
	if err != nil {
		return fmt.Errorf("convo: usage: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k [3]string
		var model string
		var n, p, c, t int64
		if err := rows.Scan(&k[0], &k[1], &k[2], &model, &n, &p, &c, &t); err != nil {
			return err
		}
		price, ok := s.priceFor(model)
		fold(k, model, price, ok, int(n), int(p), int(c), int(t))
	}
	return rows.Err()
}

const (
	dimAll  = `'', '', ''`
	dimDay  = `date(created_at/1000,'unixepoch','+7 hours'), '', ''`
	dimChat = `platform, chat_id, ''`
	dimUser = `platform, user_id, ''`
)

// UsageTotals sums all usage in the filter.
func (s *Store) UsageTotals(ctx context.Context, f UsageFilter) (UsageSum, error) {
	var u UsageSum
	err := s.usageGroup(ctx, f, dimAll, "", func(_ [3]string, _ string, pr Price, ok bool, n, p, c, t int) { u.add(pr, ok, n, p, c, t) })
	u.finish()
	return u, err
}

// UsageByDay returns one row per Vietnam-time day, ascending. When both From and
// To are set (and span at most 366 days) days without usage are included as zero
// rows so the series is continuous.
func (s *Store) UsageByDay(ctx context.Context, f UsageFilter) ([]UsageDay, error) {
	m := map[string]*UsageDay{}
	err := s.usageGroup(ctx, f, dimDay, "", func(k [3]string, _ string, pr Price, ok bool, n, p, c, t int) {
		d := m[k[0]]
		if d == nil {
			d = &UsageDay{Day: k[0]}
			m[k[0]] = d
		}
		d.add(pr, ok, n, p, c, t)
	})
	if err != nil {
		return nil, err
	}
	if !f.From.IsZero() && !f.To.IsZero() && f.To.Sub(f.From) <= 366*24*time.Hour {
		ict := time.FixedZone("ICT", 7*3600)
		d := f.From.In(ict)
		d = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, ict) // whole ICT days, even for non-midnight bounds
		for ; d.Before(f.To); d = d.AddDate(0, 0, 1) {
			if k := d.Format("2006-01-02"); m[k] == nil {
				m[k] = &UsageDay{Day: k}
			}
		}
	}
	out := make([]UsageDay, 0, len(m))
	for _, d := range m {
		d.finish()
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
	return out, nil
}

// UsageByChat returns chats ordered by total tokens (desc), paged; total is the
// number of chats with usage.
func (s *Store) UsageByChat(ctx context.Context, f UsageFilter, p Page) ([]UsageChat, int, error) {
	m := map[[2]string]*UsageChat{}
	err := s.usageGroup(ctx, f, dimChat, ` AND chat_id != ''`, func(k [3]string, _ string, pr Price, ok bool, n, pt, c, t int) {
		key := [2]string{k[0], k[1]}
		g := m[key]
		if g == nil {
			g = &UsageChat{Platform: k[0], ChatID: k[1]}
			m[key] = g
		}
		g.add(pr, ok, n, pt, c, t)
	})
	if err != nil {
		return nil, 0, err
	}
	all := make([]UsageChat, 0, len(m))
	for _, g := range m {
		g.finish()
		all = append(all, *g)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].TotalTokens != all[j].TotalTokens {
			return all[i].TotalTokens > all[j].TotalTokens
		}
		return all[i].Platform+all[i].ChatID < all[j].Platform+all[j].ChatID
	})
	lo, hi := window(len(all), p)
	page := all[lo:hi]
	if err := s.fillNames(ctx, `SELECT c.platform, c.chat_id, `+chatNameExpr+` FROM chats c`, func(pf, id, nm string) {
		for i := range page {
			if page[i].Platform == pf && page[i].ChatID == id {
				page[i].Name = nm
			}
		}
	}); err != nil {
		return nil, 0, err
	}
	return page, len(all), nil
}

// UsageByUser is UsageByChat grouped by user.
func (s *Store) UsageByUser(ctx context.Context, f UsageFilter, p Page) ([]UsageUser, int, error) {
	m := map[[2]string]*UsageUser{}
	err := s.usageGroup(ctx, f, dimUser, ` AND user_id != ''`, func(k [3]string, _ string, pr Price, ok bool, n, pt, c, t int) {
		key := [2]string{k[0], k[1]}
		g := m[key]
		if g == nil {
			g = &UsageUser{Platform: k[0], UserID: k[1]}
			m[key] = g
		}
		g.add(pr, ok, n, pt, c, t)
	})
	if err != nil {
		return nil, 0, err
	}
	all := make([]UsageUser, 0, len(m))
	for _, g := range m {
		g.finish()
		all = append(all, *g)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].TotalTokens != all[j].TotalTokens {
			return all[i].TotalTokens > all[j].TotalTokens
		}
		return all[i].Platform+all[i].UserID < all[j].Platform+all[j].UserID
	})
	lo, hi := window(len(all), p)
	page := all[lo:hi]
	if err := s.fillNames(ctx, `SELECT platform, user_id, display_name FROM users`, func(pf, id, nm string) {
		for i := range page {
			if page[i].Platform == pf && page[i].UserID == id {
				page[i].Name = nm
			}
		}
	}); err != nil {
		return nil, 0, err
	}
	return page, len(all), nil
}

// UsageByModel returns one row per model (tokens desc). Rows with no recorded
// model have Model "".
func (s *Store) UsageByModel(ctx context.Context, f UsageFilter) ([]UsageModel, error) {
	m := map[string]*UsageModel{}
	err := s.usageGroup(ctx, f, dimAll, "", func(_ [3]string, model string, pr Price, ok bool, n, p, c, t int) {
		g := m[model]
		if g == nil {
			g = &UsageModel{Model: model, Priced: ok}
			m[model] = g
		}
		g.add(pr, ok, n, p, c, t)
	})
	if err != nil {
		return nil, err
	}
	out := make([]UsageModel, 0, len(m))
	for _, g := range m {
		g.finish()
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalTokens != out[j].TotalTokens {
			return out[i].TotalTokens > out[j].TotalTokens
		}
		return out[i].Model < out[j].Model
	})
	return out, nil
}

func window(n int, p Page) (lo, hi int) {
	limit, off := p.norm()
	if off > n {
		off = n
	}
	hi = off + limit
	if hi > n {
		hi = n
	}
	return off, hi
}

func (s *Store) fillNames(ctx context.Context, q string, set func(pf, id, nm string)) error {
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return fmt.Errorf("convo: usage names: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var pf, id, nm string
		if err := rows.Scan(&pf, &id, &nm); err != nil {
			return err
		}
		set(pf, id, nm)
	}
	return rows.Err()
}
