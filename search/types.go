// Package search 는 크로스플랫폼 영상 검색의 순수 도메인 모델과 aggregator 를 제공한다.
// 외부 네트워크/브라우저 의존을 두지 않아 fake adapter 로 단위 테스트가 가능하다.
package search

import "context"

// VideoItem 은 프론트와 공유하는 통합 결과 항목(JSON 계약).
// 미지원 지표는 pointer(*int64) + omitempty 미사용 → 항상 null 키로 응답.
type VideoItem struct {
	Platform      string  `json:"platform"` // xiaohongshu|douyin|tiktok
	PostID        string  `json:"post_id"`
	PostURL       string  `json:"post_url"`
	VideoURL      string  `json:"video_url,omitempty"`
	ThumbnailURL  string  `json:"thumbnail_url"`
	Title         string  `json:"title"`
	Description   string  `json:"description,omitempty"`
	Author        string  `json:"author"`
	PublishedAt   string  `json:"published_at,omitempty"` // RFC3339
	Duration      int     `json:"duration,omitempty"`     // 초, 0=미상
	Likes         *int64  `json:"likes"`
	Comments      *int64  `json:"comments"`
	Favorites     *int64  `json:"favorites"`
	Views         *int64  `json:"views"` // XHS=null
	Shares        *int64  `json:"shares"`
	SourceKeyword string  `json:"source_keyword"`
	NeedsDetail   bool    `json:"needs_detail"` // true=XHS, false=Douyin/TikTok
	DetailToken   string  `json:"detail_token,omitempty"`
	DedupeKey     string  `json:"-"`
	RankScore     float64 `json:"-"`
}

type SearchFilters struct {
	IncludeKeywords  []string `json:"include_keywords"`
	ExcludeKeywords  []string `json:"exclude_keywords"`
	DateFrom         string   `json:"date_from"`
	DateTo           string   `json:"date_to"`
	DurationMin      int      `json:"duration_min"`
	DurationMax      int      `json:"duration_max"`
	MinLikes         int64    `json:"min_likes"`
	MinComments      int64    `json:"min_comments"`
	MinFavorites     int64    `json:"min_favorites"`
	MinViews         int64    `json:"min_views"`
	VideoOnly        bool     `json:"video_only"`
	PerPlatformLimit int      `json:"per_platform_limit"`
}

type SearchQuery struct {
	Keyword    string
	Sort       string
	Filters    SearchFilters
	Limit      int
	PageCursor string // opaque
}

type AdapterSearchPage struct {
	Items      []VideoItem
	NextCursor string
	HasMore    bool
}

type Availability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

type VideoAdapter interface {
	Name() string
	Available(ctx context.Context) Availability
	Search(ctx context.Context, q SearchQuery) (AdapterSearchPage, error)
}

type SideResult struct {
	Items      []VideoItem  `json:"items"`
	Error      string       `json:"error,omitempty"`
	Available  Availability `json:"available"`
	NextCursor string       `json:"next_cursor,omitempty"`
	HasMore    bool         `json:"has_more"`
}

type AggregatedResult struct {
	KeywordUsed string                `json:"keyword_used"`
	Sort        string                `json:"sort"`
	Items       []VideoItem           `json:"items"`
	Sides       map[string]SideResult `json:"sides"`
}

type AggregatorRequest struct {
	Keyword     string            `json:"keyword"`
	Platforms   []string          `json:"platforms"`
	Sort        string            `json:"sort"`
	PageCursors map[string]string `json:"page_cursors"`
	Filters     SearchFilters     `json:"filters"`
}
