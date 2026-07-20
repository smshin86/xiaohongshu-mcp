package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/search"
)

// mainFakeAdapter: 핸들러 테스트용(search 패키지 fake 과 별개).
type mainFakeAdapter struct {
	name  string
	items []search.VideoItem
}

func (f *mainFakeAdapter) Name() string { return f.name }
func (f *mainFakeAdapter) Available(ctx context.Context) search.Availability {
	return search.Availability{Available: true}
}
func (f *mainFakeAdapter) Search(ctx context.Context, q search.SearchQuery) (search.AdapterSearchPage, error) {
	return search.AdapterSearchPage{Items: f.items}, nil
}

func newTestApp(t *testing.T) *AppServer {
	gin.SetMode(gin.TestMode)
	app := NewAppServer(NewXiaohongshuService())
	app.aggregator = search.NewAggregatorService(map[string]search.VideoAdapter{
		"xiaohongshu": &mainFakeAdapter{name: "xiaohongshu", items: []search.VideoItem{{Platform: "xiaohongshu", PostID: "a"}}},
		"douyin":      &mainFakeAdapter{name: "douyin", items: []search.VideoItem{{Platform: "douyin", PostID: "b"}}},
	})
	return app
}

func TestUnifiedSearchReturnsMergedItems(t *testing.T) {
	app := newTestApp(t)
	router := setupRoutes(app)
	body, _ := json.Marshal(search.AggregatorRequest{Keyword: "便携风扇", Platforms: []string{"xiaohongshu", "douyin"}, Sort: "relevance"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	var wrap struct {
		Success bool                     `json:"success"`
		Data    *search.AggregatedResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &wrap))
	require.True(t, wrap.Success)
	require.Len(t, wrap.Data.Items, 2)
}

func TestUnifiedSearchRejectsInvalidSort(t *testing.T) {
	app := newTestApp(t)
	router := setupRoutes(app)
	body, _ := json.Marshal(search.AggregatorRequest{Keyword: "k", Sort: "bogus"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestUnifiedSearchRejectsInvalidPlatforms(t *testing.T) {
	app := newTestApp(t)
	router := setupRoutes(app)
	body, _ := json.Marshal(search.AggregatorRequest{Keyword: "k", Platforms: []string{"instagram"}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestUnifiedSearchRejectsEmptyKeyword(t *testing.T) {
	app := newTestApp(t)
	router := setupRoutes(app)
	body, _ := json.Marshal(search.AggregatorRequest{Keyword: "  "})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestSearchCapabilitiesEndpoint(t *testing.T) {
	app := newTestApp(t)
	router := setupRoutes(app)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/search/capabilities", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	var wrap struct {
		Success bool                   `json:"success"`
		Data    searchCapabilitiesData `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &wrap))
	require.True(t, wrap.Success)
	require.Equal(t, "per_platform_rank", wrap.Data.MergeRule)
	require.Equal(t, "opaque_cursor", wrap.Data.Platforms["douyin"].Pagination)
	require.Equal(t, "single_page", wrap.Data.Platforms["xiaohongshu"].Pagination)
	require.Equal(t, "single_page", wrap.Data.Platforms["tiktok"].Pagination)
	require.Contains(t, wrap.Data.Platforms["douyin"].Sorts, "popularity")
	require.Contains(t, wrap.Data.Platforms["douyin"].NativeFilters, "duration")
	require.True(t, wrap.Data.Platforms["douyin"].Available) // 동적 가용성(fake=true)
}
