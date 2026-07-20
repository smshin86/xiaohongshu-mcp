package search

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func ptrI(i int) *int64 { return int64Ptr(int64(i)) }

func TestApplyPostFiltersKeywordIncludeExclude(t *testing.T) {
	items := []VideoItem{
		{Platform: "douyin", PostID: "1", Title: "便携风扇 推荐", Likes: ptrI(10)},
		{Platform: "douyin", PostID: "2", Title: "桌面小风扇 避雷", Likes: ptrI(5)},
		{Platform: "douyin", PostID: "3", Title: "无关注键词", Likes: ptrI(100)},
	}
	f := SearchFilters{IncludeKeywords: []string{"风扇"}, ExcludeKeywords: []string{"避雷"}}
	got := applyPostFilters(items, f)
	require.Len(t, got, 1)
	require.Equal(t, "1", got[0].PostID)
}

func TestApplyPostFiltersDateToDateIsEndOfDay(t *testing.T) {
	// date_to 가 "2026-07-15" 이면 그날 23:59:59 까지 포함(경계 포함).
	items := []VideoItem{
		{Platform: "douyin", PostID: "a", PublishedAt: "2026-07-15T23:59:00Z"},
		{Platform: "douyin", PostID: "b", PublishedAt: "2026-07-16T00:00:01Z"},
	}
	f := SearchFilters{DateFrom: "2026-07-01", DateTo: "2026-07-15"}
	got := applyPostFilters(items, f)
	require.Len(t, got, 1)
	require.Equal(t, "a", got[0].PostID)
}

func TestApplyPostFiltersMinLikes(t *testing.T) {
	items := []VideoItem{
		{Platform: "douyin", PostID: "1", Likes: ptrI(100)},
		{Platform: "douyin", PostID: "2", Likes: ptrI(10)},
		{Platform: "douyin", PostID: "3"}, // likes=null → 제외
	}
	got := applyPostFilters(items, SearchFilters{MinLikes: 50})
	require.Len(t, got, 1)
	require.Equal(t, "1", got[0].PostID)
}

func TestApplyPostFiltersDurationRange(t *testing.T) {
	items := []VideoItem{
		{Platform: "douyin", PostID: "1", Duration: 30},
		{Platform: "douyin", PostID: "2", Duration: 120},
		{Platform: "douyin", PostID: "3", Duration: 600},
	}
	got := applyPostFilters(items, SearchFilters{DurationMin: 60, DurationMax: 300})
	require.Len(t, got, 1)
	require.Equal(t, "2", got[0].PostID)
}
