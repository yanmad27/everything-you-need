// Package lunar converts solar dates to Vietnamese lunar dates using the
// Hồ Ngọc Đức algorithm (http://www.informatik.uni-leipzig.de/~duc/amlich/).
package lunar

import (
	"math"
	"time"
)

// VietnamTimeZone is the standard offset (hours) used for Vietnamese lunar calendar.
const VietnamTimeZone = 7

// SolarToLunar converts a solar date to Vietnamese lunar date.
// Returns lunar day, lunar month, lunar year, and leap flag (1 if the month is a leap month).
func SolarToLunar(solarYear, solarMonth, solarDay, timeZone int) (lunarDay, lunarMonth, lunarYear, leap int) {
	dayNumber := jdFromDate(solarDay, solarMonth, solarYear)
	k := int(math.Floor((float64(dayNumber) - 2415021.076998695) / 29.530588853))
	monthStart := getNewMoonDay(k+1, timeZone)
	if monthStart > dayNumber {
		monthStart = getNewMoonDay(k, timeZone)
	}
	a11 := getLunarMonth11(solarYear, timeZone)
	b11 := a11
	if a11 >= monthStart {
		lunarYear = solarYear
		a11 = getLunarMonth11(solarYear-1, timeZone)
	} else {
		lunarYear = solarYear + 1
		b11 = getLunarMonth11(solarYear+1, timeZone)
	}
	lunarDay = dayNumber - monthStart + 1
	diff := int(math.Floor(float64(monthStart-a11) / 29))
	leap = 0
	lunarMonth = diff + 11
	if b11-a11 > 365 {
		leapMonthDiff := getLeapMonthOffset(a11, timeZone)
		if diff >= leapMonthDiff {
			lunarMonth = diff + 10
			if diff == leapMonthDiff {
				leap = 1
			}
		}
	}
	if lunarMonth > 12 {
		lunarMonth -= 12
	}
	if lunarMonth >= 11 && diff < 4 {
		lunarYear--
	}
	return
}

// IsLastDayOfLunarMonth reports whether the given solar date is the final day of
// the corresponding lunar month (i.e. the next day starts a new lunar month).
func IsLastDayOfLunarMonth(t time.Time, timeZone int) bool {
	y, m, d := t.Year(), int(t.Month()), t.Day()
	_, todayMonth, _, _ := SolarToLunar(y, m, d, timeZone)
	next := t.AddDate(0, 0, 1)
	_, nextMonth, _, _ := SolarToLunar(next.Year(), int(next.Month()), next.Day(), timeZone)
	return todayMonth != nextMonth
}

// IsEveOfMung1OrRam reports whether the given lunar day is the day before
// mùng 1 (29/30 + last day of month) or rằm (14).
func IsEveOfMung1OrRam(lunarDay int, isLastDayOfMonth bool) bool {
	if lunarDay == 14 {
		return true
	}
	if isLastDayOfMonth && (lunarDay == 29 || lunarDay == 30) {
		return true
	}
	return false
}

// IsNotifyDay decides whether the given lunar day (plus end-of-month signal)
// should trigger a notification, and returns a short Vietnamese label describing why.
// Trigger days: 29/30 (hôm trước mùng 1), 1, 13 (hôm trước 14), 14, 15 (rằm).
func IsNotifyDay(lunarDay int, isLastDayOfMonth bool) (notify bool, label string) {
	if isLastDayOfMonth && (lunarDay == 29 || lunarDay == 30) {
		return true, "hôm trước mùng 1"
	}
	switch lunarDay {
	case 1:
		return true, "mùng 1"
	case 13:
		return true, "hôm trước 14 âm lịch"
	case 14:
		return true, "14 âm lịch"
	case 15:
		return true, "rằm (15 âm lịch)"
	}
	return false, ""
}

func jdFromDate(dd, mm, yy int) int {
	a := (14 - mm) / 12
	y := yy + 4800 - a
	m := mm + 12*a - 3
	jd := dd + (153*m+2)/5 + 365*y + y/4 - y/100 + y/400 - 32045
	if jd < 2299161 {
		jd = dd + (153*m+2)/5 + 365*y + y/4 - 32083
	}
	return jd
}

func newMoon(k int) float64 {
	T := float64(k) / 1236.85
	T2 := T * T
	T3 := T2 * T
	dr := math.Pi / 180
	Jd1 := 2415020.75933 + 29.53058868*float64(k) + 0.0001178*T2 - 0.000000155*T3
	Jd1 += 0.00033 * math.Sin((166.56+132.87*T-0.009173*T2)*dr)
	M := 359.2242 + 29.10535608*float64(k) - 0.0000333*T2 - 0.00000347*T3
	Mpr := 306.0253 + 385.81691806*float64(k) + 0.0107306*T2 + 0.00001236*T3
	F := 21.2964 + 390.67050646*float64(k) - 0.0016528*T2 - 0.00000239*T3
	C1 := (0.1734-0.000393*T)*math.Sin(M*dr) + 0.0021*math.Sin(2*dr*M)
	C1 -= 0.4068 * math.Sin(Mpr*dr)
	C1 += 0.0161 * math.Sin(dr*2*Mpr)
	C1 -= 0.0004 * math.Sin(dr*3*Mpr)
	C1 += 0.0104 * math.Sin(dr*2*F)
	C1 -= 0.0051 * math.Sin(dr*(M+Mpr))
	C1 -= 0.0074 * math.Sin(dr*(M-Mpr))
	C1 += 0.0004 * math.Sin(dr*(2*F+M))
	C1 -= 0.0004 * math.Sin(dr*(2*F-M))
	C1 -= 0.0006 * math.Sin(dr*(2*F+Mpr))
	C1 += 0.0010 * math.Sin(dr*(2*F-Mpr))
	C1 += 0.0005 * math.Sin(dr*(2*Mpr+M))
	var deltat float64
	if T < -11 {
		deltat = 0.001 + 0.000839*T + 0.0002261*T2 - 0.00000845*T3 - 0.000000081*T*T3
	} else {
		deltat = -0.000278 + 0.000265*T + 0.000262*T2
	}
	return Jd1 + C1 - deltat
}

func sunLongitude(jdn float64) float64 {
	T := (jdn - 2451545.0) / 36525
	T2 := T * T
	dr := math.Pi / 180
	M := 357.52910 + 35999.05030*T - 0.0001559*T2 - 0.00000048*T*T2
	L0 := 280.46645 + 36000.76983*T + 0.0003032*T2
	DL := (1.914600 - 0.004817*T - 0.000014*T2) * math.Sin(dr*M)
	DL += (0.019993 - 0.000101*T) * math.Sin(dr*2*M)
	DL += 0.000290 * math.Sin(dr*3*M)
	L := L0 + DL
	L *= dr
	L -= math.Pi * 2 * math.Floor(L/(math.Pi*2))
	return L
}

func getSunLongitude(dayNumber, timeZone int) int {
	return int(math.Floor(sunLongitude(float64(dayNumber)-0.5-float64(timeZone)/24.0) / math.Pi * 6))
}

func getNewMoonDay(k, timeZone int) int {
	return int(math.Floor(newMoon(k) + 0.5 + float64(timeZone)/24.0))
}

func getLunarMonth11(yy, timeZone int) int {
	off := jdFromDate(31, 12, yy) - 2415021
	k := int(math.Floor(float64(off) / 29.530588853))
	nm := getNewMoonDay(k, timeZone)
	sunLong := getSunLongitude(nm, timeZone)
	if sunLong >= 9 {
		nm = getNewMoonDay(k-1, timeZone)
	}
	return nm
}

func getLeapMonthOffset(a11, timeZone int) int {
	k := int(math.Floor((float64(a11)-2415021.076998695)/29.530588853 + 0.5))
	last := 0
	i := 1
	arc := getSunLongitude(getNewMoonDay(k+i, timeZone), timeZone)
	for {
		last = arc
		i++
		arc = getSunLongitude(getNewMoonDay(k+i, timeZone), timeZone)
		if arc == last || i >= 14 {
			break
		}
	}
	return i - 1
}
