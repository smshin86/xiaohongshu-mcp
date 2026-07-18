package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/search"
	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

// fakeXhsSearcher: 실제 SearchFeeds 시그니처 (*FeedsListResponse, error) 흉내.
type fakeXhsSearcher struct {
	resp     *FeedsListResponse
	err      error
	last     []xiaohongshu.FilterOption
	loggedIn bool
	loginErr error
}

func (f *fakeXhsSearcher) SearchFeeds(ctx context.Context, keyword string, filters ...xiaohongshu.FilterOption) (*FeedsListResponse, error) {
	f.last = filters
	return f.resp, f.err
}

func (f *fakeXhsSearcher) CheckLoginStatus(ctx context.Context) (*LoginStatusResponse, error) {
	return &LoginStatusResponse{IsLoggedIn: f.loggedIn}, f.loginErr
}

func TestXhsSearchMapsFeedToVideoItem(t *testing.T) {
	resp := &FeedsListResponse{Feeds: []xiaohongshu.Feed{{
		ID: "abc", XsecToken: "tok",
		NoteCard: xiaohongshu.NoteCard{
			Type:         "video",
			DisplayTitle: "便携风扇",
			User:         xiaohongshu.User{Nickname: "테스터"},
			InteractInfo: xiaohongshu.InteractInfo{LikedCount: "1.2万", CommentCount: "30"},
			Cover:        xiaohongshu.Cover{URLDefault: "https://x/c.jpg"},
			Video:        &xiaohongshu.Video{Capa: xiaohongshu.VideoCapability{Duration: 45}},
		},
	}}}
	a := NewXhsAdapter(&fakeXhsSearcher{resp: resp})
	page, err := a.Search(context.Background(), search.SearchQuery{Keyword: "便携风扇", Sort: "relevance"})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	it := page.Items[0]
	require.Equal(t, "xiaohongshu", it.Platform)
	require.Equal(t, "abc", it.PostID)
	require.Equal(t, "https://x/c.jpg", it.ThumbnailURL)
	require.Equal(t, int64(12000), *it.Likes) // "1.2万" → 12000
	require.Equal(t, int64(30), *it.Comments)
	require.Nil(t, it.Views)          // XHS 미제공(항상 null 키)
	require.Equal(t, 45, it.Duration) // nc.Video.Capa.Duration
	require.Equal(t, "https://www.xiaohongshu.com/explore/abc?xsec_source=pc_feed&xsec_token=tok", it.PostURL)
	require.True(t, it.NeedsDetail)
	require.Equal(t, "tok", it.DetailToken)
}

func TestXhsSearchVideoNilDurationZero(t *testing.T) {
	// Video 가 nil 이면 Duration=0(생략). PostURL 은 token 없이도 생성.
	resp := &FeedsListResponse{Feeds: []xiaohongshu.Feed{{ID: "x1", NoteCard: xiaohongshu.NoteCard{Type: "video"}}}}
	a := NewXhsAdapter(&fakeXhsSearcher{resp: resp})
	page, _ := a.Search(context.Background(), search.SearchQuery{Keyword: "k"})
	require.Equal(t, "https://www.xiaohongshu.com/explore/x1", page.Items[0].PostURL)
	require.Zero(t, page.Items[0].Duration)
}

func TestXhsSearchSetsVideoNoteTypeAndSort(t *testing.T) {
	fs := &fakeXhsSearcher{resp: &FeedsListResponse{}}
	a := NewXhsAdapter(fs)
	_, _ = a.Search(context.Background(), search.SearchQuery{Keyword: "k", Sort: "latest"})
	require.Len(t, fs.last, 1)
	require.Equal(t, "视频", fs.last[0].NoteType)
	require.Equal(t, "最新", fs.last[0].SortBy) // latest → 最新
}

func TestXhsNoUnconditionalPublishBucket(t *testing.T) {
	// date_from 없으면 PublishTime 강제 없음(빈값=不限).
	fs := &fakeXhsSearcher{resp: &FeedsListResponse{}}
	a := NewXhsAdapter(fs)
	_, _ = a.Search(context.Background(), search.SearchQuery{Keyword: "k"})
	require.Empty(t, fs.last[0].PublishTime, "date_from 없으면 PublishTime 강제 금지")
}

func TestXhsSafePublishBucketFromRecentDate(t *testing.T) {
	// date_from 이 오늘 → 一天内 (안전 매핑).
	require.Equal(t, "一天内", xhsPublishBucket(time.Now().Format(time.RFC3339), ""))
}

func TestXhsAvailableUsesRealLoginStatus(t *testing.T) {
	a := NewXhsAdapter(&fakeXhsSearcher{resp: &FeedsListResponse{}, loggedIn: true})
	require.True(t, a.Available(context.Background()).Available)
}

func TestXhsUnavailableWhenLoggedOutOrProbeFails(t *testing.T) {
	loggedOut := NewXhsAdapter(&fakeXhsSearcher{resp: &FeedsListResponse{}, loggedIn: false})
	require.False(t, loggedOut.Available(context.Background()).Available)
	probeFail := NewXhsAdapter(&fakeXhsSearcher{resp: &FeedsListResponse{}, loginErr: errors.New("secret detail")})
	av := probeFail.Available(context.Background())
	require.False(t, av.Available)
	require.NotContains(t, av.Reason, "secret detail")
}
