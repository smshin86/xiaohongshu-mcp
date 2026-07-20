package xiaohongshu

import (
	"context"
	"encoding/json"
	"time"

	"github.com/go-rod/rod"
	"github.com/pkg/errors"
)

type LoginAction struct {
	page *rod.Page
}

func NewLogin(page *rod.Page) *LoginAction {
	return &LoginAction{page: page}
}

// navigateExplore 上下文安全地导航到首页并等待 __INITIAL_STATE__ 就绪。
// 用 bounded Wait 取代固定 Sleep, SPA 状态就绪即返回, 避免无谓等待或永久阻塞。
func navigateExplore(pp *rod.Page) error {
	if err := pp.Navigate("https://www.xiaohongshu.com/explore"); err != nil {
		return errors.Wrap(err, "navigate explore failed")
	}
	if err := pp.WaitLoad(); err != nil {
		return errors.Wrap(err, "wait explore load failed")
	}
	// __INITIAL_STATE__ 出现即认为首屏可读; 给一个上限避免卡死。
	if err := pp.Timeout(10 * time.Second).Wait(
		rod.Eval(`() => window.__INITIAL_STATE__ !== undefined && window.__INITIAL_STATE__.user !== undefined`)); err != nil {
		return errors.Wrap(err, "wait initial state failed")
	}
	return nil
}

// isLoggedIn 读取页面真实登录态: __INITIAL_STATE__.user.loggedIn 是 Vue3 ref,
// 需取 .value 解包。比 CSS 选择子(.main-container .user ...)更稳定, 不随样式重构失效。
func isLoggedIn(pp *rod.Page) (bool, error) {
	res, err := pp.Eval(`() => {
		try {
			const u = window.__INITIAL_STATE__ && window.__INITIAL_STATE__.user;
			if (!u) return false;
			const l = u.loggedIn;
			const v = (l && l.value !== undefined) ? l.value : l;
			return v === true;
		} catch (e) {
			return false;
		}
	}`)
	if err != nil {
		return false, errors.Wrap(err, "eval login state failed")
	}
	return res.Value.Bool(), nil
}

// authCheckJS: 페이지 인증 상태를 boolean 플래그로만 반환(값/PII 미출력).
// loggedIn 과 식별 정보(nickname/userId) 를 분리해 Go 쪽에서 조합한다.
// 단일 loggedIn 만 보면 QR 중간 승인 상태를 성공으로 오판(false-positive)하므로,
// 식별 정보 충족 여부를 Go 의 테스트 가능한 로직으로 판정한다.
const authCheckJS = `() => {
	try {
		const u = window.__INITIAL_STATE__ && window.__INITIAL_STATE__.user;
		if (!u) return JSON.stringify({loggedIn:false, hasIdentity:false});
		const l = u.loggedIn;
		const lv = (l && l.value !== undefined) ? l.value : l;
		const loggedIn = (lv === true);
		const ui = u.userInfo;
		const uiv = (ui && ui.value !== undefined) ? ui.value : ui;
		const nick = uiv && uiv.nickname;
		const uid = uiv && (uiv.userid || uiv.userId);
		const hasNick = (typeof nick === "string" && nick.length > 0);
		const hasUid = (typeof uid === "string" && uid.length > 0) || (typeof uid === "number" && uid > 0);
		return JSON.stringify({loggedIn: loggedIn, hasIdentity: hasNick || hasUid});
	} catch (e) {
		return JSON.stringify({loggedIn:false, hasIdentity:false});
	}
}`

// authState: authCheckJS 결과(boolean 플래그만, PII 없음).
type authState struct {
	LoggedIn    bool `json:"loggedIn"`
	HasIdentity bool `json:"hasIdentity"`
}

// parseAuthState: authCheckJS 결과 JSON 파싱. 잘못된/빈 값 → zero state.
func parseAuthState(s string) authState {
	var st authState
	if err := json.Unmarshal([]byte(s), &st); err != nil {
		return authState{}
	}
	return st
}

// isAuthStateComplete: loggedIn AND 식별 정보가 모두 충족된 robust 인증 상태.
// loggedIn 만 true 이고 식별 정보가 없으면(QR 중간 승인) false → 미인증 처리.
func isAuthStateComplete(st authState) bool {
	return st.LoggedIn && st.HasIdentity
}

// authCheck: 단일 시점 robust 인증 판정(loggedIn + 식별).
func authCheck(pp *rod.Page) (bool, error) {
	res, err := pp.Eval(authCheckJS)
	if err != nil {
		return false, errors.Wrap(err, "eval auth check failed")
	}
	return isAuthStateComplete(parseAuthState(res.Value.String())), nil
}

// IsAuthenticated: loggedIn + 사용자 식별 충족 시 true.
// false-positive 방지를 위해 안정화(stabilize) 후 재확인 한다:
// QR 중간 상태가 잠시 loggedIn=true 로 보일 수 있어 일정 시간 후 재판정.
func (a *LoginAction) IsAuthenticated(ctx context.Context) (bool, error) {
	pp := a.page.Context(ctx)

	ok, err := authCheck(pp)
	if err != nil || !ok {
		return false, err
	}

	// 안정화 대기 후 재확인(ctx 취소 시 즉시 반환).
	timer := time.NewTimer(1500 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-timer.C:
	}
	return authCheck(pp)
}

// CheckLoginStatus: 탐색(explore) 완료 후 robust 인증 판정으로 통일한다.
// 단일 loggedIn 값이 아닌 loggedIn + 식별 + 안정화 재확인(IsAuthenticated)을 사용해
// QR 중간 상태를 로그인으로 오판하지 않는다(fallback 경로 포함).
func (a *LoginAction) CheckLoginStatus(ctx context.Context) (bool, error) {
	pp := a.page.Context(ctx)

	if err := navigateExplore(pp); err != nil {
		return false, errors.Wrap(err, "check login status failed")
	}

	return a.IsAuthenticated(ctx)
}

func (a *LoginAction) Login(ctx context.Context) error {
	pp := a.page.Context(ctx)

	if err := navigateExplore(pp); err != nil {
		return errors.Wrap(err, "login navigate failed")
	}

	// 已经登录则直接返回
	if loggedIn, _ := isLoggedIn(pp); loggedIn {
		return nil
	}

	// 否则等待扫码登录成功(状态翻转); 由调用方 ctx 控制上限, ctx 取消即返回。
	_ = pp.Wait(rod.Eval(`() => {
		try {
			const u = window.__INITIAL_STATE__ && window.__INITIAL_STATE__.user;
			if (!u) return false;
			const l = u.loggedIn;
			const v = (l && l.value !== undefined) ? l.value : l;
			return v === true;
		} catch (e) { return false; }
	}`))
	return nil
}

func (a *LoginAction) FetchQrcodeImage(ctx context.Context) (string, bool, error) {
	pp := a.page.Context(ctx)

	if err := navigateExplore(pp); err != nil {
		return "", false, errors.Wrap(err, "fetch qrcode navigate failed")
	}

	// 이미 로그인됐는지 robust 하게 확인(단일 loggedIn 값 사용 금지).
	// loggedIn + 식별 + 안정화 재확인을 통과한 경우만 already=true.
	authed, err := a.IsAuthenticated(ctx)
	if err != nil {
		return "", false, errors.Wrap(err, "check already-authenticated failed")
	}
	if authed {
		return "", true, nil
	}

	// 获取二维码图片(登录态下不存在此元素). Must* 패닉 대신 에러 반환 API 사용.
	el, err := pp.Element(".login-container .qrcode-img")
	if err != nil {
		return "", false, errors.Wrap(err, "find qrcode img failed")
	}
	src, err := el.Attribute("src")
	if err != nil {
		return "", false, errors.Wrap(err, "get qrcode src failed")
	}
	if src == nil || len(*src) == 0 {
		return "", false, errors.New("qrcode src is empty")
	}

	return *src, false, nil
}

// WaitForLogin 轮询真实登录态, 取代失效的 CSS 选择子轮询。
func (a *LoginAction) WaitForLogin(ctx context.Context) bool {
	pp := a.page.Context(ctx)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			if loggedIn, _ := isLoggedIn(pp); loggedIn {
				return true
			}
		}
	}
}
