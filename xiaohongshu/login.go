package xiaohongshu

import (
	"context"
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

func (a *LoginAction) CheckLoginStatus(ctx context.Context) (bool, error) {
	pp := a.page.Context(ctx)

	if err := navigateExplore(pp); err != nil {
		return false, errors.Wrap(err, "check login status failed")
	}

	loggedIn, err := isLoggedIn(pp)
	if err != nil {
		return false, errors.Wrap(err, "check login status failed")
	}
	return loggedIn, nil
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

	// 已经登录则无需二维码
	if loggedIn, _ := isLoggedIn(pp); loggedIn {
		return "", true, nil
	}

	// 获取二维码图片(登录态下不存在此元素)
	src, err := pp.MustElement(".login-container .qrcode-img").Attribute("src")
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
