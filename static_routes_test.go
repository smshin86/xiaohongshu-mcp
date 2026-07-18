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
	})

	t.Run("/static/app.js 제공", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/static/app.js", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
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
}
