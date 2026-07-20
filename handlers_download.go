package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xpzouying/xiaohongshu-mcp/search"
)

type xhsDownloadResolver interface {
	GetFeedDetail(context.Context, string, string, bool) (*FeedDetailResponse, error)
}

type sidecarDownloader interface {
	Download(context.Context, string, string) (*http.Response, error)
}

// xhsDownloadOpener는 실제 guard transport와 테스트 fake를 분리한다.
type xhsDownloadOpener interface {
	Open(context.Context, string) (*http.Response, error)
}

type guardedXHSDownloadOpener struct {
	client *http.Client
}

func (o *guardedXHSDownloadOpener) Open(ctx context.Context, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, errInvalidURL
	}
	return o.client.Do(req)
}

func sanitizeDownloadFilename(raw, platform, postID string) string {
	defaultName := platform + "-" + postID + ".mp4"
	if strings.TrimSpace(raw) == "" {
		raw = defaultName
	}
	clean := func(value string) string {
		return strings.TrimSpace(strings.Map(func(r rune) rune {
			switch r {
			case '\r', '\n', 0, '/', '\\', '"':
				return -1
			default:
				return r
			}
		}, value))
	}
	cleaned := clean(raw)
	if cleaned == "" {
		cleaned = clean(defaultName)
	}
	if cleaned == "" {
		cleaned = platform + "-video.mp4"
	}
	return cleaned
}

func (s *AppServer) downloadHandler(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 300*time.Second)
	defer cancel()

	platform := strings.TrimSpace(c.Query("platform"))
	postID := strings.TrimSpace(c.Query("post_id"))
	rawURL := c.Query("url")
	if postID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false})
		return
	}

	var (
		upstream *http.Response
		err      error
	)
	switch platform {
	case "xiaohongshu":
		detailToken := strings.TrimSpace(c.Query("detail_token"))
		if detailToken == "" || rawURL != "" || s.xhsDownloadResolver == nil || s.xhsDownloadOpener == nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false})
			return
		}
		detail, detailErr := s.xhsDownloadResolver.GetFeedDetail(ctx, postID, detailToken, false)
		if detailErr != nil || detail == nil || strings.TrimSpace(detail.VideoURL) == "" {
			writeDownloadError(c, ctx, http.StatusBadGateway)
			return
		}
		rawURL = detail.VideoURL
		if s.downloadGuard == nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false})
			return
		}
		if _, err = s.downloadGuard.Validate(ctx, platform, rawURL); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false})
			return
		}
		upstream, err = s.xhsDownloadOpener.Open(ctx, rawURL)
	case "douyin", "tiktok":
		if rawURL == "" || s.downloadGuard == nil || s.sidecarDownloader == nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false})
			return
		}
		if _, err = s.downloadGuard.Validate(ctx, platform, rawURL); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false})
			return
		}
		upstream, err = s.sidecarDownloader.Download(ctx, platform, rawURL)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"success": false})
		return
	}

	if err != nil {
		writeDownloadUpstreamError(c, ctx, err)
		return
	}
	if upstream == nil || upstream.Body == nil {
		writeDownloadError(c, ctx, http.StatusBadGateway)
		return
	}
	defer upstream.Body.Close()
	if upstream.StatusCode != http.StatusOK {
		writeDownloadStatus(c, ctx, upstream.StatusCode)
		return
	}

	contentType := upstream.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "video/mp4"
	}
	c.Header("Content-Type", contentType)
	if upstream.ContentLength >= 0 {
		c.Header("Content-Length", strconv.FormatInt(upstream.ContentLength, 10))
	}
	filename := sanitizeDownloadFilename(c.Query("filename"), platform, postID)
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("X-Download-Notice", "personal-reference-only")

	if _, copyErr := io.Copy(c.Writer, upstream.Body); copyErr != nil && !c.Writer.Written() {
		if errors.Is(copyErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			c.Status(http.StatusGatewayTimeout)
			return
		}
		c.Status(http.StatusBadGateway)
	}
}

func writeDownloadUpstreamError(c *gin.Context, ctx context.Context, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(ctx.Err(), context.DeadlineExceeded):
		writeDownloadError(c, ctx, http.StatusGatewayTimeout)
	case errors.Is(err, search.ErrForbidden):
		writeDownloadError(c, ctx, http.StatusForbidden)
	default:
		writeDownloadError(c, ctx, http.StatusBadGateway)
	}
}

func writeDownloadStatus(c *gin.Context, ctx context.Context, status int) {
	switch status {
	case http.StatusForbidden:
		writeDownloadError(c, ctx, http.StatusForbidden)
	case http.StatusGatewayTimeout:
		writeDownloadError(c, ctx, http.StatusGatewayTimeout)
	default:
		writeDownloadError(c, ctx, http.StatusBadGateway)
	}
}

func writeDownloadError(c *gin.Context, ctx context.Context, status int) {
	if errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	c.JSON(status, gin.H{"success": false})
}
