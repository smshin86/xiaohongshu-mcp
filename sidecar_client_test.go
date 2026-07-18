package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/search"
)

func TestSidecarSearchMapsStatusToSentinels(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{503, search.ErrUnavailable},
		{502, search.ErrBadGateway},
		{500, search.ErrBadGateway},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" {
				w.WriteHeader(200)
				return
			}
			w.WriteHeader(c.status)
		}))
		defer srv.Close()
		cli := NewSidecarClient(srv.URL, 0)
		_, err := cli.Search(context.Background(), SidecarSearchRequest{Platform: "douyin", Q: "x"})
		require.ErrorIs(t, err, c.want, "status %d", c.status)
	}
}

func TestSidecarSearchParsesWrappedVideosAndBodyPlatform(t *testing.T) {
	// platform 은 body 에 전달되어야 함(쿼리 아님). 응답은 {success,data:{...}} 래핑.
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		require.Empty(t, r.URL.Query().Get("platform"), "platform 은 쿼리가 아닌 body")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"videos":[{"platform":"douyin","post_id":"a"}],"next_cursor":"eyIi","has_more":true}}`))
	}))
	defer srv.Close()
	cli := NewSidecarClient(srv.URL, 0)
	resp, err := cli.Search(context.Background(), SidecarSearchRequest{Platform: "douyin", Q: "x"})
	require.NoError(t, err)
	require.Len(t, resp.Videos, 1)
	require.True(t, resp.HasMore)
	require.Equal(t, "eyIi", resp.NextCursor)
	require.Equal(t, "douyin", gotBody["platform"], "platform 이 body 에 있어야 함")
}

func TestSidecarSearchNetworkErrorUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()
	cli := NewSidecarClient(srv.URL, 0)
	_, err := cli.Search(context.Background(), SidecarSearchRequest{Platform: "douyin", Q: "x"})
	require.ErrorIs(t, err, search.ErrUnreachable)
}

func TestSidecarHealthzCached(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","douyin":true,"tiktok":false,"llm":false}`))
	}))
	defer srv.Close()
	cli := NewSidecarClient(srv.URL, 5*time.Second)
	for i := 0; i < 3; i++ {
		h, err := cli.Healthz(context.Background())
		require.NoError(t, err)
		require.True(t, h.Douyin)
	}
	require.Equal(t, 1, calls, "healthz 는 TTL 내 1회만 호출")
}
