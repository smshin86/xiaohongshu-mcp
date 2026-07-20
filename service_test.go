package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-rod/rod"
	"github.com/stretchr/testify/require"
	xhserrors "github.com/xpzouying/xiaohongshu-mcp/errors"
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

// TestCheckLoginStatusLiveAuthed: live 세션(LoggedIn=true) 에서 liveAuth 재검증이
// true 면 IsLoggedIn=true. 캐시 bool 만 보지 않고 live 페이지로 확인하는 경로.
func TestCheckLoginStatusLiveAuthed(t *testing.T) {
	s := &XiaohongshuService{
		session:  &fakeSession{loggedIn: true},
		liveAuth: func(context.Context) (bool, error) { return true, nil },
	}

	resp, err := s.CheckLoginStatus(context.Background())
	require.NoError(t, err)
	require.True(t, resp.IsLoggedIn)
}

// TestCheckLoginStatusLiveAuthLost: 캐시 LoggedIn=true 여도 live 페이지 재검증에서
// 미인증(false) 이면 세션을 invalidate(Logout) 하고 false 를 반환한다.
// XHS 가 검색/유휴 중 세션을 무효화해도 status API 가 stale true 를 주지 않게 하는 회귀.
func TestCheckLoginStatusLiveAuthLost(t *testing.T) {
	fs := &fakeSession{loggedIn: true}
	purgeCalls := 0
	s := &XiaohongshuService{
		session:       fs,
		liveAuth:      func(context.Context) (bool, error) { return false, nil },
		deleteAuthNow: func() error { purgeCalls++; return nil },
	}

	resp, err := s.CheckLoginStatus(context.Background())
	require.NoError(t, err)
	require.False(t, resp.IsLoggedIn, "live auth loss → false")
	require.GreaterOrEqual(t, fs.logoutCalls, 1, "세션 invalidate(Logout) 호출")
	require.Equal(t, 1, purgeCalls, "저장 인증 파일도 함께 정리")
}

// TestCheckLoginStatusTransientErrorKeepsSession: liveAuth 가 일시적 에러(네트워크 등)를
// 반환하면 단절 확정이 아니므로 세션을 유지한 채 false 만 반환한다(Logout 미호출).
func TestCheckLoginStatusTransientErrorKeepsSession(t *testing.T) {
	fs := &fakeSession{loggedIn: true}
	s := &XiaohongshuService{
		session:  fs,
		liveAuth: func(context.Context) (bool, error) { return false, errors.New("network blip") },
	}

	resp, err := s.CheckLoginStatus(context.Background())
	require.NoError(t, err)
	require.False(t, resp.IsLoggedIn)
	require.Equal(t, 0, fs.logoutCalls, "일시적 에러는 세션 유지(Logout 금지)")
}

// TestSearchFeedsPropagatesLiveError: live 페이지에서 난 에러(ErrNoSession/ErrAuthLost 아님)는
// fallback 하지 않고 그대로 반환한다. dispatch 조건 회귀 방지.
// (ErrNoSession → fallback 경로는 실제 브라우저가 필요해 단위 테스트 범위가 아니다.)
func TestSearchFeedsPropagatesLiveError(t *testing.T) {
	sentinel := errors.New("live search boom")
	s := &XiaohongshuService{session: &fakeSession{withPageErr: sentinel}}

	_, err := s.SearchFeeds(context.Background(), "fan")
	require.ErrorIs(t, err, sentinel)
}

// TestSearchFeedsAuthLostPurgesSession: ErrAuthLost 수신 시 live 세션을 invalidate 하고
// 인증 파일 정리를 호출한 뒤 ErrAuthLost 를 그대로 반환(generic fallback 재시도 금지).
// deleteAuthNow stub 으로 실제 저장 파일은 보호한다.
func TestSearchFeedsAuthLostPurgesSession(t *testing.T) {
	fs := &fakeSession{withPageErr: xhserrors.ErrAuthLost}
	purgeCalls := 0
	s := &XiaohongshuService{
		session:       fs,
		deleteAuthNow: func() error { purgeCalls++; return nil },
	}

	_, err := s.SearchFeeds(context.Background(), "fan")
	require.ErrorIs(t, err, xhserrors.ErrAuthLost, "ErrAuthLost 를 그대로 반환")
	require.GreaterOrEqual(t, fs.logoutCalls, 1, "live 세션 invalidate(Logout)")
	require.GreaterOrEqual(t, purgeCalls, 1, "저장 인증 파일 정리 호출")
	// fallback 미실행: 반환값이 ErrAuthLost(브라우저 fallback 결과가 아님).
	require.NotErrorIs(t, err, errors.New("fallback"))
}

// TestDeleteAuthFiles: 명시적 path 로 cookie/localStorage 파일을 모두 삭제한다.
// capabilities 가 즉시 available=false 가 되도록 하는 파일 정리 로직(임시 path 격리).
func TestDeleteAuthFiles(t *testing.T) {
	dir := t.TempDir()
	cookiePath := filepath.Join(dir, "cookies.json")
	lsPath := filepath.Join(dir, "local_storage.json")
	require.NoError(t, os.WriteFile(cookiePath, []byte(`{"id_token":"x"}`), 0o600))
	require.NoError(t, os.WriteFile(lsPath, []byte(`[]`), 0o600))

	require.NoError(t, deleteAuthFiles(cookiePath, lsPath))

	require.NoFileExists(t, cookiePath, "cookie 파일 삭제")
	require.NoFileExists(t, lsPath, "localStorage 파일 삭제")
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
