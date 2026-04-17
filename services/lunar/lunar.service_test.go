package lunar

import (
	"testing"
	"time"
)

func TestSolarToLunar_KnownDates(t *testing.T) {
	cases := []struct {
		name                      string
		solarYear, solarMonth, solarDay int
		wantLunarDay, wantLunarMonth, wantLunarYear int
	}{
		{"Tết Giáp Thìn 2024", 2024, 2, 10, 1, 1, 2024},
		{"Rằm tháng Giêng 2024", 2024, 2, 24, 15, 1, 2024},
		{"Tết Ất Tỵ 2025", 2025, 1, 29, 1, 1, 2025},
		{"Rằm tháng Giêng 2025", 2025, 2, 12, 15, 1, 2025},
		{"Tết Bính Ngọ 2026", 2026, 2, 17, 1, 1, 2026},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotDay, gotMonth, gotYear, _ := SolarToLunar(tc.solarYear, tc.solarMonth, tc.solarDay, 7)
			if gotDay != tc.wantLunarDay || gotMonth != tc.wantLunarMonth || gotYear != tc.wantLunarYear {
				t.Errorf("SolarToLunar(%d-%02d-%02d) = (%d/%d/%d), want (%d/%d/%d)",
					tc.solarYear, tc.solarMonth, tc.solarDay,
					gotDay, gotMonth, gotYear,
					tc.wantLunarDay, tc.wantLunarMonth, tc.wantLunarYear)
			}
		})
	}
}

func TestLunarMonthLength(t *testing.T) {
	// Tháng Giêng 2024 âm lịch bắt đầu 2024-02-10, tháng 2 âm bắt đầu 2024-03-10
	// → tháng 1 có 29 ngày, ngày cuối là 29
	gotDay, gotMonth, _, _ := SolarToLunar(2024, 3, 9, 7)
	if gotMonth != 1 || gotDay != 29 {
		t.Errorf("expected 29/1 lunar for 2024-03-09, got %d/%d", gotDay, gotMonth)
	}
}

func TestIsNotifyDay(t *testing.T) {
	cases := []struct {
		name               string
		lunarDay           int
		isLastDayOfMonth   bool
		wantNotify         bool
		wantLabel          string
	}{
		{"mùng 1", 1, false, true, "mùng 1"},
		{"14 âm", 14, false, true, "14 âm lịch"},
		{"rằm 15", 15, false, true, "rằm (15 âm lịch)"},
		{"13 âm (hôm trước 14)", 13, false, true, "hôm trước 14 âm lịch"},
		{"29 không phải cuối tháng", 29, false, false, ""},
		{"29 là cuối tháng (hôm trước mùng 1)", 29, true, true, "hôm trước mùng 1"},
		{"30 là cuối tháng (hôm trước mùng 1)", 30, true, true, "hôm trước mùng 1"},
		{"ngày 5 không trigger", 5, false, false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			notify, label := IsNotifyDay(tc.lunarDay, tc.isLastDayOfMonth)
			if notify != tc.wantNotify || label != tc.wantLabel {
				t.Errorf("IsNotifyDay(%d, %v) = (%v, %q), want (%v, %q)",
					tc.lunarDay, tc.isLastDayOfMonth, notify, label, tc.wantNotify, tc.wantLabel)
			}
		})
	}
}

func TestIsLastDayOfLunarMonth(t *testing.T) {
	// 2024-03-09 solar là 29/1 lunar và là ngày cuối tháng (2024-03-10 solar là 1/2 lunar)
	loc := time.FixedZone("UTC+7", 7*60*60)
	lastDay := time.Date(2024, 3, 9, 12, 0, 0, 0, loc)
	if !IsLastDayOfLunarMonth(lastDay, 7) {
		t.Errorf("expected 2024-03-09 to be last day of lunar month")
	}

	notLast := time.Date(2024, 3, 8, 12, 0, 0, 0, loc)
	if IsLastDayOfLunarMonth(notLast, 7) {
		t.Errorf("expected 2024-03-08 NOT to be last day of lunar month")
	}
}
