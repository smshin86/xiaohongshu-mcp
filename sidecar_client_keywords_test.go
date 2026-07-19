package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/search"
)

// 성공 응답 파싱: 후보 필드(keyword/source_url/basis/confidence) + note.
func TestSidecarExtractParsesCandidates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/keywords/extract", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"candidates":[{"keyword":"팬","source_url":"https://www.youtube.com/a","basis":"title","confidence":0.9}],"note":"ok"}}`))
	}))
	defer srv.Close()

	cli := NewSidecarClient(srv.URL, 0)
	res, err := cli.ExtractKeywords(context.Background(), []string{"https://www.youtube.com/a"})
	require.NoError(t, err)
	require.Len(t, res.Candidates, 1)
	require.Equal(t, "팬", res.Candidates[0].Keyword)
	require.Equal(t, "https://www.youtube.com/a", res.Candidates[0].SourceURL)
	require.Equal(t, "title", res.Candidates[0].Basis)
	require.Equal(t, 0.9, res.Candidates[0].Confidence)
	require.Equal(t, "ok", res.Note)
}

// success:false → ErrKeywordsFailed (ErrBadGateway 아님 — 게이트웨이가 직접 입력 복구).
func TestSidecarExtractSuccessFalseErrKeywordsFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":false,"data":{}}`))
	}))
	defer srv.Close()

	cli := NewSidecarClient(srv.URL, 0)
	_, err := cli.ExtractKeywords(context.Background(), []string{"https://www.youtube.com/a"})
	require.ErrorIs(t, err, search.ErrKeywordsFailed)
}

// 502 → ErrBadGateway.
func TestSidecarExtractBadStatusErrBadGateway(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	cli := NewSidecarClient(srv.URL, 0)
	_, err := cli.ExtractKeywords(context.Background(), []string{"https://www.youtube.com/a"})
	require.ErrorIs(t, err, search.ErrBadGateway)
}

// 네트워크 에러(연결 거부) → ErrUnreachable.
func TestSidecarExtractNetworkErrUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	cli := NewSidecarClient(srv.URL, 0)
	_, err := cli.ExtractKeywords(context.Background(), []string{"https://www.youtube.com/a"})
	require.ErrorIs(t, err, search.ErrUnreachable)
}

// caller ctx cancel → 원문 context.Canceled 상위 전파(Search/Download 패턴 동일).
// 미리 cancel 된 ctx 로 요청 → http.Client 가 즉시 context.Canceled 반환.
func TestSidecarExtractCanceledPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	cli := NewSidecarClient(srv.URL, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := cli.ExtractKeywords(ctx, []string{"https://www.youtube.com/a"})
	require.ErrorIs(t, err, context.Canceled)
}

// translate 성공 응답 파싱: 후보 필드(zh).
func TestSidecarTranslateParsesCandidates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/keywords/translate", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"candidates":[{"zh":"便携风扇"}]}}`))
	}))
	defer srv.Close()

	cli := NewSidecarClient(srv.URL, 0)
	res, err := cli.TranslateKeywords(context.Background(), "휴대용 선풍기", "ko")
	require.NoError(t, err)
	require.Len(t, res.Candidates, 1)
	require.Equal(t, "便携风扇", res.Candidates[0].ZH)
}

// translate success:false → ErrKeywordsFailed.
func TestSidecarTranslateSuccessFalseErrKeywordsFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":false,"data":{}}`))
	}))
	defer srv.Close()

	cli := NewSidecarClient(srv.URL, 0)
	_, err := cli.TranslateKeywords(context.Background(), "휴대용 선풍기", "ko")
	require.ErrorIs(t, err, search.ErrKeywordsFailed)
}
