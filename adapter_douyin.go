package main

import (
	"context"

	"github.com/xpzouying/xiaohongshu-mcp/search"
)

// sidecarSearcher: 어댑터가 의존하는 사이드카 인터페이스(SidecarClient 구현, 테스트는 fake).
type sidecarSearcher interface {
	Healthz(ctx context.Context) (sidecarHealth, error)
	Search(ctx context.Context, req SidecarSearchRequest) (sidecarSearchData, error)
}

// buildSidecarRequest: SearchQuery → 사이드카 /search body(Douyin/TikTok 공용).
// platform 은 body 필드로 지정(쿼리 아님). count 기본 15.
func buildSidecarRequest(platform string, q search.SearchQuery) SidecarSearchRequest {
	count := q.Limit
	if count <= 0 {
		count = 15
	}
	return SidecarSearchRequest{
		Platform: platform,
		Q:        q.Keyword,
		Sort:     q.Sort,
		Cursor:   q.PageCursor,
		Count:    count,
		Filters:  q.Filters,
	}
}

// toVideoItem: 사이드카 sidecarVideo → search.VideoItem(스키마 동일).
func toVideoItem(v sidecarVideo) search.VideoItem {
	return search.VideoItem{
		Platform:     v.Platform,
		PostID:       v.PostID,
		PostURL:      v.PostURL,
		VideoURL:     v.VideoURL,
		ThumbnailURL: v.ThumbnailURL,
		Title:        v.Title,
		Description:  v.Description,
		Author:       v.Author,
		PublishedAt:  v.PublishedAt,
		Duration:     v.Duration,
		Likes:        v.Likes,
		Comments:     v.Comments,
		Favorites:    v.Favorites,
		Views:        v.Views,
		Shares:       v.Shares,
		NeedsDetail:  v.NeedsDetail,
	}
}

// DouyinAdapter: Douyin 영상 검색 어댑터(사이드카 래핑, opaque cursor).
type DouyinAdapter struct {
	cli sidecarSearcher
}

func NewDouyinAdapter(cli sidecarSearcher) *DouyinAdapter { return &DouyinAdapter{cli: cli} }

func (a *DouyinAdapter) Name() string { return "douyin" }

// Available: 사이드카 /healthz 의 douyin bool. 비밀 평문 없음.
func (a *DouyinAdapter) Available(ctx context.Context) search.Availability {
	hz, err := a.cli.Healthz(ctx)
	if err != nil {
		return search.Availability{Available: false, Reason: "사이드카 연결 불가"}
	}
	if !hz.Douyin {
		return search.Availability{Available: false, Reason: "Douyin: 쿠키/서명 미설정"}
	}
	return search.Availability{Available: true}
}

func (a *DouyinAdapter) Search(ctx context.Context, q search.SearchQuery) (search.AdapterSearchPage, error) {
	resp, err := a.cli.Search(ctx, buildSidecarRequest("douyin", q))
	if err != nil {
		return search.AdapterSearchPage{}, err
	}
	items := make([]search.VideoItem, 0, len(resp.Videos))
	for _, v := range resp.Videos {
		items = append(items, toVideoItem(v))
	}
	// Douyin opaque cursor: 사이드카 next_cursor/has_more 를 그대로 매핑.
	return search.AdapterSearchPage{Items: items, NextCursor: resp.NextCursor, HasMore: resp.HasMore}, nil
}
