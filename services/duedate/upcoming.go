package duedate

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// DueItem is one not-done sheet task due within a requested window.
type DueItem struct{ Title, Due, Bucket string }

const maxUpcomingDays = 90

// Upcoming lists not-done tasks due from today through today+days
// (Asia/Ho_Chi_Minh), soonest first. Read-only: it never touches the digest
// state file and sends nothing. Bucket is one of "today",
// "tomorrow", or "in N days". Due is formatted YYYY-MM-DD.
func (s *Service) Upcoming(ctx context.Context, days int) ([]DueItem, error) {
	if days < 0 {
		days = 0
	}
	if days > maxUpcomingDays {
		days = maxUpcomingDays
	}
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		loc = time.FixedZone("UTC+7", 7*60*60)
	}
	today := dayOnly(s.nowFunc().In(loc))
	rows, err := s.fetchCSV(ctx)
	if err != nil {
		return nil, fmt.Errorf("duedate: fetch sheet: %w", err)
	}
	tasks := parseTasks(rows, today.Year(), loc)
	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].due.Before(tasks[j].due) })

	var out []DueItem
	for _, t := range tasks {
		d := dayOnly(t.due)
		diff := int(d.Sub(today).Hours() / 24)
		if d.Before(today) || diff > days {
			continue
		}
		bucket := fmt.Sprintf("in %d days", diff)
		switch diff {
		case 0:
			bucket = "today"
		case 1:
			bucket = "tomorrow"
		}
		out = append(out, DueItem{Title: t.name, Due: d.Format("2006-01-02"), Bucket: bucket})
	}
	return out, nil
}
