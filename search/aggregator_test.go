package search

import (
	"context"
	"errors"
	"testing"
	"time"

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

// blockingAdapter: Available 이 ctx 가 끊길 때까지 block(실제 브라우저 probe hang 시뮬레이션).
// Availability 의 fan-out + per-adapter timeout 격리를 검증한다.
type blockingAdapter struct {
	name  string
	avail Availability
}

func (b *blockingAdapter) Name() string { return b.name }
func (b *blockingAdapter) Available(ctx context.Context) Availability {
	select {
	case <-ctx.Done():
		return Availability{Available: false, Reason: "probe timeout"}
	case <-time.After(30 * time.Second):
		return b.avail
	}
}
func (b *blockingAdapter) Search(ctx context.Context, q SearchQuery) (AdapterSearchPage, error) {
	return AdapterSearchPage{}, nil
}

func TestAggregatorAvailabilityIsolatesSlowProbe(t *testing.T) {
	// 회귀: Availability 가 순차+무타임아웃이면 XHS probe hang 이 전체를 블록한다.
	// fan-out + per-adapter timeout 적용 후엔 (1) 즉시 반환, (2) XHS unavailable, (3) douyin 계산 유지.
	prev := perAdapterTimeout["xiaohongshu"]
	perAdapterTimeout["xiaohongshu"] = 50 * time.Millisecond
	defer func() { perAdapterTimeout["xiaohongshu"] = prev }()

	svc := NewAggregatorService(map[string]VideoAdapter{
		"xiaohongshu": &blockingAdapter{name: "xiaohongshu", avail: Availability{Available: true}},
		"douyin":      &fakeAdapter{name: "douyin", avail: Availability{Available: true}},
	})

	start := time.Now()
	out := svc.Availability(context.Background())
	elapsed := time.Since(start)

	require.Less(t, elapsed, 2*time.Second, "Availability 가 XHS probe hang 에 블록되면 안 됨(순차/무타임아웃 결함 회귀)")
	require.False(t, out["xiaohongshu"].Available, "타임아웃 난 XHS probe 는 unavailable")
	require.True(t, out["douyin"].Available, "지연 사이드와 무관하게 douyin 가용성이 계산되어야 함")
}

// panickingAdapter: Available/Search 가 context cancel 등으로 panic(go-rod Must* 시뮬레이션).
// Availability/Search goroutine 의 panic 격리를 검증한다.
type panickingAdapter struct {
	name string
}

func (p *panickingAdapter) Name() string { return p.name }
func (p *panickingAdapter) Available(ctx context.Context) Availability {
	panic("simulated go-rod panic on context canceled")
}
func (p *panickingAdapter) Search(ctx context.Context, q SearchQuery) (AdapterSearchPage, error) {
	panic("simulated go-rod search panic on context canceled")
}

func TestAggregatorAvailabilityIsolatesAdapterPanic(t *testing.T) {
	// 회귀: XHS 의 Available panic(go-rod MustNavigate 의 ctx cancel panic)이
	// 프로세스를 죽이면 안 된다. (1) panic 난 플랫폼은 unavailable,
	// (2) 다른 플랫폼 응답은 보존.
	svc := NewAggregatorService(map[string]VideoAdapter{
		"xiaohongshu": &panickingAdapter{name: "xiaohongshu"},
		"douyin":      &fakeAdapter{name: "douyin", avail: Availability{Available: true}},
		"tiktok":      &fakeAdapter{name: "tiktok", avail: Availability{Available: true}},
	})

	out := svc.Availability(context.Background())

	require.False(t, out["xiaohongshu"].Available, "panic 난 XHS 는 unavailable")
	require.True(t, out["douyin"].Available, "XHS panic 과 무관하게 douyin 보존")
	require.True(t, out["tiktok"].Available, "XHS panic 과 무관하게 tiktok 보존")
}

func TestAggregatorSearchIsolatesAdapterPanic(t *testing.T) {
	// 회귀: Search 경로(runSide) 에서 adapter panic 이 프로세스를 죽이면 안 된다.
	// panic 난 사이드는 unavailable, 다른 사이드 결과는 보존.
	svc := NewAggregatorService(map[string]VideoAdapter{
		"xiaohongshu": &panickingAdapter{name: "xiaohongshu"},
		"douyin":      &fakeAdapter{name: "douyin", avail: Availability{Available: true}, items: []VideoItem{{Platform: "douyin", PostID: "d1"}}},
	})

	res, err := svc.Search(context.Background(), AggregatorRequest{
		Keyword:   "便携风扇",
		Platforms: []string{"xiaohongshu", "douyin"},
		Sort:      "relevance",
		Filters:   SearchFilters{PerPlatformLimit: 5, VideoOnly: true},
	})
	require.NoError(t, err)
	require.False(t, res.Sides["xiaohongshu"].Available.Available, "panic 난 XHS side 는 unavailable")
	require.True(t, res.Sides["douyin"].Available.Available, "douyin side 보존")
	require.Len(t, res.Sides["douyin"].Items, 1)
}
