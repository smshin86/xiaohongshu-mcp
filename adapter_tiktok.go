package main

import (
	"context"

	"github.com/xpzouying/xiaohongshu-mcp/search"
)

// TikTokAdapter: TikTok 영상 검색 어댑터(사이드카 래핑, M1 단일 페이지).
type TikTokAdapter struct {
	cli sidecarSearcher
}

func NewTikTokAdapter(cli sidecarSearcher) *TikTokAdapter { return &TikTokAdapter{cli: cli} }

func (a *TikTokAdapter) Name() string { return "tiktok" }

// Available: 사이드카 /healthz 의 tiktok bool(TT_MSTOKEN 설정 여부).
func (a *TikTokAdapter) Available(ctx context.Context) search.Availability {
	hz, err := a.cli.Healthz(ctx)
	if err != nil {
		return search.Availability{Available: false, Reason: "사이드카 연결 불가"}
	}
	if !hz.Tiktok {
		return search.Availability{Available: false, Reason: "TikTok: ms_token 갱신 필요"}
	}
	return search.Availability{Available: true}
}

func (a *TikTokAdapter) Search(ctx context.Context, q search.SearchQuery) (search.AdapterSearchPage, error) {
	resp, err := a.cli.Search(ctx, buildSidecarRequest("tiktok", q))
	if err != nil {
		return search.AdapterSearchPage{}, err
	}
	items := make([]search.VideoItem, 0, len(resp.Videos))
	for _, v := range resp.Videos {
		items = append(items, toVideoItem(v))
	}
	// M1: TikTok 단일 페이지 — 사이드카가 has_more=false/next_cursor="" 반환(강제 아님, 계약).
	return search.AdapterSearchPage{Items: items, NextCursor: resp.NextCursor, HasMore: resp.HasMore}, nil
}
