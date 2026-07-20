package browser

import (
	"net/url"
	"os"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/xpzouying/headless_browser"
	"github.com/xpzouying/xiaohongshu-mcp/cookies"
)

type browserConfig struct {
	binPath   string
	userAgent string
}

type Option func(*browserConfig)

func WithBinPath(binPath string) Option {
	return func(c *browserConfig) {
		c.binPath = binPath
	}
}

// WithUserAgent: XHS 전용 User-Agent 옵션. headless_browser 기본값(Chrome/124)을
// 실제 브라우저 빌드와 일치하는 값으로 덮어쓴다(자동화 탐지 완화 목적).
// 값은 NewBrowser 에서 검증(제어문자/CR/LF 거부, 길이 상한)한다.
// UA 값 자체는 비밀 취급해 로그에 출력하지 않는다(길이만 로그).
func WithUserAgent(userAgent string) Option {
	return func(c *browserConfig) {
		c.userAgent = userAgent
	}
}

// maxUserAgentLen: 허용 UA 최대 길이. 실사 UA 보다 넉넉한 상한(주입/오용 방지).
const maxUserAgentLen = 512

// validateUserAgent: UA 검증. 빈 값은 "미설정"으로 허용(기본 동작 호환).
// 제어문자(CR/LF 포함)를 거부해 런치 인자/헤더 주입을 막고, 길이 상한을 적용한다.
func validateUserAgent(ua string) error {
	if ua == "" {
		return nil
	}
	if len(ua) > maxUserAgentLen {
		return errors.Errorf("user agent too long: %d > %d", len(ua), maxUserAgentLen)
	}
	for _, r := range ua {
		if r < 0x20 || r == 0x7f {
			return errors.New("user agent must not contain control characters (CR/LF included)")
		}
	}
	return nil
}

// maskProxyCredentials masks username and password in proxy URL for safe logging.
func maskProxyCredentials(proxyURL string) string {
	u, err := url.Parse(proxyURL)
	if err != nil || u.User == nil {
		return proxyURL
	}
	if _, hasPassword := u.User.Password(); hasPassword {
		u.User = url.UserPassword("***", "***")
	} else {
		u.User = url.User("***")
	}
	return u.String()
}

func NewBrowser(headless bool, options ...Option) *headless_browser.Browser {
	cfg := &browserConfig{}
	for _, opt := range options {
		opt(cfg)
	}

	opts := []headless_browser.Option{
		headless_browser.WithHeadless(headless),
	}
	if cfg.binPath != "" {
		opts = append(opts, headless_browser.WithChromeBinPath(cfg.binPath))
	}

	// User-Agent 우선순위: 옵션 > XHS_USER_AGENT env > headless_browser 기본(Chrome/124).
	// 검증 실패 시 값 없이 무시(기본 동작 유지). UA 값은 로그하지 않고 길이만 로그.
	ua := cfg.userAgent
	if ua == "" {
		ua = os.Getenv("XHS_USER_AGENT")
	}
	if err := validateUserAgent(ua); err != nil {
		logrus.Warnf("ignoring invalid user agent: %v (len=%d)", err, len(ua))
	} else if ua != "" {
		opts = append(opts, headless_browser.WithUserAgent(ua))
		logrus.Infof("using custom user agent (len=%d)", len(ua))
	}

	// Read proxy from environment variable
	if proxy := os.Getenv("XHS_PROXY"); proxy != "" {
		opts = append(opts, headless_browser.WithProxy(proxy))
		logrus.Infof("Using proxy: %s", maskProxyCredentials(proxy))
	}

	// 加载 cookies
	cookiePath := cookies.GetCookiesFilePath()
	cookieLoader := cookies.NewLoadCookie(cookiePath)

	if data, err := cookieLoader.LoadCookies(); err == nil {
		opts = append(opts, headless_browser.WithCookies(string(data)))
		logrus.Debugf("loaded cookies from filesuccessfully")
	} else {
		logrus.Warnf("failed to load cookies: %v", err)
	}

	return headless_browser.New(opts...)
}
