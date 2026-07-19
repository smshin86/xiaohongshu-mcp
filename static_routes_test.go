package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestStaticRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	app := NewAppServer(NewXiaohongshuService())
	router := setupRoutes(app)

	t.Run("루트에서 index.html 제공", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		require.Contains(t, rr.Header().Get("Content-Type"), "text/html")
		require.Contains(t, rr.Body.String(), "<!doctype html>")
		require.Contains(t, rr.Body.String(), "권한이 있는 콘텐츠의 개인 참고용")
	})

	t.Run("/static/app.js 제공", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/static/app.js", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		require.Contains(t, rr.Body.String(), "data-card-action")
		require.Contains(t, rr.Body.String(), "XHS 링크 복사 + 수동 도구")
	})

	t.Run("/static/style.css 제공", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/static/style.css", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		require.Contains(t, rr.Body.String(), ".card-actions")
		require.Contains(t, rr.Body.String(), "flex-wrap")
	})

	t.Run("/settings 가 settings.html 제공", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/settings", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		require.Contains(t, rr.Header().Get("Content-Type"), "text/html")
		require.Contains(t, rr.Body.String(), "<!doctype html>")
	})

	t.Run("/static/lib.js 제공", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/static/lib.js", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("/static/settings.js 제공", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/static/settings.js", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
	})

	// Task 5: URL 분석 진입 + 한국어→중국어 후보 칩 DOM/스크립트 hook 회귀
	t.Run("index.html 에 URL 분석/후보칩 hook 과 비-submit 버튼 존재", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		body := rr.Body.String()
		require.Equal(t, http.StatusOK, rr.Code)
		for _, hook := range []string{
			`id="entry-mode-toggle"`,
			`id="url-entry"`,
			`id="url-input"`,
			`id="analyze-btn"`,
			`id="candidate-chips"`,
		} {
			require.Contains(t, body, hook, "missing hook: %s", hook)
		}
		// 두 신규 버튼은 form submit 을 유발하지 않도록 type="button" 이어야 한다.
		require.Contains(t, body, `<button id="entry-mode-toggle" type="button"`)
		require.Contains(t, body, `<button id="analyze-btn" type="button"`)
	})

	t.Run("app.js 에 keyword API path 와 이벤트 hook 존재", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/static/app.js", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		body := rr.Body.String()
		require.Equal(t, http.StatusOK, rr.Code)
		require.Contains(t, body, "/api/v1/keywords/extract")
		require.Contains(t, body, "/api/v1/keywords/translate")
		require.Contains(t, body, `"entry-mode-toggle"`)
		require.Contains(t, body, `"analyze-btn"`)
	})
}
