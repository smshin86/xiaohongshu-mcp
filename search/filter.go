package search

import (
	"strings"
	"time"
)

// applyPostFilters 는 반환 집합에 대해 포함/제외 키워드, 날짜, duration, 지표 임계치를 적용한다.
func applyPostFilters(items []VideoItem, f SearchFilters) []VideoItem {
	out := make([]VideoItem, 0, len(items))
	from, to, hasDate := parseDateRange(f.DateFrom, f.DateTo)
	for _, it := range items {
		if len(f.IncludeKeywords) > 0 && !containsAnyFold(it.Title+" "+it.Description, f.IncludeKeywords) {
			continue
		}
		if len(f.ExcludeKeywords) > 0 && containsAnyFold(it.Title+" "+it.Description, f.ExcludeKeywords) {
			continue
		}
		if hasDate {
			if !matchDate(it.PublishedAt, from, to) {
				continue
			}
		}
		if f.DurationMin > 0 && it.Duration < f.DurationMin {
			continue
		}
		if f.DurationMax > 0 && it.Duration > f.DurationMax {
			continue
		}
		if f.MinLikes > 0 && (it.Likes == nil || *it.Likes < f.MinLikes) {
			continue
		}
		if f.MinComments > 0 && (it.Comments == nil || *it.Comments < f.MinComments) {
			continue
		}
		if f.MinFavorites > 0 && (it.Favorites == nil || *it.Favorites < f.MinFavorites) {
			continue
		}
		if f.MinViews > 0 && (it.Views == nil || *it.Views < f.MinViews) {
			continue
		}
		out = append(out, it)
	}
	return out
}

func containsAnyFold(haystack string, needles []string) bool {
	h := strings.ToLower(haystack)
	for _, n := range needles {
		if strings.Contains(h, strings.ToLower(n)) {
			return true
		}
	}
	return false
}

// parseDateRange 는 date-only/date-time 문자열을 RFC3339 time 으로 파싱.
// date-only 면 from 은 그날 00:00:00Z, to 는 그날 23:59:59Z 까지 포함(종일).
func parseDateRange(fromStr, toStr string) (from, to time.Time, ok bool) {
	layouts := []string{time.RFC3339, "2006-01-02"}
	var f, t time.Time
	var ferr, terr error
	if fromStr != "" {
		f, ferr = parseAny(fromStr, layouts, true) // start of day
	}
	if toStr != "" {
		t, terr = parseAny(toStr, layouts, false) // end of day
	}
	if fromStr == "" && toStr == "" {
		return time.Time{}, time.Time{}, false
	}
	if ferr != nil || terr != nil {
		return time.Time{}, time.Time{}, false
	}
	if fromStr == "" {
		f = time.Time{}
	}
	if toStr == "" {
		t = time.Time{}
	}
	return f, t, true
}

// parseAny 는 여러 레이아웃을 시도. date-only 면 endOfDay=true 이면 23:59:59Z, false 면 00:00:00Z.
func parseAny(s string, layouts []string, startOfDay bool) (time.Time, error) {
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			if l == "2006-01-02" {
				if startOfDay {
					return t, nil // 이미 00:00:00
				}
				return t.Add(24*time.Hour - time.Second), nil // 그날 23:59:59
			}
			return t, nil
		}
	}
	return time.Time{}, errInvalidDate
}

var errInvalidDate = &parseErr{"invalid date"}

type parseErr struct{ msg string }

func (e *parseErr) Error() string { return e.msg }

// matchDate 는 PublishedAt(RFC3339) 가 [from, to] 구간에 있는지.
func matchDate(publishedAt string, from, to time.Time) bool {
	if publishedAt == "" {
		return false
	}
	pt, err := time.Parse(time.RFC3339, publishedAt)
	if err != nil {
		return false
	}
	if !from.IsZero() && pt.Before(from) {
		return false
	}
	if !to.IsZero() && pt.After(to) {
		return false
	}
	return true
}
