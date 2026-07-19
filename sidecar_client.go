package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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

	// downloadHTTP: 다운로드 전용 client. stream 수명이 300s 까지 가므로
	// search 용 25s timeout client 와 분리(Timeout 없이 Transport 만).
	downloadHTTP *http.Client

	// keywordsHTTP: 키워드 extract/translate 전용. LLM 왕복으로 60s 까지 가므로
	// search 25s·download Timeout 없음 과 분리.
	keywordsHTTP *http.Client

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
		// 다운로드 전용: Timeout 없이 stream 전송(handler ctx 가 수명 제어).
		downloadHTTP: &http.Client{},
		// 키워드 전용: LLM 왕복 60s. handler ctx 도 60s 로 일치.
		keywordsHTTP: &http.Client{Timeout: 60 * time.Second},
		hzTTL:        healthzTTL,
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

// Download: 사이드카 GET /download 호출. raw URL 은 url.Values 로 정확히 한번만 encode.
// 응답 body 를 읽지 않고 OPEN *http.Response 를 반환. caller 가 반드시 resp.Body.Close().
// handler 가 부여한 ctx 를 그대로 전달해 client cancellation 이 upstream stream 을 종료시킨다.
func (c *SidecarClient) Download(ctx context.Context, platform, rawURL string) (*http.Response, error) {
	// baseURL 은 loopback 신뢰 경계. rawURL 은 단일 encode 로 sidecar 가 원문 복원.
	q := url.Values{}
	q.Set("platform", platform)
	q.Set("url", rawURL)
	reqURL := c.baseURL + "/download?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, search.ErrUnreachable
	}

	resp, err := c.downloadHTTP.Do(req)
	if err != nil {
		// ctx deadline/canceled 는 상위로 그대로 전달(aggregator 가 메시지 처리).
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, search.ErrUnreachable
	}

	// 상태코드만 보고 매핑. body 는 읽지 않는다(원문/signed URL 누출 방지).
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusForbidden:
			return nil, search.ErrForbidden
		case http.StatusGatewayTimeout:
			return nil, context.DeadlineExceeded
		default:
			return nil, search.ErrBadGateway
		}
	}
	return resp, nil
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

// SidecarKeywordCandidate: 사이드카 /keywords/extract 가 뽑은 단일 후보.
type SidecarKeywordCandidate struct {
	Keyword    string  `json:"keyword"`
	SourceURL  string  `json:"source_url"`
	Basis      string  `json:"basis"`
	Confidence float64 `json:"confidence"`
}

// SidecarKeywordResult: extract 응답 data(candidates + note).
type SidecarKeywordResult struct {
	Candidates []SidecarKeywordCandidate `json:"candidates"`
	Note       string                    `json:"note"`
}

// SidecarTranslateCandidate: 사이드카 /keywords/translate 가 번역한 단일 후보.
type SidecarTranslateCandidate struct {
	ZH string `json:"zh"`
}

// SidecarTranslateResult: translate 응답 data.
type SidecarTranslateResult struct {
	Candidates []SidecarTranslateCandidate `json:"candidates"`
}

// sidecarKeywordEnvelope: {success,data:{candidates,note}} 래핑.
type sidecarKeywordEnvelope struct {
	Success bool                 `json:"success"`
	Data    SidecarKeywordResult `json:"data"`
}

// sidecarTranslateEnvelope: {success,data:{candidates}} 래핑.
type sidecarTranslateEnvelope struct {
	Success bool                   `json:"success"`
	Data    SidecarTranslateResult `json:"data"`
}

// ExtractKeywords: POST /keywords/extract 호출. caller ctx 로 cancel propagation.
// 200+success:true→data; 200+success:false→ErrKeywordsFailed; ≥400→ErrBadGateway;
// 네트워크→ErrUnreachable; ctx deadline/canceled→그대로 상위.
func (c *SidecarClient) ExtractKeywords(ctx context.Context, urls []string) (SidecarKeywordResult, error) {
	body, err := json.Marshal(struct {
		URLs []string `json:"urls"`
	}{URLs: urls})
	if err != nil {
		return SidecarKeywordResult{}, fmt.Errorf("%w: %v", search.ErrBadGateway, err)
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/keywords/extract", bytes.NewReader(body))
	if err != nil {
		return SidecarKeywordResult{}, search.ErrUnreachable
	}
	hr.Header.Set("Content-Type", "application/json")

	resp, err := c.keywordsHTTP.Do(hr)
	if err != nil {
		// ctx deadline/canceled 는 원문 그대로(handler 가 504/빈응답 매핑).
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return SidecarKeywordResult{}, err
		}
		return SidecarKeywordResult{}, search.ErrUnreachable
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return SidecarKeywordResult{}, search.ErrBadGateway
	}

	var env sidecarKeywordEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return SidecarKeywordResult{}, search.ErrBadGateway
	}
	if !env.Success {
		return SidecarKeywordResult{}, search.ErrKeywordsFailed
	}
	return env.Data, nil
}

// TranslateKeywords: POST /keywords/translate 호출. caller ctx 로 cancel propagation.
// 매핑은 ExtractKeywords 와 동일.
func (c *SidecarClient) TranslateKeywords(ctx context.Context, text, sourceLang string) (SidecarTranslateResult, error) {
	body, err := json.Marshal(struct {
		Text       string `json:"text"`
		SourceLang string `json:"source_lang"`
	}{Text: text, SourceLang: sourceLang})
	if err != nil {
		return SidecarTranslateResult{}, fmt.Errorf("%w: %v", search.ErrBadGateway, err)
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/keywords/translate", bytes.NewReader(body))
	if err != nil {
		return SidecarTranslateResult{}, search.ErrUnreachable
	}
	hr.Header.Set("Content-Type", "application/json")

	resp, err := c.keywordsHTTP.Do(hr)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return SidecarTranslateResult{}, err
		}
		return SidecarTranslateResult{}, search.ErrUnreachable
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return SidecarTranslateResult{}, search.ErrBadGateway
	}

	var env sidecarTranslateEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return SidecarTranslateResult{}, search.ErrBadGateway
	}
	if !env.Success {
		return SidecarTranslateResult{}, search.ErrKeywordsFailed
	}
	return env.Data, nil
}
