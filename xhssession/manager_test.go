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
	startDelay     time.Duration
	panicOnStart   bool
	panicOnFetch   bool
	panicOnConfirm int
	panicOnClose   bool
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
	// Start 단계의 지연: StartLogin 들이 Start/Fetch 중 직렬화 없이 겹치는
	// 경합(중복 설치 / Logout 끼어듦)을 재현하기 위함. ctx 취소 시 즉시 반환.
	if f.startDelay > 0 {
		select {
		case <-time.After(f.startDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
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
	if f.panicOnClose {
		panic("fake close panic")
	}
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

// TestStartLoginNewPanic: newBS(팩토리) 패닉 시 recover 가 잡아 error 로 반환한다.
// recover 가 factory 호출보다 먼저 설치돼 있지 않으면 프로세스가 죽는다(3차 리뷰 #1 회귀).
func TestStartLoginNewPanic(t *testing.T) {
	m := newTestManager(t, func() BrowserSession { panic("factory boom") }, nil)

	_, _, err := m.StartLogin(context.Background())
	require.Error(t, err)
	require.False(t, m.LoggedIn())
	require.Equal(t, StateIdle, m.State())
}

// TestConcurrentStartLoginSingleSession: 지연된 동시 StartLogin 경합에서
// 오직 유효한 한 세션만 설치되고 나머지는 모두 Close 되야 한다(3차 리뷰 #2 회귀).
// -race 로 데이터레이스 없음을 함께 검증한다.
func TestConcurrentStartLoginSingleSession(t *testing.T) {
	var (
		allBS []*fakeBS
		bsMu  sync.Mutex
	)
	m := NewManager(func() BrowserSession {
		bs := &fakeBS{fetchImg: "qr", confirmOk: true, startDelay: 10 * time.Millisecond}
		bsMu.Lock()
		allBS = append(allBS, bs)
		bsMu.Unlock()
		return bs
	},
		WithClock((&fakeClock{t: time.Unix(1700000000, 0)}).now),
		WithPollEvery(5*time.Millisecond),
		WithLoginWait(time.Minute),
		WithTTL(time.Minute),
	)

	const n = 5
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// 진입 시점을 어긋나게 해 직렬화/세대 검증을 자극.
			time.Sleep(time.Duration(i) * 20 * time.Millisecond)
			_, _, _ = m.StartLogin(context.Background())
		}(i)
	}
	wg.Wait()

	// 최종 한 세션만 LoggedIn 으로 살아남는다.
	require.Eventually(t, func() bool { return m.State() == StateLoggedIn }, 2*time.Second, 5*time.Millisecond)

	bsMu.Lock()
	defer bsMu.Unlock()
	require.Len(t, allBS, n, "each StartLogin must create one session")
	alive := 0
	for _, b := range allBS {
		if b.closeCount() == 0 {
			alive++
		}
	}
	require.Equal(t, 1, alive, "exactly one session must survive; the rest must be closed")
}

// TestStartLoginDuringLogout: StartLogin 이 Start(지연) 단계에 있을 때 Logout 이
// 끼어들면 늦게 도착한 QR 세션은 설치되지 않고 반드시 Close 된다(3차 리뷰 #2 회귀).
// 상태는 Idle 로 귀결되고 생성된 모든 세션이 닫힌다(누수 없음).
func TestStartLoginDuringLogout(t *testing.T) {
	var (
		allBS []*fakeBS
		bsMu  sync.Mutex
	)
	m := NewManager(func() BrowserSession {
		bs := &fakeBS{fetchImg: "qr", confirmOk: false, confirmDelay: time.Hour, startDelay: 80 * time.Millisecond}
		bsMu.Lock()
		allBS = append(allBS, bs)
		bsMu.Unlock()
		return bs
	},
		WithClock((&fakeClock{t: time.Unix(1700000000, 0)}).now),
		WithPollEvery(5*time.Millisecond),
		WithLoginWait(time.Hour),
		WithTTL(time.Minute),
	)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _ = m.StartLogin(context.Background())
	}()

	// StartLogin 이 Start(지연) 중일 때 Logout 이 세대를 무효화한다.
	time.Sleep(20 * time.Millisecond)
	require.NoError(t, m.Logout())
	<-done

	require.Equal(t, StateIdle, m.State())
	bsMu.Lock()
	defer bsMu.Unlock()
	require.NotEmpty(t, allBS, "a session must have been created")
	for _, b := range allBS {
		require.GreaterOrEqual(t, b.closeCount(), 1, "every created session must be closed (no leak)")
	}
}

// TestStartLoginLocalClosePanicSafe: Start/Fetch 실패(panic 또는 error) 시 로컬 bs 정리에서
// Close 자체가 panic 해도 StartLogin 은 정상 error 반환 + State Idle 로 끝난다(최종 리뷰).
// 로컬 정리를 직접 bs.Close() 로 했다면 recover 중 재패닉으로 프로세스가 죽는다 →
// 모든 로컬 정리 경로(defer/Start/Fetch/superseded)는 closeBSPanicSafe 로 통일.
func TestStartLoginLocalClosePanicSafe(t *testing.T) {
	cases := []struct {
		name string
		bs   *fakeBS
	}{
		{"start error", &fakeBS{startErr: errors.New("start boom"), panicOnClose: true}},
		{"start panic", &fakeBS{panicOnStart: true, panicOnClose: true}},
		{"fetch error", &fakeBS{fetchImg: "qr", fetchErr: errors.New("fetch boom"), panicOnClose: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := newTestManager(t, func() BrowserSession { return c.bs }, nil)
			_, _, err := m.StartLogin(context.Background())
			require.Error(t, err)
			require.False(t, m.LoggedIn())
			require.Equal(t, StateIdle, m.State())
		})
	}
}
