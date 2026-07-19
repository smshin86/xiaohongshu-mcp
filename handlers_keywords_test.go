package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/search"
)

// fakeKeywords: sidecarKeywords seam 테스트 더블.
type fakeKeywords struct {
	extractResult   SidecarKeywordResult
	extractErr      error
	translateResult SidecarTranslateResult
	translateErr    error
}

func (f *fakeKeywords) ExtractKeywords(ctx context.Context, urls []string) (SidecarKeywordResult, error) {
	return f.extractResult, f.extractErr
}

func (f *fakeKeywords) TranslateKeywords(ctx context.Context, text, sourceLang string) (SidecarTranslateResult, error) {
	return f.translateResult, f.translateErr
}

func newKeywordsServer(kw sidecarKeywords) *AppServer {
	gin.SetMode(gin.TestMode)
	app := NewAppServer(NewXiaohongshuService())
	app.sidecarKeywords = kw
	return app
}

// 성공 → 200 success:true; ErrKeywordsFailed → 200 success:false(직접 입력 복구).
func TestExtractHandlerSuccessAndRecoverable(t *testing.T) {
	s := newKeywordsServer(&fakeKeywords{
		extractResult: SidecarKeywordResult{
			Candidates: []SidecarKeywordCandidate{{Keyword: "팬", SourceURL: "https://www.youtube.com/a", Basis: "title", Confidence: 0.9}},
			Note:       "ok",
		},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte(`{"urls":["https://www.youtube.com/a"]}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	s.extractKeywordsHandler(c)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"success":true`)
	require.Contains(t, w.Body.String(), `"keyword":"팬"`)

	s2 := newKeywordsServer(&fakeKeywords{extractErr: search.ErrKeywordsFailed})
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte(`{"urls":["https://www.youtube.com/a"]}`)))
	c2.Request.Header.Set("Content-Type", "application/json")
	s2.extractKeywordsHandler(c2)
	require.Equal(t, 200, w2.Code)
	require.Contains(t, w2.Body.String(), `"success":false`)
}

// 빈/4 urls → 400.
func TestExtractHandlerInputValidation(t *testing.T) {
	s := newKeywordsServer(&fakeKeywords{})
	cases := []struct {
		name string
		body string
	}{
		{"empty urls", `{"urls":[]}`},
		{"4 urls", `{"urls":["a","b","c","d"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte(tc.body)))
			c.Request.Header.Set("Content-Type", "application/json")
			s.extractKeywordsHandler(c)
			require.Equal(t, 400, w.Code)
			require.Contains(t, w.Body.String(), `"success":false`)
		})
	}
}

func TestExtractHandlerUnreachable502(t *testing.T) {
	s := newKeywordsServer(&fakeKeywords{extractErr: search.ErrUnreachable})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte(`{"urls":["https://www.youtube.com/a"]}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	s.extractKeywordsHandler(c)
	require.Equal(t, 502, w.Code)
}

func TestExtractHandlerDeadlineExceeded504(t *testing.T) {
	s := newKeywordsServer(&fakeKeywords{extractErr: context.DeadlineExceeded})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte(`{"urls":["https://www.youtube.com/a"]}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	s.extractKeywordsHandler(c)
	require.Equal(t, 504, w.Code)
}

// 빈/200자 초과 text·source_lang!="ko" → 400; ErrUnreachable/ErrBadGateway → 502.
func TestTranslateHandlerEmptyAndLangValidation(t *testing.T) {
	s := newKeywordsServer(&fakeKeywords{})
	cases := []struct {
		name string
		body string
	}{
		{"empty text", `{"text":"","source_lang":"ko"}`},
		{"over 200 chars", `{"text":"` + strings.Repeat("가", 201) + `","source_lang":"ko"}`},
		{"non-ko source_lang", `{"text":"안녕","source_lang":"en"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte(tc.body)))
			c.Request.Header.Set("Content-Type", "application/json")
			s.translateKeywordsHandler(c)
			require.Equal(t, 400, w.Code)
			require.Contains(t, w.Body.String(), `"success":false`)
		})
	}
}

func TestTranslateHandlerUnreachableBadGateway502(t *testing.T) {
	s := newKeywordsServer(&fakeKeywords{translateErr: search.ErrUnreachable})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte(`{"text":"휴대용 선풍기","source_lang":"ko"}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	s.translateKeywordsHandler(c)
	require.Equal(t, 502, w.Code)

	s2 := newKeywordsServer(&fakeKeywords{translateErr: search.ErrBadGateway})
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte(`{"text":"휴대용 선풍기","source_lang":"ko"}`)))
	c2.Request.Header.Set("Content-Type", "application/json")
	s2.translateKeywordsHandler(c2)
	require.Equal(t, 502, w2.Code)
}

// 취소된 ctx → 응답 미작성(body 길이 0, Recorder 기본 code 판정 안 함).
func TestHandlerCanceledWritesNothing(t *testing.T) {
	s := newKeywordsServer(&fakeKeywords{extractErr: context.Canceled})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte(`{"urls":["https://www.youtube.com/a"]}`))).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	s.extractKeywordsHandler(c)
	require.Equal(t, 0, w.Body.Len(), "취소 시 응답 body 미작성")
}

func TestKeywordRoutesRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	app := NewAppServer(NewXiaohongshuService())
	router := setupRoutes(app)
	paths := map[string]bool{}
	for _, r := range router.Routes() {
		if r.Method == http.MethodPost {
			paths[r.Path] = true
		}
	}
	require.True(t, paths["/api/v1/keywords/extract"], "extract 라우트 등록")
	require.True(t, paths["/api/v1/keywords/translate"], "translate 라우트 등록")
}
