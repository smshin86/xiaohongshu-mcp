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

// raw URL 이 정확히 한번만 encode 되어 sidecar 가 원문을 복원하는지 확인.
func TestSidecarDownloadEncodesQueryOnce(t *testing.T) {
	rawURL := "https://www.douyin.com/video/123?X-Bogus=abc&msToken=def#frag"
	var gotURL, gotPlatform string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.Query().Get("url")
		gotPlatform = r.URL.Query().Get("platform")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("data"))
	}))
	defer srv.Close()

	cli := NewSidecarClient(srv.URL, 0)
	resp, err := cli.Download(context.Background(), "douyin", rawURL)
	require.NoError(t, err)
	require.NotNil(t, resp)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	require.Equal(t, rawURL, gotURL, "sidecar 가 원문 raw URL 을 그대로 받아야 함(단일 encode)")
	require.Equal(t, "douyin", gotPlatform, "platform query 도 intact")
}

// Download 내부에서 body 를 읽지 않고 caller 가 chunk 단위로 읽을 수 있는지 확인.
func TestSidecarDownloadDoesNotReadBody(t *testing.T) {
	chunks := []string{"chunk1-", "chunk2-", "chunk3"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		for _, c := range chunks {
			_, _ = w.Write([]byte(c))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer srv.Close()

	cli := NewSidecarClient(srv.URL, 0)
	resp, err := cli.Download(context.Background(), "douyin", "https://example.com/v")
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "chunk1-chunk2-chunk3", string(got), "caller 가 chunk 순서대로 읽음")
}

// caller ctx cancel 시 request 가 종료되고 에러가 전달되는지 확인.
func TestSidecarDownloadContextCancelClosesBody(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		// client ctx cancel 이 전파되면 server r.Context() 도 종료 → handler 즉시 반환.
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cli := NewSidecarClient(srv.URL, 0)
	go func() {
		<-started
		cancel()
	}()

	resp, err := cli.Download(ctx, "douyin", "https://example.com/v")
	require.ErrorIs(t, err, context.Canceled)
	if resp != nil {
		resp.Body.Close()
	}
}

// 상태코드 → sentinel 매핑(403/504/502/500).
func TestSidecarDownloadMapsStatusToSentinels(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{403, search.ErrForbidden},
		{504, context.DeadlineExceeded},
		{502, search.ErrBadGateway},
		{500, search.ErrBadGateway},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
		}))
		cli := NewSidecarClient(srv.URL, 0)
		resp, err := cli.Download(context.Background(), "douyin", "https://example.com/v?a=b")
		require.ErrorIs(t, err, c.want, "status %d", c.status)
		if resp != nil {
			resp.Body.Close()
		}
		srv.Close()
	}
}

// 네트워크 에러(연결 거부) → ErrUnreachable.
func TestSidecarDownloadNetworkErrorUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()
	cli := NewSidecarClient(srv.URL, 0)
	_, err := cli.Download(context.Background(), "douyin", "https://example.com/v?a=b")
	require.ErrorIs(t, err, search.ErrUnreachable)
}

// 에러 문자열에 signed query / 원문 URL 이 노출되지 않는지 확인.
func TestSidecarDownloadErrorHasNoSignedQuery(t *testing.T) {
	rawURL := "https://www.douyin.com/video/123?X-Bogus=SECRET123&sign=ABCDEF"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
	}))
	defer srv.Close()
	cli := NewSidecarClient(srv.URL, 0)
	_, err := cli.Download(context.Background(), "douyin", rawURL)
	require.Error(t, err)
	s := err.Error()
	require.NotContains(t, s, "SECRET123")
	require.NotContains(t, s, "ABCDEF")
	require.NotContains(t, s, "X-Bogus")
}
