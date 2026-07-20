// Package xhssession: QR 로그인 브라우저를 유지·재사용하는 동시성 안전 세션 매니저.
// 설계: 상태 머신(state/TTL/gen)은 순수 로직으로 분리해 fake 로 TDD.
// BrowserSession 인터페이스로 실제 rod 구현과 분리한다.
//
// 라이프사이클: 중복 QR 교체, logout/shutdown close, TTL 만료,
// panic/context-cancel 복구, mutex 직렬화를 모두 지원한다.
package xhssession

import (
	"context"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/pkg/errors"
)

// State 세션 상태.
type State int

const (
	StateIdle State = iota
	StateQRPending
	StateLoggedIn
)

// ErrNoSession: 사용 가능한 인증 세션이 없음(재사용 불가).
var ErrNoSession = errors.New("no active authenticated session")

// BrowserSession: 매니저가 사용하는 브라우저 세션 추상(테스트용 fake 교체 가능).
type BrowserSession interface {
	Start(ctx context.Context) error
	FetchQrcode(ctx context.Context) (img string, alreadyLoggedIn bool, err error)
	ConfirmLogin(ctx context.Context) (confirmed bool, err error)
	Page() *rod.Page
	Close() error
}

// Manager: 단일 live 세션을 보유하며 동시성 안전하게 재사용시킨다.
type Manager struct {
	mu          sync.Mutex // state/bs/page/confirmedAt/loginCancel/gen 보호
	startMu     sync.Mutex // StartLogin 의 Start/Fetch 단계 직렬화(중복 설치/누수 방지)
	state       State
	bs          BrowserSession
	page        *rod.Page
	confirmedAt time.Time
	loginCancel context.CancelFunc
	gen         int64 // QR 교체 시 구분용 세대(구 goroutine 무효화)

	newBS     func() BrowserSession
	ttl       time.Duration
	loginWait time.Duration
	pollEvery time.Duration
	now       func() time.Time
	onConfirm func(*rod.Page) // 로그인 확정 후 fallback 영속화 콜백
}

// Option Manager 설정자.
type Option func(*Manager)

// WithTTL 로그인 세션 유효시간(경과 시 재로그인 필요).
func WithTTL(d time.Duration) Option { return func(m *Manager) { m.ttl = d } }

// WithLoginWait QR 스캔 대기 최대 시간.
func WithLoginWait(d time.Duration) Option { return func(m *Manager) { m.loginWait = d } }

// WithPollEvery 로그인 확인 폴링 주기.
func WithPollEvery(d time.Duration) Option { return func(m *Manager) { m.pollEvery = d } }

// WithClock 주입 가능한 시계(TTL 테스트용).
func WithClock(now func() time.Time) Option { return func(m *Manager) { m.now = now } }

// WithOnConfirm 로그인 확정 직후 호출되는 콜백(fallback cookies/localStorage 영속화).
func WithOnConfirm(fn func(*rod.Page)) Option { return func(m *Manager) { m.onConfirm = fn } }

// NewManager: newBS 팩토리로 세션을 생성하는 매니저.
func NewManager(newBS func() BrowserSession, opts ...Option) *Manager {
	m := &Manager{
		newBS:     newBS,
		ttl:       30 * time.Minute,
		loginWait: 4 * time.Minute,
		pollEvery: 500 * time.Millisecond,
		now:       time.Now,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// State 현재 세션 상태(스냅샷).
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// LoggedIn: 재사용 가능한 인증 세션이 있는지. TTL 만료 시 즉시 invalidate/Close
// 해 브라우저 누수를 막는다(단순 false 반환만 하면 상태 확인 반복 시 만료 세션이 쌓임).
func (m *Manager) LoggedIn() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == StateLoggedIn && m.expiredLocked() {
		m.invalidateLocked()
		return false
	}
	return m.reusableLocked()
}

// StartLogin: 기존 세션을 교체하고 새 QR 세션을 시작한다. QR 만료/재시도 시 반복 호출.
// already(이미 로그인) 신호는 응답 힌트로만 쓰고, LoggedIn 전이는 항상
// waitForLogin 의 robust ConfirmLogin 을 통과해야 한다(weak already 거부).
//
// 동시성: startMu 로 Start/Fetch 단계를 직렬화하고, 설치 시점에 generation 을
// 검증해 도중 Logout/다른 무효화가 있으면 늦게 도착한 세션을 반드시 Close 한다.
// 따라서 중복 StartLogin/Logout 경합에서 오직 유효한 한 세션만 설치된다(나머지 Close).
//
// 패닉 안전: recover 를 newBS 호출보다 먼저 설치한다. newBS/Start/Fetch 패닉 시
// 로컬 bs 를 닫고 매니저 세션을 정리한 뒤 error 로 반환(HTTP 프로세스 보호).
// 패닉 발생 지점은 모두 mu 바깥이므로 recover 의 mu 재획득 데드락이 없다.
func (m *Manager) StartLogin(ctx context.Context) (img string, already bool, err error) {
	var bs BrowserSession
	handedOver := false
	defer func() {
		if r := recover(); r != nil {
			if !handedOver && bs != nil {
				// 매니저 인계 전 패닉: 로컬 bs 정리. Close 자체 패닉 시 재패닉으로
				// 프로세스가 죽으므로 panic-safe 헬퍼로 통일.
				closeBSPanicSafe(bs)
			}
			m.mu.Lock()
			m.invalidateLocked() // 인계 후 패닉: 매니저 세션 정리(panic-safe)
			m.mu.Unlock()
			err = errors.Errorf("start login panic recovered: %v", r)
		}
	}()

	// startMu 로 StartLogin 끼리 직렬화(동시 브라우저 기동/중복 설치 방지).
	m.startMu.Lock()
	defer m.startMu.Unlock()

	bs = m.newBS() // recover 가 이미 설치됐으므로 패닉해도 안전

	m.mu.Lock()
	m.invalidateLocked() // 중복 QR 교체: 기존 세션/진행 중 로그인 정리
	installGen := m.gen  // 이 세대가 유지돼야만 설치 허용
	m.mu.Unlock()

	if err = bs.Start(ctx); err != nil {
		closeBSPanicSafe(bs)
		return "", false, errors.Wrap(err, "start browser session")
	}
	img, already, err = bs.FetchQrcode(ctx)
	if err != nil {
		closeBSPanicSafe(bs)
		return "", false, errors.Wrap(err, "fetch qrcode")
	}

	// page 획득은 mu 바깥에서(rod 호출이 lock 구간에 들어가 패닉 시 데드락 방지).
	page := bs.Page()

	m.mu.Lock()
	// generation 검증: Start/Fetch 중 Logout 등이 무효화했으면 이 세션은 폐기.
	if m.gen != installGen {
		m.mu.Unlock()
		closeBSPanicSafe(bs)
		return "", false, errors.New("start login superseded by logout or replace")
	}
	m.bs = bs
	m.page = page
	handedOver = true
	// already 여부와 무관하게 항상 QRPending + waitForLogin 로 robust 확인한다.
	// weak already(loggedIn 만 true)는 ConfirmLogin 가 거부해 LoggedIn 전이/영속화 안 함.
	m.state = StateQRPending
	lctx, cancel := context.WithTimeout(context.Background(), m.loginWait)
	m.loginCancel = cancel
	m.mu.Unlock()

	go m.waitForLogin(lctx, installGen)
	return img, already, nil
}

// waitForLogin: QR 스캔 완료까지 ConfirmLogin 폴링. 패닉/ctx 복구 포함.
func (m *Manager) waitForLogin(ctx context.Context, myGen int64) {
	defer func() {
		if r := recover(); r != nil {
			// 패닉 복구: 내 세션이면 무효화(프로세스 보호)
			m.mu.Lock()
			if m.gen == myGen {
				m.invalidateLocked()
			}
			m.mu.Unlock()
		}
	}()

	ticker := time.NewTicker(m.pollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			m.mu.Lock()
			if m.gen == myGen && m.state == StateQRPending {
				m.invalidateLocked() // 타임아웃: 미승인 세션 정리
			}
			m.mu.Unlock()
			return
		case <-ticker.C:
		}

		m.mu.Lock()
		bs := m.bs
		stillMine := m.gen == myGen && m.state == StateQRPending
		m.mu.Unlock()
		if !stillMine || bs == nil {
			return // 도중 교체/로그아웃 됨
		}

		confirmed, err := bs.ConfirmLogin(ctx) // 브라우저 I/O 는 lock 외부
		if err != nil {
			continue // 일시적 에러/ctx 취소 시 다음 폴링 또는 종료
		}
		if !confirmed {
			continue
		}

		m.mu.Lock()
		// 여전히 내 세션이면 로그인 확정
		if m.gen != myGen || m.state != StateQRPending {
			m.mu.Unlock()
			return
		}
		m.state = StateLoggedIn
		m.confirmedAt = m.now()
		var onConfirm func(*rod.Page)
		var page *rod.Page
		if m.onConfirm != nil {
			onConfirm = m.onConfirm
			page = m.page
		}
		m.mu.Unlock()

		if onConfirm != nil {
			onConfirm(page) // fallback 영속화(restart 복원용 보조)
		}
		return
	}
}

// WithPage: 인증된 live 페이지에서 fn 을 실행(재사용). 접근은 직렬화된다.
// 세션이 없거나 만료면 ErrNoSession 반환(호출자가 fallback 처리).
// fn 내 패닉은 복구 후 세션 무효화.
func (m *Manager) WithPage(ctx context.Context, fn func(*rod.Page) error) error {
	m.mu.Lock()
	if m.state == StateLoggedIn && m.expiredLocked() {
		m.invalidateLocked() // TTL 만료 정리
	}
	if !m.reusableLocked() {
		m.mu.Unlock()
		return ErrNoSession
	}

	var fnErr error
	func() {
		defer m.mu.Unlock()
		defer func() {
			if r := recover(); r != nil {
				m.invalidateLocked()
				fnErr = errors.Errorf("page operation panic recovered: %v", r)
			}
		}()
		fnErr = fn(m.page)
	}()
	return fnErr
}

// Logout: 세션 종료(브라우저 닫기 + 상태 초기화).
func (m *Manager) Logout() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.invalidateLocked()
	return nil
}

// Close: 앱 종료 시 정리.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.invalidateLocked()
	return nil
}

// reusableLocked: 호출자가 mu 를 잡고 있어야 함. 재사용 가능 여부.
func (m *Manager) reusableLocked() bool {
	if m.state != StateLoggedIn || m.page == nil {
		return false
	}
	if m.ttl > 0 && !m.confirmedAt.IsZero() && m.expiredLocked() {
		return false
	}
	return true
}

// expiredLocked: TTL 경과 여부.
func (m *Manager) expiredLocked() bool {
	if m.ttl <= 0 || m.confirmedAt.IsZero() {
		return false
	}
	return m.now().Sub(m.confirmedAt) > m.ttl
}

// invalidateLocked: 현재 세션/진행 중 로그인을 모두 정리(호출자가 mu 잡고 있어야 함).
// bs.Close() 는 panic-safe 하게 호출해 상태 초기화가 항상 완료되도록 한다.
// 그래야 호출자의 mu 해제가 빠짐없이 실행되고, 바깥 recover 가 같은 mu 를
// 재획득하며 데드락하는 일이 없다.
func (m *Manager) invalidateLocked() {
	if m.loginCancel != nil {
		m.loginCancel()
		m.loginCancel = nil
	}
	if m.bs != nil {
		closeBSPanicSafe(m.bs)
		m.bs = nil
	}
	m.page = nil
	m.state = StateIdle
	m.confirmedAt = time.Time{}
	m.gen++ // 구 goroutine 이 무효화 감지
}

// closeBSPanicSafe: bs.Close() 패닉을 복구해 무시한다.
// invalidateLocked 가 mu 보유 중 호출되므로 Close 패닉이 전파되면 호출자의 mu 해제가
// 누락되고 바깥 recover 의 mu 재획득이 데드락한다. 패닉을 삼켜 상태 초기화를 보장한다.
func closeBSPanicSafe(bs BrowserSession) {
	defer func() { _ = recover() }()
	_ = bs.Close()
}
