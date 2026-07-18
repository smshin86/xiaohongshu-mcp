package main

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/xpzouying/xiaohongshu-mcp/search"
	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

// XhsService: 검색과 실제 로그인 readiness 를 함께 제공하는 최소 인터페이스.
type XhsService interface {
	SearchFeeds(ctx context.Context, keyword string, filters ...xiaohongshu.FilterOption) (*FeedsListResponse, error)
	CheckLoginStatus(ctx context.Context) (*LoginStatusResponse, error)
}

// XhsAdapter: XHS(go-rod) 영상 검색 어댑터. 영상 URL 은 detail 호출에서 획득(NeedsDetail=true).
type XhsAdapter struct {
	service XhsService
}

func NewXhsAdapter(s XhsService) *XhsAdapter {
	return &XhsAdapter{service: s}
}

func (a *XhsAdapter) Name() string { return "xiaohongshu" }

// Available: 기존 QR/cookies 세션을 실제 페이지 probe 로 검증한다.
func (a *XhsAdapter) Available(ctx context.Context) search.Availability {
	status, err := a.service.CheckLoginStatus(ctx)
	if err != nil || status == nil || !status.IsLoggedIn {
		return search.Availability{Available: false, Reason: "샤오홍슈 로그인이 필요합니다. 설정에서 QR 로그인을 완료해 주세요."}
	}
	return search.Availability{Available: true}
}

func (a *XhsAdapter) Search(ctx context.Context, q search.SearchQuery) (search.AdapterSearchPage, error) {
	opt := xiaohongshu.FilterOption{NoteType: "视频"} // 영상만
	switch q.Sort {
	case "popularity":
		opt.SortBy = "最多点赞"
	case "latest":
		opt.SortBy = "最新"
	}
	opt.PublishTime = xhsPublishBucket(q.Filters.DateFrom, q.Filters.DateTo)

	resp, err := a.service.SearchFeeds(ctx, q.Keyword, opt)
	if err != nil {
		return search.AdapterSearchPage{}, err
	}
	items := make([]search.VideoItem, 0, len(resp.Feeds))
	for _, feed := range resp.Feeds {
		items = append(items, xhsFeedToVideoItem(feed))
	}
	// XHS 검색은 M1 단일 페이지(SearchFeeds 가 cursor 미지원).
	return search.AdapterSearchPage{Items: items, NextCursor: "", HasMore: false}, nil
}

// xhsFeedToVideoItem: xiaohongshu.Feed → search.VideoItem.
func xhsFeedToVideoItem(feed xiaohongshu.Feed) search.VideoItem {
	nc := feed.NoteCard
	cover := nc.Cover.URLDefault
	if cover == "" {
		cover = nc.Cover.URLPre
	}
	author := nc.User.Nickname
	if author == "" {
		author = nc.User.NickName
	}
	vi := search.VideoItem{
		Platform:     "xiaohongshu",
		PostID:       feed.ID,
		PostURL:      xhsPostURL(feed.ID, feed.XsecToken),
		ThumbnailURL: cover,
		Title:        nc.DisplayTitle,
		Author:       author,
		Likes:        parseCount(nc.InteractInfo.LikedCount),
		Comments:     parseCount(nc.InteractInfo.CommentCount),
		Favorites:    parseCount(nc.InteractInfo.CollectedCount),
		Shares:       parseCount(nc.InteractInfo.SharedCount),
		Views:        nil, // XHS 미지원(항상 null 키)
		NeedsDetail:  true,
		DetailToken:  feed.XsecToken,
	}
	if nc.Video != nil { // Video 는 *Video(nil 가능)
		vi.Duration = nc.Video.Capa.Duration // 초 단위
	}
	return vi
}

// xhsPostURL: XHS explore URL(note_id + xsec_token). 원본 보기 fallback 용.
func xhsPostURL(noteID, xsecToken string) string {
	if noteID == "" {
		return ""
	}
	u := &url.URL{Scheme: "https", Host: "www.xiaohongshu.com", Path: "/explore/" + noteID}
	if xsecToken != "" {
		q := u.Query()
		q.Set("xsec_token", xsecToken)
		q.Set("xsec_source", "pc_feed")
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// parseCount: "1.2万"/"5千"/"100" 같은 XHS 문자열 카운트 → *int64. 빈값/해석불가 → nil.
func parseCount(s string) *int64 {
	n, ok := parseChineseCount(s)
	if !ok {
		return nil
	}
	return &n
}

// parseChineseCount: 한자 단위(万/亿/千/百) + 소수 파싱.
func parseChineseCount(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "亿"):
		mult = 100_000_000
		s = strings.TrimSuffix(s, "亿")
	case strings.HasSuffix(s, "万"):
		mult = 10_000
		s = strings.TrimSuffix(s, "万")
	case strings.HasSuffix(s, "千"):
		mult = 1_000
		s = strings.TrimSuffix(s, "千")
	case strings.HasSuffix(s, "百"):
		mult = 100
		s = strings.TrimSuffix(s, "百")
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return int64(f * float64(mult)), true
}

// xhsPublishBucket: date_from 기반 안전 XHS publish_time 버킷. 무조건 一周内 강제 금지.
func xhsPublishBucket(dateFrom, dateTo string) string {
	if dateFrom == "" {
		return "" // 不限
	}
	t, ok := tryParseAnyDate(dateFrom)
	if !ok {
		return ""
	}
	ageDays := time.Since(t).Hours() / 24
	switch {
	case ageDays <= 1:
		return "一天内"
	case ageDays <= 7:
		return "一周内"
	case ageDays <= 180:
		return "半年内"
	default:
		return "" // 不限(반년 초과)
	}
}

// tryParseAnyDate: date-only 또는 RFC3339 파싱.
func tryParseAnyDate(s string) (time.Time, bool) {
	for _, l := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
