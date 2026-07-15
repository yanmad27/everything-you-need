package duedate

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Column indexes in the wedding MASTER PLAN sheet (0-based).
const (
	colGroup   = 1 // section-level name (e.g. "Chụp hình pre-wedding")
	colItem    = 2
	colContent = 3
	colPIC     = 5
	colDueDate = 6
	colStatus  = 7
)

// SendFunc delivers the rendered digest to a destination.
type SendFunc func(message string) error

// Service scans a Google Sheet's Due Date column once a day and sends a Telegram
// digest of tasks whose deadline is one of the configured offsets away from today.
type Service struct {
	csvURL    string
	statePath string
	client    *http.Client
	nowFunc   func() time.Time
}

func NewService(csvURL, statePath string, nowFunc func() time.Time) *Service {
	if nowFunc == nil {
		nowFunc = time.Now
	}
	if statePath == "" {
		statePath = "data/duedate_sent.txt"
	}
	return &Service{
		csvURL:    csvURL,
		statePath: statePath,
		client:    &http.Client{Timeout: 30 * time.Second},
		nowFunc:   nowFunc,
	}
}

// offsetBucket is one "remind before" window, ordered most-urgent first.
type offsetBucket struct {
	label    string
	daysFrom func(due time.Time) time.Time // trigger date = due shifted back
}

// buckets: day-of, 1 day, 3 days, 1 week, 2 weeks, 1 month before the due date.
var buckets = []offsetBucket{
	{"🔴 Hôm nay đến hạn", func(d time.Time) time.Time { return d }},
	{"⏳ Còn 1 ngày (ngày mai)", func(d time.Time) time.Time { return d.AddDate(0, 0, -1) }},
	{"⏳ Còn 3 ngày", func(d time.Time) time.Time { return d.AddDate(0, 0, -3) }},
	{"⏳ Còn 1 tuần", func(d time.Time) time.Time { return d.AddDate(0, 0, -7) }},
	{"⏳ Còn 2 tuần", func(d time.Time) time.Time { return d.AddDate(0, 0, -14) }},
	{"⏳ Còn 1 tháng", func(d time.Time) time.Time { return d.AddDate(0, -1, 0) }},
}

type task struct {
	name string
	pic  string
	due  time.Time
}

// RunDigest fetches the sheet, matches tasks against the offset buckets for
// today (Asia/Ho_Chi_Minh), and sends one combined digest. It is a no-op when
// nothing is due or the digest was already sent today.
func (s *Service) RunDigest(ctx context.Context, send SendFunc) error {
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		loc = time.FixedZone("UTC+7", 7*60*60)
	}
	today := dayOnly(s.nowFunc().In(loc))

	if s.alreadySentToday(today) {
		log.Printf("duedate: digest already sent for %s, skipping", today.Format("2006-01-02"))
		return nil
	}

	rows, err := s.fetchCSV(ctx)
	if err != nil {
		return fmt.Errorf("duedate: fetch sheet: %w", err)
	}
	tasks := parseTasks(rows, today.Year(), loc)
	log.Printf("duedate: parsed %d tasks with a due date", len(tasks))

	grouped, total := matchBuckets(tasks, today)
	if total == 0 {
		log.Printf("duedate: nothing due today, skipping")
		return nil
	}

	msg := render(today, grouped)
	if err := send(msg); err != nil {
		return fmt.Errorf("duedate: send: %w", err)
	}
	s.markSentToday(today)
	log.Printf("duedate: sent digest with %d reminders", total)
	return nil
}

// matchBuckets groups tasks under each offset bucket whose trigger date equals
// today, returning the per-bucket slices and the total number of matches.
func matchBuckets(tasks []task, today time.Time) ([][]task, int) {
	grouped := make([][]task, len(buckets))
	total := 0
	for bi, b := range buckets {
		for _, t := range tasks {
			if dayOnly(b.daysFrom(t.due)).Equal(today) {
				grouped[bi] = append(grouped[bi], t)
				total++
			}
		}
	}
	return grouped, total
}

func (s *Service) fetchCSV(ctx context.Context) ([][]string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", s.csvURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	r := csv.NewReader(resp.Body)
	r.FieldsPerRecord = -1 // rows have ragged column counts
	return r.ReadAll()
}

var dateRe = regexp.MustCompile(`(\d{1,2})/(\d{1,2})(?:/(\d{2,4}))?`)

func parseTasks(rows [][]string, defaultYear int, loc *time.Location) []task {
	var out []task
	for _, r := range rows {
		if len(r) <= colStatus {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(r[colStatus]), "Done") {
			continue
		}
		due, ok := parseDue(r[colDueDate], defaultYear, loc)
		if !ok {
			continue
		}
		name := firstNonEmpty(r[colItem], r[colContent], r[colGroup])
		if name == "" {
			continue
		}
		out = append(out, task{name: name, pic: strings.TrimSpace(r[colPIC]), due: due})
	}
	return out
}

// parseDue extracts a DD/MM[/YYYY] date from a cell like "Thứ 5, 20/08" or
// "CN, 4/10/2026". Missing year defaults to defaultYear, bumped to next year if
// that would put the date more than a month in the past (handles year rollover).
func parseDue(cell string, defaultYear int, loc *time.Location) (time.Time, bool) {
	m := dateRe.FindStringSubmatch(cell)
	if m == nil {
		return time.Time{}, false
	}
	day, _ := strconv.Atoi(m[1])
	month, _ := strconv.Atoi(m[2])
	if day < 1 || day > 31 || month < 1 || month > 12 {
		return time.Time{}, false
	}
	year := defaultYear
	if m[3] != "" {
		y, _ := strconv.Atoi(m[3])
		if y < 100 {
			y += 2000
		}
		year = y
	}
	d := time.Date(year, time.Month(month), day, 0, 0, 0, 0, loc)
	if m[3] == "" {
		now := dayOnly(time.Now().In(loc))
		if d.Before(now.AddDate(0, -1, 0)) {
			d = d.AddDate(1, 0, 0)
		}
	}
	return d, true
}

func render(today time.Time, grouped [][]task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "📅 Nhắc lịch cưới hôm nay\n🗓 %s\n", today.Format("02/01/2006"))
	for bi, tasks := range grouped {
		if len(tasks) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s:\n", buckets[bi].label)
		for _, t := range tasks {
			line := fmt.Sprintf(" • %s (hạn %s", t.name, t.due.Format("02/01"))
			if t.pic != "" {
				line += ", " + t.pic
			}
			line += ")"
			b.WriteString(line + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func dayOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func (s *Service) alreadySentToday(today time.Time) bool {
	data, err := os.ReadFile(s.statePath)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == today.Format("2006-01-02")
}

func (s *Service) markSentToday(today time.Time) {
	if err := os.WriteFile(s.statePath, []byte(today.Format("2006-01-02")), 0o644); err != nil {
		log.Printf("duedate: write state %s: %v", s.statePath, err)
	}
}
