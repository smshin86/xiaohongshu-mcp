package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/search"
)

type downloadPublicResolver struct{}

func (downloadPublicResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
}

type downloadPrivateResolver struct{}

func (downloadPrivateResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
}

type fakeXHSDownloadResolver struct {
	result  *FeedDetailResponse
	err     error
	calls   int
	postID  string
	token   string
	ctx     context.Context
	loadAll bool
}

func (f *fakeXHSDownloadResolver) GetFeedDetail(ctx context.Context, postID, token string, loadAll bool) (*FeedDetailResponse, error) {
	f.calls++
	f.postID, f.token, f.ctx, f.loadAll = postID, token, ctx, loadAll
	return f.result, f.err
}

type fakeSidecarDownloader struct {
	response *http.Response
	err      error
	calls    int
	platform string
	rawURL   string
	ctx      context.Context
}

func (f *fakeSidecarDownloader) Download(ctx context.Context, platform, rawURL string) (*http.Response, error) {
	f.calls++
	f.ctx, f.platform, f.rawURL = ctx, platform, rawURL
	return f.response, f.err
}

type fakeXHSDownloadOpener struct {
	response *http.Response
	err      error
	calls    int
	rawURL   string
	ctx      context.Context
}

func (f *fakeXHSDownloadOpener) Open(ctx context.Context, rawURL string) (*http.Response, error) {
	f.calls++
	f.ctx, f.rawURL = ctx, rawURL
	return f.response, f.err
}

type trackingReadCloser struct {
	io.Reader
	closed int
}

func (b *trackingReadCloser) Close() error {
	b.closed++
	return nil
}

func downloadResponse(status int, body *trackingReadCloser) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Header:        http.Header{"Content-Type": []string{"video/webm"}},
		Body:          body,
		ContentLength: -1,
	}
}

func newDownloadTestApp() (*AppServer, *fakeXHSDownloadResolver, *fakeXHSDownloadOpener, *fakeSidecarDownloader) {
	app := NewAppServer(NewXiaohongshuService())
	resolver := &fakeXHSDownloadResolver{}
	opener := &fakeXHSDownloadOpener{}
	sidecar := &fakeSidecarDownloader{}
	app.xhsDownloadResolver = resolver
	app.xhsDownloadOpener = opener
	app.sidecarDownloader = sidecar
	app.downloadGuard = newDownloadURLGuardWithResolver(downloadPublicResolver{}, &net.Dialer{})
	return app, resolver, opener, sidecar
}

func performDownloadRequest(app *AppServer, query url.Values) *httptest.ResponseRecorder {
	router := setupRoutes(app)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/download?"+query.Encode(), nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

func TestDownloadDouyinStreamsAndSanitizesFilename(t *testing.T) {
	app, _, _, sidecar := newDownloadTestApp()
	body := &trackingReadCloser{Reader: strings.NewReader("left-right")}
	sidecar.response = downloadResponse(http.StatusOK, body)
	sidecar.response.ContentLength = int64(len("left-right"))
	rawURL := "https://v.douyinvod.com/video.mp4?token=secret"

	rr := performDownloadRequest(app, url.Values{
		"platform": {"douyin"},
		"post_id":  {"p1"},
		"url":      {rawURL},
		"filename": {"../bad\r\nname.mp4"},
	})

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "left-right", rr.Body.String())
	require.Equal(t, "video/webm", rr.Header().Get("Content-Type"))
	require.Equal(t, "10", rr.Header().Get("Content-Length"))
	require.Equal(t, `attachment; filename="..badname.mp4"`, rr.Header().Get("Content-Disposition"))
	require.Equal(t, "nosniff", rr.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "personal-reference-only", rr.Header().Get("X-Download-Notice"))
	require.Equal(t, 1, body.closed)
	require.Equal(t, 1, sidecar.calls)
	require.Equal(t, rawURL, sidecar.rawURL)
	require.Equal(t, "douyin", sidecar.platform)
	deadline, ok := sidecar.ctx.Deadline()
	require.True(t, ok)
	require.WithinDuration(t, time.Now().Add(300*time.Second), deadline, 3*time.Second)
}

func TestSanitizeDownloadFilenameAlsoCleansDefaultPostID(t *testing.T) {
	require.Equal(t, "tiktok-..evil.mp4.mp4", sanitizeDownloadFilename("", "tiktok", "../evil.mp4\r\n"))
	require.Equal(t, "douyin-p.mp4", sanitizeDownloadFilename("\r\n/\\\"", "douyin", "p"))
}

func TestDownloadXHSUsesDetailURLAndRejectsClientURL(t *testing.T) {
	t.Run("detail URL only", func(t *testing.T) {
		app, resolver, opener, sidecar := newDownloadTestApp()
		resolvedURL := "https://sns-video-hw.xhscdn.com/stream/video.mp4"
		resolver.result = &FeedDetailResponse{VideoURL: resolvedURL}
		body := &trackingReadCloser{Reader: strings.NewReader("xhs")}
		opener.response = downloadResponse(http.StatusOK, body)

		rr := performDownloadRequest(app, url.Values{
			"platform":     {"xiaohongshu"},
			"post_id":      {"note1"},
			"detail_token": {"detail-secret"},
		})

		require.Equal(t, http.StatusOK, rr.Code)
		require.Equal(t, "xhs", rr.Body.String())
		require.Equal(t, 1, resolver.calls)
		require.Equal(t, "note1", resolver.postID)
		require.Equal(t, "detail-secret", resolver.token)
		require.False(t, resolver.loadAll)
		require.Equal(t, resolvedURL, opener.rawURL)
		require.Equal(t, 1, opener.calls)
		require.Equal(t, 0, sidecar.calls)
		require.Equal(t, 1, body.closed)
	})

	t.Run("client URL rejected before detail", func(t *testing.T) {
		app, resolver, opener, _ := newDownloadTestApp()
		rr := performDownloadRequest(app, url.Values{
			"platform":     {"xiaohongshu"},
			"post_id":      {"note1"},
			"detail_token": {"token"},
			"url":          {"https://sns-video-hw.xhscdn.com/evil"},
		})
		require.Equal(t, http.StatusBadRequest, rr.Code)
		require.Zero(t, resolver.calls)
		require.Zero(t, opener.calls)
	})
}

func TestDownloadRejectsInvalidRequestsBeforeSidecar(t *testing.T) {
	cases := []url.Values{
		{"platform": {"instagram"}, "post_id": {"p"}, "url": {"https://www.tiktok.com/v"}},
		{"platform": {"tiktok"}, "url": {"https://www.tiktok.com/v"}},
		{"platform": {"tiktok"}, "post_id": {"p"}},
		{"platform": {"tiktok"}, "post_id": {"p"}, "url": {"http://www.tiktok.com/v"}},
		{"platform": {"tiktok"}, "post_id": {"p"}, "url": {"https://tiktok.com.evil.test/v"}},
	}
	for _, query := range cases {
		app, _, _, sidecar := newDownloadTestApp()
		rr := performDownloadRequest(app, query)
		require.Equal(t, http.StatusBadRequest, rr.Code, query.Encode())
		require.Zero(t, sidecar.calls)
	}

	app, _, _, sidecar := newDownloadTestApp()
	app.downloadGuard = newDownloadURLGuardWithResolver(downloadPrivateResolver{}, &net.Dialer{})
	rr := performDownloadRequest(app, url.Values{
		"platform": {"douyin"}, "post_id": {"p"}, "url": {"https://v.douyinvod.com/video.mp4"},
	})
	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Zero(t, sidecar.calls)
}

func TestDownloadMapsDetailAndUpstreamFailures(t *testing.T) {
	t.Run("missing detail token", func(t *testing.T) {
		app, resolver, _, _ := newDownloadTestApp()
		rr := performDownloadRequest(app, url.Values{"platform": {"xiaohongshu"}, "post_id": {"n"}})
		require.Equal(t, http.StatusBadRequest, rr.Code)
		require.Zero(t, resolver.calls)
	})

	for name, resultErr := range map[string]struct {
		result *FeedDetailResponse
		err    error
	}{
		"detail failed": {err: errors.New("private detail")},
		"empty video":   {result: &FeedDetailResponse{}},
	} {
		t.Run(name, func(t *testing.T) {
			app, resolver, opener, _ := newDownloadTestApp()
			resolver.result, resolver.err = resultErr.result, resultErr.err
			rr := performDownloadRequest(app, url.Values{
				"platform": {"xiaohongshu"}, "post_id": {"n"}, "detail_token": {"secret"},
			})
			require.Equal(t, http.StatusBadGateway, rr.Code)
			require.Zero(t, opener.calls)
			require.NotContains(t, rr.Body.String(), "secret")
		})
	}

	for name, tc := range map[string]struct {
		err    error
		status int
	}{
		"forbidden": {search.ErrForbidden, http.StatusForbidden},
		"timeout":   {context.DeadlineExceeded, http.StatusGatewayTimeout},
		"upstream":  {search.ErrBadGateway, http.StatusBadGateway},
	} {
		t.Run(name, func(t *testing.T) {
			app, _, _, sidecar := newDownloadTestApp()
			sidecar.err = tc.err
			rr := performDownloadRequest(app, url.Values{
				"platform": {"tiktok"}, "post_id": {"p"}, "url": {"https://v.tiktokcdn.com/v?token=secret"},
			})
			require.Equal(t, tc.status, rr.Code)
			require.NotContains(t, rr.Body.String(), "secret")
		})
	}
}

func TestDownloadRouteLoggerOmitsRawQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldWriter := gin.DefaultWriter
	var logs bytes.Buffer
	gin.DefaultWriter = &logs
	t.Cleanup(func() { gin.DefaultWriter = oldWriter })

	app, _, _, _ := newDownloadTestApp()
	_ = performDownloadRequest(app, url.Values{
		"platform": {"tiktok"}, "post_id": {"p"}, "url": {"https://v.tiktokcdn.com/v?token=SUPERSECRET"},
	})

	require.Contains(t, logs.String(), "/api/v1/download")
	require.NotContains(t, logs.String(), "SUPERSECRET")
	require.NotContains(t, logs.String(), "token=")
}
