package duedate

import (
	"strings"
	"testing"
	"time"
)

var ict = time.FixedZone("UTC+7", 7*60*60)

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, ict)
}

func TestParseDue(t *testing.T) {
	cases := []struct {
		cell string
		want time.Time
		ok   bool
	}{
		{"Thứ 5, 20/08", date(2026, 8, 20), true},
		{"CN, 4/10/2026", date(2026, 10, 4), true},
		{"Thứ 2, 13/07", date(2026, 7, 13), true},
		{"", time.Time{}, false},
		{"no date here", time.Time{}, false},
	}
	for _, c := range cases {
		got, ok := parseDue(c.cell, 2026, ict)
		if ok != c.ok {
			t.Errorf("parseDue(%q) ok=%v want %v", c.cell, ok, c.ok)
			continue
		}
		if ok && !got.Equal(c.want) {
			t.Errorf("parseDue(%q)=%s want %s", c.cell, got, c.want)
		}
	}
}

func TestParseTasksSkipsDone(t *testing.T) {
	rows := [][]string{
		{"Part", "", "Item", "Content", "Place", "PIC", "Due Date", "Status"}, // header, no valid date
		{"", "", "Lên danh sách", "", "", "Both", "Thứ 5, 20/08", "In-progress"},
		{"", "", "Đã xong", "", "", "Yan", "Thứ 6, 10/07", "Done"}, // skipped
		{"", "", "", "Fallback content name", "", "", "CN, 19/07", "Not yet"},
		{"", "", "No due", "", "", "", "", "Not yet"}, // skipped (no due)
	}
	tasks := parseTasks(rows, 2026, ict)
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks, want 2: %+v", len(tasks), tasks)
	}
	if tasks[0].name != "Lên danh sách" || tasks[1].name != "Fallback content name" {
		t.Errorf("unexpected task names: %q, %q", tasks[0].name, tasks[1].name)
	}
}

func TestMatchBuckets(t *testing.T) {
	due := date(2026, 8, 20)
	tk := []task{{name: "Thử Váy", pic: "Both", due: due}}

	// today -> expected bucket index
	cases := map[string]struct {
		today time.Time
		bkt   int
	}{
		"day-of":  {due, 0},
		"1 day":   {date(2026, 8, 19), 1},
		"3 days":  {date(2026, 8, 17), 2},
		"1 week":  {date(2026, 8, 13), 3},
		"2 weeks": {date(2026, 8, 6), 4},
		"1 month": {date(2026, 7, 20), 5},
	}
	for name, c := range cases {
		grouped, total := matchBuckets(tk, c.today)
		if total != 1 {
			t.Errorf("%s: total=%d want 1", name, total)
			continue
		}
		if len(grouped[c.bkt]) != 1 {
			t.Errorf("%s: expected task in bucket %d (%s), got empty", name, c.bkt, buckets[c.bkt].label)
		}
	}

	// A day that matches no offset produces nothing.
	if _, total := matchBuckets(tk, date(2026, 8, 15)); total != 0 {
		t.Errorf("non-offset day: total=%d want 0", total)
	}
}

func TestRender(t *testing.T) {
	grouped, _ := matchBuckets([]task{{name: "Thử Váy cưới", pic: "Both", due: date(2026, 8, 20)}}, date(2026, 8, 13))
	msg := render(date(2026, 8, 13), grouped)
	if !strings.Contains(msg, "Thử Váy cưới") || !strings.Contains(msg, "Còn 1 tuần") || !strings.Contains(msg, "hạn 20/08") {
		t.Errorf("render missing expected content:\n%s", msg)
	}
}
