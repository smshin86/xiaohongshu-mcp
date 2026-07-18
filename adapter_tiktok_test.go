package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/search"
)

func TestTikTokAvailableFromHealthz(t *testing.T) {
	require.True(t, NewTikTokAdapter(&fakeSidecar{hz: sidecarHealth{Tiktok: true}}).Available(context.Background()).Available)
	av := NewTikTokAdapter(&fakeSidecar{hz: sidecarHealth{Tiktok: false}}).Available(context.Background())
	require.False(t, av.Available)
	require.Contains(t, av.Reason, "ms_token")
}

func TestTikTokSearchMapsVideosSinglePage(t *testing.T) {
	views := int64(5000)
	fs := &fakeSidecar{
		hz: sidecarHealth{Tiktok: true},
		// 사이드카는 TikTok 에 대해 단일 페이지(has_more=false, next_cursor="") 반환.
		resp: sidecarSearchData{Videos: []sidecarVideo{{Platform: "tiktok", PostID: "t1", Views: &views, PostURL: "https://www.tiktok.com/@u/video/t1"}}, NextCursor: "", HasMore: false},
	}
	a := NewTikTokAdapter(fs)
	page, err := a.Search(context.Background(), search.SearchQuery{Keyword: "k"})
	require.NoError(t, err)
	require.Equal(t, "tiktok", fs.gotReq.Platform)
	require.Len(t, page.Items, 1)
	require.False(t, page.HasMore) // M1 단일 페이지
	require.Empty(t, page.NextCursor)
	require.NotEmpty(t, page.Items[0].PostURL) // 사이드카가 PostURL 채움(원본 보기 fallback)
}

func TestTikTokSearchMsTokenUnavailable(t *testing.T) {
	a := NewTikTokAdapter(&fakeSidecar{hz: sidecarHealth{Tiktok: true}, sErr: search.ErrUnavailable})
	_, err := a.Search(context.Background(), search.SearchQuery{Keyword: "k"})
	require.ErrorIs(t, err, search.ErrUnavailable)
}
