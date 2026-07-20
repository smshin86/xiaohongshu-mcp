package main

import (
	"context"

	"github.com/go-rod/rod"
	"github.com/xpzouying/headless_browser"
	"github.com/xpzouying/xiaohongshu-mcp/xhssession"
	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

// xhsBrowserSession: xhssession.BrowserSession 의 실제 rod 구현(direction #2).
// QR 생성에 사용한 동일 Browser/Page 를 유지해 CheckLoginStatus/SearchFeeds 가
// 동일 인증 세션을 재사용한다. 새 브라우저에서 cookie/localStorage replay 시
// anti-bot 에 차단되는 문제를 회피한다.
type xhsBrowserSession struct {
	b     *headless_browser.Browser
	page  *rod.Page
	login *xiaohongshu.LoginAction
}

func newXhsBrowserSession() *xhsBrowserSession {
	return &xhsBrowserSession{}
}

// Start: 브라우저 기동 + 페이지 생성. newBrowser 는 저장 쿠키(재시작 복원용)를
// 자동 로드한다. ctx 가 이미 취소됐으면 시작하지 않는다(context-safe).
func (s *xhsBrowserSession) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.b = newBrowser()
	s.page = s.b.NewPage()
	s.login = xiaohongshu.NewLogin(s.page)
	return nil
}

// FetchQrcode: explore 진입 후 QR 이미지 반환. 이미 로그인 상태면 already=true.
func (s *xhsBrowserSession) FetchQrcode(ctx context.Context) (string, bool, error) {
	return s.login.FetchQrcodeImage(ctx)
}

// ConfirmLogin: loggedIn + 사용자 식별 + 안정화 재확인으로 false-positive 차단.
func (s *xhsBrowserSession) ConfirmLogin(ctx context.Context) (bool, error) {
	return s.login.IsAuthenticated(ctx)
}

// Page: 재사용할 live 페이지.
func (s *xhsBrowserSession) Page() *rod.Page { return s.page }

// Close: 브라우저 종료(로그아웃/TTL/교체/앱 종료 시 호출).
func (s *xhsBrowserSession) Close() error {
	if s.b != nil {
		s.b.Close()
	}
	return nil
}

// 컴파일 타임 인터페이스 충족 보장.
var _ xhssession.BrowserSession = (*xhsBrowserSession)(nil)
