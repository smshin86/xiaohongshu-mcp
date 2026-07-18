package search

import (
	"context"
	"sync"
	"time"
)

// perAdapterTimeout: 어댑터별 최대 대기. 한 사이드 지연이 전체를 막지 않도록.
var perAdapterTimeout = map[string]time.Duration{
	"xiaohongshu": 60 * time.Second,
	"douyin":      45 * time.Second,
	"tiktok":      45 * time.Second,
}

const aggregatorOverallTimeout = 75 * time.Second

// sideOut 은 단일 플랫폼 fan-out 결과. 패키지 레벨에 한 번만 정의(중복 정의 금지).
type sideOut struct {
	name string
	res  SideResult
}

// AggregatorService 는 VideoAdapter 들을 fan-out 해 머지한다.
type AggregatorService struct {
	adapters map[string]VideoAdapter
}

func NewAggregatorService(adapters map[string]VideoAdapter) *AggregatorService {
	return &AggregatorService{adapters: adapters}
}

// Availability 는 각 어댑터의 현재 가용성을 반환(capability endpoint 용).
func (s *AggregatorService) Availability(ctx context.Context) map[string]Availability {
	out := make(map[string]Availability, len(s.adapters))
	for name, ad := range s.adapters {
		out[name] = ad.Available(ctx)
	}
	return out
}

// Search 는 요청된 플랫폼에 대해 병렬 검색 후 머지/필터/정렬한다.
// 한 사이드 실패/미가용은 SideResult 로 흡수되고 전체는 실패하지 않는다(err 는 항상 nil).
func (s *AggregatorService) Search(ctx context.Context, req AggregatorRequest) (*AggregatedResult, error) {
	ctx, cancel := context.WithTimeout(ctx, aggregatorOverallTimeout)
	defer cancel()

	platforms := req.Platforms
	if len(platforms) == 0 {
		for n := range s.adapters {
			platforms = append(platforms, n)
		}
	}

	outs := make([]sideOut, len(platforms))
	var wg sync.WaitGroup
	for i, name := range platforms {
		wg.Add(1)
		go func(idx int, pname string) {
			defer wg.Done()
			outs[idx] = s.runSide(ctx, pname, req)
		}(i, name)
	}
	wg.Wait()

	result := &AggregatedResult{
		KeywordUsed: req.Keyword,
		Sort:        req.Sort,
		Sides:       map[string]SideResult{},
	}
	groups := map[string][]VideoItem{}
	for _, o := range outs {
		// per-side 파이프라인(spec 순서): filter → dedupe → per_platform_limit.
		// Sides[name].Items 와 groups[name] 은 동일(필터+절단된) 결과를 공유한다.
		// 크로스플랫폼 dedupe 금지 — 플랫폼 내부 dedupe 만.
		if o.res.Available.Available && len(o.res.Items) > 0 {
			items := applyPostFilters(o.res.Items, req.Filters)
			items = dedupeByKey(assignDedupeKey(items, req.Keyword))
			items = truncatePerPlatform(items, req.Filters.PerPlatformLimit)
			o.res.Items = items
			groups[o.name] = items
		}
		result.Sides[o.name] = o.res
	}

	// rank 머지는 마지막: within-platform adapter 순서 보존, 재정렬/merged-limit 없음.
	result.Items = rankMerge(groups)
	return result, nil
}

// truncatePerPlatform 은 per-platform 최대 개수로 절단(0 이하 = 제한 없음).
// 어댑터가 Limit 힌트를 받더라도 aggregator 가 권위 있는 상한으로 한 번 더 절단(방어적 이중 보장).
// adapter 입력 순서(=rank)를 보존해 앞쪽을 유지한다.
func truncatePerPlatform(items []VideoItem, limit int) []VideoItem {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

// runSide 는 단일 어댑터를 가용성 확인 후 검색(타임아웃 격리).
// 검색 에러 시 Available=false + sanitize 메시지.
func (s *AggregatorService) runSide(ctx context.Context, name string, req AggregatorRequest) sideOut {
	ad, ok := s.adapters[name]
	if !ok {
		return sideOut{name: name, res: SideResult{Available: Availability{Available: false, Reason: "지원하지 않는 플랫폼"}}}
	}
	timeout := perAdapterTimeout[name]
	if timeout == 0 {
		timeout = 45 * time.Second
	}
	sctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	avail := ad.Available(sctx)
	if !avail.Available {
		return sideOut{name: name, res: SideResult{Available: avail}}
	}

	page, err := ad.Search(sctx, SearchQuery{
		Keyword:    req.Keyword,
		Sort:       req.Sort,
		Filters:    req.Filters,
		Limit:      req.Filters.PerPlatformLimit,
		PageCursor: req.PageCursors[name],
	})
	if err != nil {
		msg := SideErrorMessage(name, err)
		return sideOut{name: name, res: SideResult{
			Available: Availability{Available: false, Reason: msg},
			Error:     msg,
		}}
	}
	return sideOut{name: name, res: SideResult{
		Items:      page.Items,
		Available:  Availability{Available: true},
		NextCursor: page.NextCursor,
		HasMore:    page.HasMore,
	}}
}
