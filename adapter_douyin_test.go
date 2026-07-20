package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/search"
)

// fakeSidecar: 어댑터 테스트용 sidecarSearcher(TikTok 어댑터 테스트가 공유).
type fakeSidecar struct {
	hz     sidecarHealth
	hzErr  error
	resp   sidecarSearchData
	sErr   error
	gotReq SidecarSearchRequest
}

func (f *fakeSidecar) Healthz(ctx context.Context) (sidecarHealth, error) { return f.hz, f.hzErr }
func (f *fakeSidecar) Search(ctx context.Context, req SidecarSearchRequest) (sidecarSearchData, error) {
	f.gotReq = req
	return f.resp, f.sErr
}

func TestDouyinAvailableFromHealthz(t *testing.T) {
	a := NewDouyinAdapter(&fakeSidecar{hz: sidecarHealth{Douyin: true}})
	require.True(t, a.Available(context.Background()).Available)

	a2 := NewDouyinAdapter(&fakeSidecar{hz: sidecarHealth{Douyin: false}})
	av := a2.Available(context.Background())
	require.False(t, av.Available)
	require.Contains(t, av.Reason, "쿠키/서명")
}

func TestDouyinSearchMapsVideosWithCursor(t *testing.T) {
	likes := int64(1200)
	fs := &fakeSidecar{
		hz:   sidecarHealth{Douyin: true},
		resp: sidecarSearchData{Videos: []sidecarVideo{{Platform: "douyin", PostID: "a", Title: "t", Likes: &likes}}, NextCursor: "cur1", HasMore: true},
	}
	a := NewDouyinAdapter(fs)
	page, err := a.Search(context.Background(), search.SearchQuery{Keyword: "k", Sort: "popularity"})
	require.NoError(t, err)
	// platform 이 body 에 전달(쿼리 아님).
	require.Equal(t, "douyin", fs.gotReq.Platform)
	require.Equal(t, "k", fs.gotReq.Q)
	require.Len(t, page.Items, 1)
	require.Equal(t, int64(1200), *page.Items[0].Likes)
	require.Equal(t, "cur1", page.NextCursor) // Douyin opaque cursor 매핑
	require.True(t, page.HasMore)
	require.False(t, page.Items[0].NeedsDetail) // Douyin 은 video_url 바로 제공
}

func TestDouyinSearchPropagatesSentinel(t *testing.T) {
	a := NewDouyinAdapter(&fakeSidecar{hz: sidecarHealth{Douyin: true}, sErr: search.ErrBadGateway})
	_, err := a.Search(context.Background(), search.SearchQuery{Keyword: "k"})
	require.ErrorIs(t, err, search.ErrBadGateway)
}
