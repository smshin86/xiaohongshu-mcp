package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/xpzouying/xiaohongshu-mcp/search"
)

// sidecarKeywords: 키워드 게이트웨이 seam. *SidecarClient 가 구현.
type sidecarKeywords interface {
	ExtractKeywords(ctx context.Context, urls []string) (SidecarKeywordResult, error)
	TranslateKeywords(ctx context.Context, text, sourceLang string) (SidecarTranslateResult, error)
}

const (
	keywordsTimeout  = 60 * time.Second // sidecar LLM 왕복에 맞춘 충분한 여유
	maxKeywordURLs   = 3                // URL 은 1~3개
	maxTranslateText = 200              // 한국어 키워드 원문 길이 상한
)

// keywordExtractRequest: POST /api/v1/keywords/extract 바디.
type keywordExtractRequest struct {
	URLs []string `json:"urls"`
}

// keywordTranslateRequest: POST /api/v1/keywords/translate 바디.
type keywordTranslateRequest struct {
	Text       string `json:"text"`
	SourceLang string `json:"source_lang"`
}

// extractKeywordsHandler: URL 목록 → 사이드카 키워드 추출 프록시.
func (s *AppServer) extractKeywordsHandler(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), keywordsTimeout)
	defer cancel()

	var req keywordExtractRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "요청 본문이 올바르지 않습니다."})
		return
	}
	// 입력 개수 검증(빈/4개 이상 = 400). trim+dedupe 는 sidecar 담당.
	if len(req.URLs) == 0 || len(req.URLs) > maxKeywordURLs {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "URL은 1~3개까지 가능합니다."})
		return
	}

	res, err := s.sidecarKeywords.ExtractKeywords(ctx, req.URLs)
	if err != nil {
		writeKeywordError(c, ctx, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": res})
}

// translateKeywordsHandler: 한국어 키워드 → 중국어 번역 프록시.
func (s *AppServer) translateKeywordsHandler(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), keywordsTimeout)
	defer cancel()

	var req keywordTranslateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "요청 본문이 올바르지 않습니다."})
		return
	}
	text := strings.TrimSpace(req.Text)
	// 길이는 rune 단위(한글 200자 계약) — len() 은 UTF-8 바이트 수라 한글이 과단축된다.
	if text == "" || utf8.RuneCountInString(text) > maxTranslateText {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "키워드는 1~200자여야 합니다."})
		return
	}
	// 현재 한국어 원문만 지원(source_lang == "ko").
	if req.SourceLang != "ko" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "현재 한국어만 지원합니다."})
		return
	}

	res, err := s.sidecarKeywords.TranslateKeywords(ctx, text, req.SourceLang)
	if err != nil {
		writeKeywordError(c, ctx, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": res})
}

// writeKeywordError: 핸들러 에러 매핑. ctx 취소 시 응답 미작성(download 패턴).
// 검사 순서: Canceled → DeadlineExceeded → sentinels(ErrKeywordsFailed/Unreachable/BadGateway) → 500.
func writeKeywordError(c *gin.Context, ctx context.Context, err error) {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		c.JSON(http.StatusGatewayTimeout, gin.H{"success": false, "message": "응답 시간 초과입니다."})
		return
	}
	switch {
	case errors.Is(err, search.ErrKeywordsFailed):
		// 직접 입력 복구 — 메시지 없이 success:false.
		c.JSON(http.StatusOK, gin.H{"success": false})
	case errors.Is(err, search.ErrUnreachable):
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "서비스에 연결할 수 없습니다."})
	case errors.Is(err, search.ErrBadGateway):
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "키워드 서비스 응답이 올바르지 않습니다."})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "오류가 발생했습니다."})
	}
}
