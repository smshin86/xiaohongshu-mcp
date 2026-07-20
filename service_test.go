package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-rod/rod"
	"github.com/stretchr/testify/require"
)

// TestSavedCookiesAt: 쿠키 파일 존재/크기/디렉토리 여부로 fast path 진입 조건을 결정한다.
// 임시 경로로 결정적으로 검증(실제 쿠키 파일에 의존하지 않음).
func TestSavedCookiesAt(t *testing.T) {
	dir := t.TempDir()

	// (1) 존재하지 않는 경로 → false (가장 흔한 fast-path 진입 케이스).
	require.False(t, savedCookiesAt(filepath.Join(dir, "missing.json")))

	// (2) 빈 파일(크기 0) → false: 0바이트 쿠키는 미로그인과 동급.
	empty := filepath.Join(dir, "empty.json")
	require.NoError(t, os.WriteFile(empty, nil, 0o600))
	require.False(t, savedCookiesAt(empty))

	// (3) 내용이 있는 파일(크기 > 0) → true: 저장된 로그인 쿠키가 있으면 브라우저 검증 진행.
	full := filepath.Join(dir, "cookies.json")
	require.NoError(t, os.WriteFile(full, []byte(`{"a":1}`), 0o600))
	require.True(t, savedCookiesAt(full))

	// (4) 경로가 디렉토리 → false: 파일이 아니면 쿠키로 인정하지 않는다.
	require.False(t, savedCookiesAt(dir))
}

// fakeSession: sessionRunner 테스트용 가짜. 브라우저/rod 없이 dispatch 만 검증.
type fakeSession struct {
	loggedIn     bool
	startImg     string
	startAlready bool
	startErr     error
	withPageErr  error // WithPage 가 fn 호출 없이 반환할 에러(ErrNoSession 등)
	logoutCalls  int
	closeCalls   int
}

func (f *fakeSession) LoggedIn() bool { return f.loggedIn }
func (f *fakeSession) StartLogin(ctx context.Context) (string, bool, error) {
	return f.startImg, f.startAlready, f.startErr
}
func (f *fakeSession) WithPage(ctx context.Context, fn func(*rod.Page) error) error {
	return f.withPageErr
}
func (f *fakeSession) Logout() error { f.logoutCalls++; return nil }
func (f *fakeSession) Close() error  { f.closeCalls++; return nil }

// TestCheckLoginStatusFastPath: live 세션 LoggedIn=true 면 브라우저 없이 즉시 true.
// 세션의 LoggedIn 은 로그인 확정 시점에 loggedIn+식별+안정화로 robust 판정된 값이다.
func TestCheckLoginStatusFastPath(t *testing.T) {
	s := &XiaohongshuService{session: &fakeSession{loggedIn: true}}

	resp, err := s.CheckLoginStatus(context.Background())
	require.NoError(t, err)
	require.True(t, resp.IsLoggedIn)
}

// TestSearchFeedsPropagatesLiveError: live 페이지에서 난 에러(ErrNoSession 아님)는
// fallback 하지 않고 그대로 반환한다. dispatch 조건 회귀 방지.
// (ErrNoSession → fallback 경로는 실제 브라우저가 필요해 단위 테스트 범위가 아니다.)
func TestSearchFeedsPropagatesLiveError(t *testing.T) {
	sentinel := errors.New("live search boom")
	s := &XiaohongshuService{session: &fakeSession{withPageErr: sentinel}}

	_, err := s.SearchFeeds(context.Background(), "fan")
	require.ErrorIs(t, err, sentinel)
}

// TestGetLoginQrcodeDelegates: GetLoginQrcode 는 세션 매니저의 StartLogin 결과를
// 그대로 응답에 반영한다(브라우저/QR 없이 위임 검증).
func TestGetLoginQrcodeDelegates(t *testing.T) {
	s := &XiaohongshuService{session: &fakeSession{startImg: "data:qr", startAlready: false}}

	resp, err := s.GetLoginQrcode(context.Background())
	require.NoError(t, err)
	require.Equal(t, "data:qr", resp.Img)
	require.False(t, resp.IsLoggedIn)
	require.Equal(t, "4m0s", resp.Timeout)
}
