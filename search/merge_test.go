package search

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// postIDs 는 결과 순서 검증용 헬퍼(merge_test.go 전용).
func postIDs(items []VideoItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.PostID
	}
	return out
}

func TestRankMergePreservesAdapterOrder(t *testing.T) {
	// 재정렬 금지: adapter 입력 순서가 곧 rank(likes 값과 무관).
	// XHS: b(r0,likes50), a(r1,likes100) / Douyin: d(r0,likes5), c(r1,likes10).
	// r0 라운드로빈[xhs,douyin] → b,d / r1 → a,c → [b,d,a,c].
	xhs := []VideoItem{
		{Platform: "xiaohongshu", PostID: "b", Likes: ptrI(50)},
		{Platform: "xiaohongshu", PostID: "a", Likes: ptrI(100)},
	}
	douyin := []VideoItem{
		{Platform: "douyin", PostID: "d", Likes: ptrI(5)},
		{Platform: "douyin", PostID: "c", Likes: ptrI(10)},
	}
	got := rankMerge(map[string][]VideoItem{"xiaohongshu": xhs, "douyin": douyin})
	require.Equal(t, []string{"b", "d", "a", "c"}, postIDs(got))
}

func TestRankMergeAssignsRankScoreOnCopy(t *testing.T) {
	// r(0부터) → RankScore=1/(r+1). 복사본에 설정(원본 슬라이스 미변경).
	xhs := []VideoItem{{Platform: "xiaohongshu", PostID: "a"}, {Platform: "xiaohongshu", PostID: "b"}}
	orig := xhs[0].RankScore
	got := rankMerge(map[string][]VideoItem{"xiaohongshu": xhs})
	require.Equal(t, "a", got[0].PostID)
	require.Equal(t, 1.0, got[0].RankScore)         // r=0 → 1/1
	require.InDelta(t, 0.5, got[1].RankScore, 1e-9) // r=1 → 1/2
	require.Equal(t, orig, xhs[0].RankScore)        // 원본 미변경
}

func TestRankMergeRoundRobinByPlatformOrder(t *testing.T) {
	// 입력 순서 = rank, 동점(동일 r)은 platformOrder 라운드로빈.
	xhs := []VideoItem{{Platform: "xiaohongshu", PostID: "a"}, {Platform: "xiaohongshu", PostID: "b"}}
	douyin := []VideoItem{{Platform: "douyin", PostID: "c"}}
	got := rankMerge(map[string][]VideoItem{"xiaohongshu": xhs, "douyin": douyin})
	require.Equal(t, []string{"a", "c", "b"}, postIDs(got))
}

func TestRankMergeNoCrossPlatformDedupe(t *testing.T) {
	// 같은 PostURL 이라도 플랫폼이 다르면 둘 다 유지(크로스플랫폼 dedupe 금지).
	xhs := []VideoItem{{Platform: "xiaohongshu", PostID: "a", PostURL: "u1"}}
	douyin := []VideoItem{{Platform: "douyin", PostID: "b", PostURL: "u1"}}
	got := rankMerge(map[string][]VideoItem{"xiaohongshu": xhs, "douyin": douyin})
	require.Len(t, got, 2)
}
