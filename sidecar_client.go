package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/xpzouying/xiaohongshu-mcp/search"
)

// SidecarSearchRequest: 사이드카 POST /search body(spec 계약). platform 은 body 에 포함.
type SidecarSearchRequest struct {
	Platform string               `json:"platform"` // douyin|tiktok
	Q        string               `json:"q"`
	Count    int                  `json:"count"`
	Sort     string               `json:"sort"`
	Cursor   string               `json:"cursor"`
	Filters  search.SearchFilters `json:"filters"`
}

// sidecarSearchEnvelope: {success,data:{...}} 래핑 응답.
type sidecarSearchEnvelope struct {
	Success bool              `json:"success"`
	Data    sidecarSearchData `json:"data"`
}

// sidecarSearchData: 래핑 안쪽 데이터.
type sidecarSearchData struct {
	Videos     []sidecarVideo `json:"videos"`
	NextCursor string         `json:"next_cursor"`
	HasMore    bool           `json:"has_more"`
}

// sidecarVideo: 사이드카가 통일 매핑한 VideoItem JSON. search.VideoItem 과 동일 스키마.
type sidecarVideo struct {
	Platform     string `json:"platform"`
	PostID       string `json:"post_id"`
	PostURL      string `json:"post_url"`
	VideoURL     string `json:"video_url,omitempty"`
	ThumbnailURL string `json:"thumbnail_url"`
	Title        string `json:"title"`
	Description  string `json:"description,omitempty"`
	Author       string `json:"author"`
	PublishedAt  string `json:"published_at,omitempty"`
	Duration     int    `json:"duration,omitempty"`
	Likes        *int64 `json:"likes"`
	Comments     *int64 `json:"comments"`
	Favorites    *int64 `json:"favorites"`
	Views        *int64 `json:"views"`
	Shares       *int64 `json:"shares"`
	NeedsDetail  bool   `json:"needs_detail"`
}

// sidecarHealth: /healthz 응답. 비밀(cookie/ms_token) 평문 없이 bool 만.
type sidecarHealth struct {
	Status string `json:"status"`
	Douyin bool   `json:"douyin"`
	Tiktok bool   `json:"tiktok"`
	Llm    bool   `json:"llm"`
}

// SidecarClient: Python 사이드카 HTTP 클라이언트.
type SidecarClient struct {
	baseURL string
	http    *http.Client

	mu      sync.Mutex
	hzCache sidecarHealth
	hzAt    time.Time
	hzTTL   time.Duration
}

// NewSidecarClient: baseURL 과 healthz TTL(0=기본 10s) 로 생성.
func NewSidecarClient(baseURL string, healthzTTL time.Duration) *SidecarClient {
	if healthzTTL == 0 {
		healthzTTL = 10 * time.Second
	}
	return &SidecarClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 25 * time.Second},
		hzTTL:   healthzTTL,
	}
}

// Search: 사이드카 POST /search 호출. platform 은 body. 상태코드/네트워크 에러를 search sentinel 로 매핑.
// 응답 {success,data:{videos,next_cursor,has_more}} 의 data 를 반환.
func (c *SidecarClient) Search(ctx context.Context, req SidecarSearchRequest) (sidecarSearchData, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return sidecarSearchData{}, fmt.Errorf("%w: %v", search.ErrBadGateway, err)
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/search", bytes.NewReader(body))
	if err != nil {
		return sidecarSearchData{}, search.ErrUnreachable
	}
	hr.Header.Set("Content-Type", "application/json")
	// platform 은 body 에 포함 — 쿼리 파라미터 사용 금지(spec 계약).

	resp, err := c.http.Do(hr)
	if err != nil {
		// ctx deadline/canceled 는 원문 그대로 상위로(aggregator 가 SideErrorMessage 처리).
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return sidecarSearchData{}, err
		}
		return sidecarSearchData{}, search.ErrUnreachable
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusServiceUnavailable:
		return sidecarSearchData{}, search.ErrUnavailable
	case resp.StatusCode >= 400:
		return sidecarSearchData{}, search.ErrBadGateway
	}

	var env sidecarSearchEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return sidecarSearchData{}, search.ErrBadGateway
	}
	if !env.Success {
		return sidecarSearchData{}, search.ErrBadGateway
	}
	return env.Data, nil
}

// Healthz: 사이드카 /healthz 조회(TTL 캐시). 가용성 bool 만.
func (c *SidecarClient) Healthz(ctx context.Context) (sidecarHealth, error) {
	c.mu.Lock()
	if !c.hzAt.IsZero() && time.Since(c.hzAt) < c.hzTTL {
		hz := c.hzCache
		c.mu.Unlock()
		return hz, nil
	}
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return sidecarHealth{}, search.ErrUnreachable
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return sidecarHealth{}, search.ErrUnreachable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return sidecarHealth{}, search.ErrUnreachable
	}
	var hz sidecarHealth
	if err := json.NewDecoder(resp.Body).Decode(&hz); err != nil {
		return sidecarHealth{}, search.ErrBadGateway
	}

	c.mu.Lock()
	c.hzCache = hz
	c.hzAt = time.Now()
	c.mu.Unlock()
	return hz, nil
}
