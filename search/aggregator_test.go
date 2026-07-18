package search

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeAdapter: 테스트용 VideoAdapter.
type fakeAdapter struct {
	name    string
	avail   Availability
	items   []VideoItem
	err     error
	nextCur string
	hasMore bool
}

func (f *fakeAdapter) Name() string                               { return f.name }
func (f *fakeAdapter) Available(ctx context.Context) Availability { return f.avail }
func (f *fakeAdapter) Search(ctx context.Context, q SearchQuery) (AdapterSearchPage, error) {
	if f.err != nil {
		return AdapterSearchPage{}, f.err
	}
	return AdapterSearchPage{Items: f.items, NextCursor: f.nextCur, HasMore: f.hasMore}, nil
}

func TestAggregatorSuccessMerges(t *testing.T) {
	svc := NewAggregatorService(map[string]VideoAdapter{
		"xiaohongshu": &fakeAdapter{name: "xiaohongshu", avail: Availability{Available: true}, items: []VideoItem{{Platform: "xiaohongshu", PostID: "a"}}},
		"douyin":      &fakeAdapter{name: "douyin", avail: Availability{Available: true}, items: []VideoItem{{Platform: "douyin", PostID: "b"}}},
	})
	res, err := svc.Search(context.Background(), AggregatorRequest{
		Keyword:   "便携风扇",
		Platforms: []string{"xiaohongshu", "douyin"},
		Sort:      "relevance",
	})
	require.NoError(t, err)
	require.Len(t, res.Items, 2) // rank 머지: a(r0), b(r0) → [a, b]
	require.True(t, res.Sides["xiaohongshu"].Available.Available)
}

func TestAggregatorIsolatesSideFailureSanitized(t *testing.T) {
	// Douyin 은 ErrBadGateway → Available=false + 고정 메시지; XHS 결과는 살아남음.
	svc := NewAggregatorService(map[string]VideoAdapter{
		"xiaohongshu": &fakeAdapter{name: "xiaohongshu", avail: Availability{Available: true}, items: []VideoItem{{Platform: "xiaohongshu", PostID: "a"}}},
		"douyin":      &fakeAdapter{name: "douyin", avail: Availability{Available: true}, err: ErrBadGateway},
	})
	res, _ := svc.Search(context.Background(), AggregatorRequest{Keyword: "k", Platforms: []string{"xiaohongshu", "douyin"}, Sort: "relevance"})
	// 검색 에러 → Available=false + sanitize 메시지.
	require.False(t, res.Sides["douyin"].Available.Available)
	require.Equal(t, "일시적으로 차단 — 잠시 후 재시도", res.Sides["douyin"].Error)
	require.NotContains(t, res.Sides["douyin"].Error, "bad gateway") // 원문 노출 금지
	require.Len(t, res.Items, 1)                                     // XHS 만 결과에 포함
}

func TestAggregatorUnavailableWhenAdapterDown(t *testing.T) {
	// Available() 가 false 면 Search 호출 없이 해당 사이드 미가용.
	svc := NewAggregatorService(map[string]VideoAdapter{
		"tiktok": &fakeAdapter{name: "tiktok", avail: Availability{Available: false, Reason: "ms_token 갱신 필요"}},
	})
	res, _ := svc.Search(context.Background(), AggregatorRequest{Keyword: "k", Platforms: []string{"tiktok"}, Sort: "relevance"})
	require.False(t, res.Sides["tiktok"].Available.Available)
	require.Contains(t, res.Sides["tiktok"].Available.Reason, "ms_token")
}

func TestAggregatorIntraPlatformDedupe(t *testing.T) {
	// 같은 플랫폼 내 동일 post_url 은 첫 것만(플랫폼 내부 dedupe).
	svc := NewAggregatorService(map[string]VideoAdapter{
		"douyin": &fakeAdapter{name: "douyin", avail: Availability{Available: true}, items: []VideoItem{
			{Platform: "douyin", PostID: "a", PostURL: "u1"},
			{Platform: "douyin", PostID: "b", PostURL: "u1"},
		}},
	})
	res, _ := svc.Search(context.Background(), AggregatorRequest{Keyword: "k", Platforms: []string{"douyin"}, Sort: "relevance"})
	require.Len(t, res.Items, 1)
	require.Equal(t, "a", res.Items[0].PostID)
}

func TestAggregatorFiltersBeforeDedupe(t *testing.T) {
	// 첫 중복이 필터 탈락, 둘째가 통과: dedupe→filter 이면 잘못 0개가 되므로 순서를 고정한다.
	svc := NewAggregatorService(map[string]VideoAdapter{
		"douyin": &fakeAdapter{name: "douyin", avail: Availability{Available: true}, items: []VideoItem{
			{Platform: "douyin", PostID: "a", PostURL: "u1", Likes: int64Ptr(1)},
			{Platform: "douyin", PostID: "b", PostURL: "u1", Likes: int64Ptr(100)},
		}},
	})
	res, _ := svc.Search(context.Background(), AggregatorRequest{
		Keyword: "k", Platforms: []string{"douyin"}, Sort: "relevance",
		Filters: SearchFilters{MinLikes: 50},
	})
	require.Equal(t, []string{"b"}, postIDs(res.Items))
	require.Equal(t, []string{"b"}, postIDs(res.Sides["douyin"].Items))
}

func TestAggregatorAppliesPerPlatformLimit(t *testing.T) {
	// per-platform limit 은 aggregator 에서 filter→dedupe 이후에 절단(방어적 이중 보장).
	// adapter 입력 순서(=rank)를 보존해 앞쪽 N 개만 유지.
	svc := NewAggregatorService(map[string]VideoAdapter{
		"douyin": &fakeAdapter{name: "douyin", avail: Availability{Available: true}, items: []VideoItem{
			{Platform: "douyin", PostID: "a"},
			{Platform: "douyin", PostID: "b"},
			{Platform: "douyin", PostID: "c"},
		}},
	})
	res, _ := svc.Search(context.Background(), AggregatorRequest{
		Keyword:   "k",
		Platforms: []string{"douyin"},
		Sort:      "relevance",
		Filters:   SearchFilters{PerPlatformLimit: 2},
	})
	require.Len(t, res.Items, 2)
	require.Equal(t, []string{"a", "b"}, postIDs(res.Items)) // 앞쪽 2개 보존(adapter 순서=rank)
	require.Len(t, res.Sides["douyin"].Items, 2)             // Sides.Items 도 동일 절단 결과
}

func TestSideErrorMessageMapping(t *testing.T) {
	// ErrUnavailable 은 플랫폼별 고정 메시지(spec Error Handling 표와 일치) — test/impl 하나로 통일.
	require.Equal(t, "샤오홍슈: 설정에서 QR 로그인 필요", SideErrorMessage("xiaohongshu", ErrUnavailable))
	require.Equal(t, "Douyin: 쿠키/서명 미설정", SideErrorMessage("douyin", ErrUnavailable))
	require.Equal(t, "TikTok: ms_token 갱신 필요(tiktok.com 쿠키)", SideErrorMessage("tiktok", ErrUnavailable))
	require.Equal(t, "플랫폼 준비 중", SideErrorMessage("unknown", ErrUnavailable))
	// 공통 sentinel.
	require.Equal(t, "일시적으로 차단 — 잠시 후 재시도", SideErrorMessage("douyin", ErrBadGateway))
	require.Equal(t, "서비스에 연결할 수 없습니다", SideErrorMessage("douyin", ErrUnreachable))
	// 타임아웃/취소.
	require.Equal(t, "응답 시간 초과", SideErrorMessage("douyin", context.DeadlineExceeded))
	require.Equal(t, "요청이 취소되었습니다", SideErrorMessage("douyin", context.Canceled))
	// 알 수 없는 에러 → 원문 노출 금지, 고정 안전 문구.
	msg := SideErrorMessage("douyin", errors.New("random raw with cookie=secret"))
	require.Equal(t, "검색 중 오류가 발생했습니다", msg)
	require.NotContains(t, msg, "secret")
}
