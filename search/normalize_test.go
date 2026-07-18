package search

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDedupeKeyPrefersPostURL(t *testing.T) {
	require.Equal(t, "douyin:https://x.com/a", dedupeKey(VideoItem{Platform: "douyin", PostID: "p1", PostURL: "https://x.com/a"}))
}

func TestDedupeKeyFallsBackToPlatformPostID(t *testing.T) {
	require.Equal(t, "xiaohongshu:p1", dedupeKey(VideoItem{Platform: "xiaohongshu", PostID: "p1"}))
}

func TestDedupeByKeyKeepsFirstOccurrence(t *testing.T) {
	items := []VideoItem{
		{Platform: "douyin", PostID: "a", PostURL: "u1"},
		{Platform: "douyin", PostID: "b", PostURL: "u1"},
		{Platform: "douyin", PostID: "c", PostURL: "u2"},
	}
	// 실 파이프라인: assignDedupeKey 로 DedupeKey 를 스테이징한 뒤 dedupeByKey 가 읽는다.
	got := dedupeByKey(assignDedupeKey(items, ""))
	require.Len(t, got, 2)
	require.Equal(t, "a", got[0].PostID)
	require.Equal(t, "c", got[1].PostID)
}

func TestAssignDedupeKeySetsKeywordAndKey(t *testing.T) {
	got := assignDedupeKey([]VideoItem{{Platform: "douyin", PostID: "p1"}}, "便携风扇")
	require.Equal(t, "便携风扇", got[0].SourceKeyword)
	require.Equal(t, "douyin:p1", got[0].DedupeKey)
}

func TestInt64Ptr(t *testing.T) {
	p := int64Ptr(7)
	require.NotNil(t, p)
	require.Equal(t, int64(7), *p)
}
