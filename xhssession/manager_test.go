package xhssession

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/stretchr/testify/require"
)

// fakeBS: BrowserSession 테스트용 가짜. 값 검증은 하지 않는다.
type fakeBS struct {
	startErr       error
	fetchImg       string
	fetchAlready   bool
	fetchErr       error
	confirmOk      bool
	confirmErr     error
	confirmDelay   time.Duration
	panicOnStart   bool
	panicOnFetch   bool
	panicOnConfirm int
	startCheckCtx  bool // Start 가 ctx 를 검사할지

	mu           sync.Mutex
	started      bool
	closed       int
	confirmCalls int
}

func (f *fakeBS) Start(ctx context.Context) error {
	if f.panicOnStart {
		panic("fake start panic")
	}
	if f.startCheckCtx && ctx.Err() != nil {
		return ctx.Err()
	}
	f.mu.Lock()
	f.started = true
	f.mu.Unlock()
	return f.startErr
}

func (f *fakeBS) FetchQrcode(ctx context.Context) (string, bool, error) {
	if f.panicOnFetch {
		panic("fake fetch panic")
	}
	if ctx.Err() != nil {
		return "", false, ctx.Err()
	}
	return f.fetchImg, f.fetchAlready, f.fetchErr
}

func (f *fakeBS) ConfirmLogin(ctx context.Context) (bool, error) {
	f.mu.Lock()
	f.confirmCalls++
	n := f.confirmCalls
	f.mu.Unlock()

	if f.confirmDelay > 0 {
		select {
		case <-time.After(f.confirmDelay):
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	if f.panicOnConfirm > 0 && n == f.panicOnConfirm {
		panic("fake confirm panic")
	}
	if f.confirmErr != nil {
		return false, f.confirmErr
	}
	return f.confirmOk, nil
}

// 페이지 포인터만 필요(메서드 호출 없음). zero rod.Page 로 대체.
var fakeRodPage = &rod.Page{}

func (f *fakeBS) Page() *rod.Page { return fakeRodPage }

func (f *fakeBS) Close() error {
	f.mu.Lock()
	f.closed++
	f.mu.Unlock()
	return nil
}

func (f *fakeBS) closeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// fakeClock: 주입 가능한 시계(TTL 테스트용).
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestManager(t *testing.T, bsFactory func() BrowserSession, clk *fakeClock) *Manager {
	t.Helper()
	if clk == nil {
		clk = &fakeClock{t: time.Unix(1700000000, 0)}
	}
	return NewManager(bsFactory,
		WithClock(clk.now),
		WithPollEvery(5*time.Millisecond),
		WithLoginWait(200*time.Millisecond),
		WithTTL(time.Minute),
	)
}

// TestFreshManagerNoSession: 새 매니저는 세션 없음.
func TestFreshManagerNoSession(t *testing.T) {
	m := newTestManager(t, func() BrowserSession { return &fakeBS{} }, nil)
	require.Equal(t, StateIdle, m.State())
	require.False(t, m.LoggedIn())

	var ran bool
	err := m.WithPage(context.Background(), func(*rod.Page) error { ran = true; return nil })
	require.True(t, errors.Is(err, ErrNoSession))
	require.False(t, ran)
}

// TestStartLoginReturnsQR: QR 요청 시 이미지 반환 + QRPending 상태.
func TestStartLoginReturnsQR(t *testing.T) {
	bs := &fakeBS{fetchImg: "data:qr", confirmOk: false, confirmDelay: time.Hour}
	m := newTestManager(t, func() BrowserSession { return bs }, nil)

	img, already, err := m.StartLogin(context.Background())
	require.NoError(t, err)
	require.Equal(t, "data:qr", img)
	require.False(t, already)
	require.Equal(t, StateQRPending, m.State())
}

// TestConfirmTransitionsToLoggedIn: ConfirmLogin 참 → LoggedIn 전이 + WithPage 재사용.
func TestConfirmTransitionsToLoggedIn(t *testing.T) {
	bs := &fakeBS{fetchImg: "qr", confirmOk: true}
	m := newTestManager(t, func() BrowserSession { return bs }, nil)

	_, _, err := m.StartLogin(context.Background())
	require.NoError(t, err)

	require.Eventually(t, func() bool { return m.LoggedIn() }, time.Second, 5*time.Millisecond)

	var ran bool
	err = m.WithPage(context.Background(), func(*rod.Page) error { ran = true; return nil })
	require.NoError(t, err)
	require.True(t, ran)
}

// TestTTLExpiry: TTL 경과 후 세션 만료 → WithPage 거부 + 브라우저 종료.
func TestTTLExpiry(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1700000000, 0)}
	bs := &fakeBS{fetchImg: "qr", confirmOk: true}
	m := newTestManager(t, func() BrowserSession { return bs }, clk)

	_, _, _ = m.StartLogin(context.Background())
	require.Eventually(t, func() bool { return m.LoggedIn() }, time.Second, 5*time.Millisecond)

	// TTL 이후로 시계 전진
	clk.advance(2 * time.Minute)
	require.False(t, m.LoggedIn())

	err := m.WithPage(context.Background(), func(*rod.Page) error { return nil })
	require.True(t, errors.Is(err, ErrNoSession))
	require.GreaterOrEqual(t, bs.closeCount(), 1, "expired session must be closed")
}

// TestQRReplacesPendingSession: 진행 중 QR 재요청 시 기존 세션 종료 후 새 세션.
func TestQRReplacesPendingSession(t *testing.T) {
	first := &fakeBS{fetchImg: "qr1", confirmOk: false, confirmDelay: time.Hour}
	var current *fakeBS
	m := newTestManager(t, func() BrowserSession {
		current = &fakeBS{fetchImg: "qr2", confirmOk: false, confirmDelay: time.Hour}
		if !first.started {
			return first
		}
		return current
	}, nil)

	_, _, err := m.StartLogin(context.Background())
	require.NoError(t, err)
	require.Equal(t, StateQRPending, m.State())

	img2, _, err := m.StartLogin(context.Background())
	require.NoError(t, err)
	require.Equal(t, "qr2", img2)

	require.GreaterOrEqual(t, first.closeCount(), 1, "old pending session must be replaced/closed")
}

// TestLogoutClosesSession: 로그아웃 시 세션 종료.
func TestLogoutClosesSession(t *testing.T) {
	bs := &fakeBS{fetchImg: "qr", confirmOk: true}
	m := newTestManager(t, func() BrowserSession { return bs }, nil)

	_, _, _ = m.StartLogin(context.Background())
	require.Eventually(t, func() bool { return m.LoggedIn() }, time.Second, 5*time.Millisecond)

	require.NoError(t, m.Logout())
	require.False(t, m.LoggedIn())
	require.GreaterOrEqual(t, bs.closeCount(), 1)

	err := m.WithPage(context.Background(), func(*rod.Page) error { return nil })
	require.True(t, errors.Is(err, ErrNoSession))
}

// TestConfirmPanicRecovered: ConfirmLogin 패닉 시 복구 + 세션 무효화(프로세스 보호).
func TestConfirmPanicRecovered(t *testing.T) {
	bs := &fakeBS{fetchImg: "qr", confirmOk: false, panicOnConfirm: 1}
	m := newTestManager(t, func() BrowserSession { return bs }, nil)

	_, _, err := m.StartLogin(context.Background())
	require.NoError(t, err)

	require.Eventually(t, func() bool { return bs.closeCount() >= 1 }, time.Second, 5*time.Millisecond)
	require.False(t, m.LoggedIn(), "session must be invalidated after panic")
	require.Equal(t, StateIdle, m.State())
}

// TestStartLoginContextCancel: 취소된 ctx 로 시작하면 에러.
func TestStartLoginContextCancel(t *testing.T) {
	bs := &fakeBS{fetchImg: "qr", startCheckCtx: true}
	m := newTestManager(t, func() BrowserSession { return bs }, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := m.StartLogin(ctx)
	require.Error(t, err)
}

// TestOnConfirmCallback: 로그인 확정 시 fallback 영속화 콜백 호출.
func TestOnConfirmCallback(t *testing.T) {
	bs := &fakeBS{fetchImg: "qr", confirmOk: true}
	var called int32
	m := NewManager(func() BrowserSession { return bs },
		WithClock((&fakeClock{t: time.Unix(1700000000, 0)}).now),
		WithPollEvery(5*time.Millisecond),
		WithLoginWait(200*time.Millisecond),
		WithTTL(time.Minute),
		WithOnConfirm(func(*rod.Page) { atomic.AddInt32(&called, 1) }),
	)
	_, _, _ = m.StartLogin(context.Background())
	require.Eventually(t, func() bool { return atomic.LoadInt32(&called) == 1 }, time.Second, 5*time.Millisecond)
}

// TestConcurrentAccessNoRace: 동시 접근에도 데이터레이스/패닉 없음(-race 로 검증).
func TestConcurrentAccessNoRace(t *testing.T) {
	bs := &fakeBS{fetchImg: "qr", confirmOk: true}
	m := newTestManager(t, func() BrowserSession { return &fakeBS{fetchImg: "qr", confirmOk: true, confirmDelay: time.Hour} }, nil)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.WithPage(context.Background(), func(*rod.Page) error { return nil })
			_ = m.LoggedIn()
			_ = m.State()
		}()
	}
	// 도중 로그인/로그아웃 혼합
	wg.Add(2)
	go func() { defer wg.Done(); _, _, _ = m.StartLogin(context.Background()) }()
	go func() { defer wg.Done(); time.Sleep(10 * time.Millisecond); _ = m.Logout() }()

	wg.Wait()
	_ = bs
}

// TestWeakAlreadyRejected: FetchQrcode 가 already=true(weak 신호)를 반환해도
// ConfirmLogin(robust) 이 false 면 LoggedIn 전이/영속화 가 없어야 한다(#1 회귀).
func TestWeakAlreadyRejected(t *testing.T) {
	var persisted int32
	bs := &fakeBS{fetchAlready: true, confirmOk: false, confirmDelay: time.Hour}
	m := NewManager(func() BrowserSession { return bs },
		WithClock((&fakeClock{t: time.Unix(1700000000, 0)}).now),
		WithPollEvery(5*time.Millisecond),
		WithLoginWait(200*time.Millisecond),
		WithTTL(time.Minute),
		WithOnConfirm(func(*rod.Page) { atomic.AddInt32(&persisted, 1) }),
	)

	img, already, err := m.StartLogin(context.Background())
	require.NoError(t, err)
	require.True(t, already, "response hint may carry already")
	require.Empty(t, img)
	require.NotEqual(t, StateLoggedIn, m.State(), "must not transition to LoggedIn on weak already")
	require.False(t, m.LoggedIn())
	require.Equal(t, int32(0), atomic.LoadInt32(&persisted), "must not persist on weak already")
}

// TestLoggedInTTLClose: TTL 만료 시 LoggedIn 은 false 고 브라우저도 즉시 닫힌다(#3 회귀).
// 반복 조회해도 추가 close/누수 가 없어야 한다.
func TestLoggedInTTLClose(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1700000000, 0)}
	bs := &fakeBS{fetchImg: "qr", confirmOk: true}
	m := newTestManager(t, func() BrowserSession { return bs }, clk)

	_, _, _ = m.StartLogin(context.Background())
	require.Eventually(t, func() bool { return m.LoggedIn() }, time.Second, 5*time.Millisecond)

	clk.advance(2 * time.Minute) // TTL(1m) 경과
	require.False(t, m.LoggedIn(), "expired must report not logged in")
	require.GreaterOrEqual(t, bs.closeCount(), 1, "expired session must be closed immediately")

	// 반복 조회 → 이미 Idle 이므로 추가 close 없음(누수 아님).
	_ = m.LoggedIn()
	require.Equal(t, 1, bs.closeCount())
}

// TestStartLoginStartPanic: bs.Start 패닉 시 복구 → error 반환, LoggedIn/상태 정리(#4 회귀).
func TestStartLoginStartPanic(t *testing.T) {
	bs := &fakeBS{panicOnStart: true}
	m := newTestManager(t, func() BrowserSession { return bs }, nil)

	_, _, err := m.StartLogin(context.Background())
	require.Error(t, err)
	require.False(t, m.LoggedIn())
	require.Equal(t, StateIdle, m.State())
}

// TestStartLoginFetchPanic: bs.FetchQrcode 패닉 시 복구 → error 반환 + bs 정리(#4 회귀).
func TestStartLoginFetchPanic(t *testing.T) {
	bs := &fakeBS{fetchImg: "qr", panicOnFetch: true}
	m := newTestManager(t, func() BrowserSession { return bs }, nil)

	_, _, err := m.StartLogin(context.Background())
	require.Error(t, err)
	require.False(t, m.LoggedIn())
	require.Equal(t, StateIdle, m.State())
	require.GreaterOrEqual(t, bs.closeCount(), 1, "local bs must be closed after fetch panic")
}

// TestOnConfirmPanicRecovered: onConfirm 패닉 시 waitForLogin 복구 → 세션 무효화(#4 회귀).
func TestOnConfirmPanicRecovered(t *testing.T) {
	bs := &fakeBS{fetchImg: "qr", confirmOk: true}
	m := NewManager(func() BrowserSession { return bs },
		WithClock((&fakeClock{t: time.Unix(1700000000, 0)}).now),
		WithPollEvery(5*time.Millisecond),
		WithLoginWait(200*time.Millisecond),
		WithTTL(time.Minute),
		WithOnConfirm(func(*rod.Page) { panic("onConfirm boom") }),
	)

	_, _, _ = m.StartLogin(context.Background())
	require.Eventually(t, func() bool { return bs.closeCount() >= 1 }, time.Second, 5*time.Millisecond)
	require.False(t, m.LoggedIn(), "session must be invalidated after onConfirm panic")
	require.Equal(t, StateIdle, m.State())
}
