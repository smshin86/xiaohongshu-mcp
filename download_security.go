package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// downloadResolver 抽象 DNS 解析,便于测试注入伪造解析器.
type downloadResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

// downloadURLGuard 为平台下载场景提供 SSRF 防护:
// 校验 URL 协议/主机后缀/DNS 结果,并在拨号阶段重新解析以闭合重绑定窗口.
type downloadURLGuard struct {
	resolver downloadResolver
	dialer   *net.Dialer
}

var platformAllowlist = map[string][]string{
	"xiaohongshu": {"xiaohongshu.com", "xhscdn.com", "xhslink.com"},
	"douyin":      {"douyin.com", "douyinvod.com", "douyinpic.com", "byteimg.com", "bytecdn.cn"},
	"tiktok":      {"tiktok.com", "tiktokcdn.com", "tiktokv.com", "byteoversea.com", "ibytedtos.com"},
}

const maxRedirects = 5

var (
	errUnsupportedPlatform = errors.New("unsupported platform")
	errInvalidURL          = errors.New("invalid url")
	errDisallowedHost      = errors.New("disallowed host")
	errPrivateAddress      = errors.New("private or reserved address")
	errTooManyRedirects    = errors.New("too many redirects")
	errRedirectLoop        = errors.New("redirect loop detected")
)

func newDownloadURLGuard() *downloadURLGuard {
	return newDownloadURLGuardWithResolver(net.DefaultResolver, &net.Dialer{Timeout: 30 * time.Second})
}

func newDownloadURLGuardWithResolver(r downloadResolver, dialer *net.Dialer) *downloadURLGuard {
	return &downloadURLGuard{resolver: r, dialer: dialer}
}

// Validate 按 platform 的允许名单校验 rawURL,并执行 DNS 私有地址过滤.
func (g *downloadURLGuard) Validate(ctx context.Context, platform, rawURL string) (*url.URL, error) {
	allow, ok := platformAllowlist[platform]
	if !ok {
		return nil, errUnsupportedPlatform
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("%w: parse failed", errInvalidURL)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("%w: scheme not https", errInvalidURL)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: userinfo not allowed", errInvalidURL)
	}
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("%w: empty host", errInvalidURL)
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("%w: invalid port", errInvalidURL)
		}
	}

	if !hostMatchesAny(host, allow) {
		return nil, errDisallowedHost
	}

	ips, err := g.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("%w: resolution failed", errInvalidURL)
	}
	for _, ip := range ips {
		if !isPublicIP(ip.IP) {
			return nil, errPrivateAddress
		}
	}
	return u, nil
}

func hostMatchesAny(host string, suffixes []string) bool {
	for _, s := range suffixes {
		if host == s || strings.HasSuffix(host, "."+s) {
			return true
		}
	}
	return false
}

// isPublicIP 判定 IP 是否为可对外访问的公网地址.
// IPv4-mapped IPv6 在 To4() 后归一化为 IPv4 再分类.
func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	return true
}

// NewClient 构造 *http.Client:无总超时,DialContext 重新解析并直连已验证公网 IP,
// CheckRedirect 限制 5 跳且对同 platform 名单重做全部校验.
func (g *downloadURLGuard) NewClient(platform string) *http.Client {
	tr := &http.Transport{
		ResponseHeaderTimeout: 30 * time.Second,
		DialContext:           g.dialContext,
	}
	return &http.Client{
		Transport:     tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return g.checkRedirect(platform, req, via) },
	}
}

// dialContext 重新解析主机名,任一 IP 命中私有/保留地址段即拒绝拨号.
func (g *downloadURLGuard) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid address", errInvalidURL)
	}
	resolved, err := g.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("%w: resolution failed", errInvalidURL)
	}
	var target net.IP
	for _, ipa := range resolved {
		if !isPublicIP(ipa.IP) {
			return nil, errPrivateAddress
		}
		if target == nil {
			target = ipa.IP
		}
	}
	if target == nil {
		return nil, errPrivateAddress
	}
	return g.dialer.DialContext(ctx, network, net.JoinHostPort(target.String(), port))
}

// checkRedirect 在每个跳转上重放 Validate,检测循环并限制最大 5 跳.
func (g *downloadURLGuard) checkRedirect(platform string, req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errTooManyRedirects
	}
	for _, prev := range via {
		if prev.URL.String() == req.URL.String() {
			return errRedirectLoop
		}
	}
	if _, err := g.Validate(req.Context(), platform, req.URL.String()); err != nil {
		return err
	}
	return nil
}
