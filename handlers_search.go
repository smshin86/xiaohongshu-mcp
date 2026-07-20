package main

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/xpzouying/xiaohongshu-mcp/search"
)

var validPlatformSet = map[string]bool{
	"xiaohongshu": true,
	"douyin":      true,
	"tiktok":      true,
}

// validSort: 허용 정렬 키(폴백 없음).
func validSort(s string) bool {
	return s == "relevance" || s == "popularity" || s == "latest"
}

// validPlatforms: 모든 platform 이 허용 집합에 포함되는지.
func validPlatforms(ps []string) bool {
	for _, p := range ps {
		if !validPlatformSet[p] {
			return false
		}
	}
	return true
}

// searchCapabilitiesHandler: GET /api/v1/search/capabilities
func (s *AppServer) searchCapabilitiesHandler(c *gin.Context) {
	avail := s.aggregator.Availability(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{"success": true, "data": searchCapabilitiesData{
		Platforms: searchPlatformCapabilities(avail),
		MergeRule: "per_platform_rank",
	}})
}

// unifiedSearchHandler: POST /api/v1/search — 통합 영상 검색.
func (s *AppServer) unifiedSearchHandler(c *gin.Context) {
	var req search.AggregatorRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "잘못된 요청입니다."})
		return
	}
	req.Keyword = strings.TrimSpace(req.Keyword)
	if req.Keyword == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "검색어를 입력해 주세요."})
		return
	}
	if req.Sort == "" {
		req.Sort = "relevance"
	}
	if !validSort(req.Sort) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "sort 는 relevance|popularity|latest 만 허용됩니다."})
		return
	}
	if !validPlatforms(req.Platforms) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "platforms 는 xiaohongshu|douyin|tiktok 만 허용됩니다."})
		return
	}
	if req.Filters.PerPlatformLimit <= 0 {
		req.Filters.PerPlatformLimit = 15
	}
	req.Filters.VideoOnly = true // M1 은 영상 검색 전용; false 입력도 서버에서 true 로 정규화

	res, err := s.aggregator.Search(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "검색 중 오류가 발생했습니다."})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": res})
}
