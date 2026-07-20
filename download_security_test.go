package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeResolver 按调用顺序依次返回预设的 IP 列表,用于 DNS 重绑定测试.
type fakeResolver struct {
	responses [][]net.IPAddr
	call      int32
}

func (r *fakeResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	_ = ctx
	_ = host
	n := int(atomic.AddInt32(&r.call, 1)) - 1
	if n >= len(r.responses) {
		n = len(r.responses) - 1
	}
	if n < 0 || len(r.responses) == 0 {
		return nil, fmt.Errorf("no ips configured")
	}
	return r.responses[n], nil
}

// hostMapResolver 按主机名映射返回特定 IP,未命中时回退到 fallback.
type hostMapResolver struct {
	m        map[string][]net.IPAddr
	fallback []net.IPAddr
}

func (r *hostMapResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	_ = ctx
	if ips, ok := r.m[host]; ok {
		return ips, nil
	}
	return r.fallback, nil
}

func ips(strs ...string) []net.IPAddr {
	out := make([]net.IPAddr, 0, len(strs))
	for _, s := range strs {
		out = append(out, net.IPAddr{IP: net.ParseIP(s)})
	}
	return out
}

var publicResolver = &fakeResolver{responses: [][]net.IPAddr{ips("93.184.216.34")}}

func mustURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}

func mkReq(raw string) *http.Request {
	u := mustURL(raw)
	r := &http.Request{
		Method: "GET",
		URL:    u,
		Header: make(http.Header),
		Host:   u.Host,
	}
	return r.WithContext(context.Background())
}

// leaksRawQuery 检查错误信息是否泄露了原始 URL 的查询串或 userinfo.
func leaksRawQuery(rawURL, errStr string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if u.RawQuery != "" && strings.Contains(errStr, u.RawQuery) {
		return true
	}
	if u.User != nil {
		if s := u.User.String(); s != "" && strings.Contains(errStr, s) {
			return true
		}
	}
	if u.Host != "" && strings.Contains(errStr, u.Host) {
		return true
	}
	return false
}

func TestDownloadURLGuard(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		rawURL   string
		resolver downloadResolver
		wantSub  string // 期望的错误子串,空串表示期望成功
	}{
		// 各平台合法 CDN host
		{"xhs root", "xiaohongshu", "https://www.xiaohongshu.com/explore", publicResolver, ""},
		{"xhs xhscdn", "xiaohongshu", "https://sns-img-bd.xhscdn.com/a.jpg", publicResolver, ""},
		{"xhs xhslink", "xiaohongshu", "https://xhslink.com/a", publicResolver, ""},
		{"douyin root", "douyin", "https://www.douyin.com/v/1", publicResolver, ""},
		{"douyin vod", "douyin", "https://v.douyinvod.com/x.m4v", publicResolver, ""},
		{"douyin pic", "douyin", "https://p3.douyinpic.com/x.jpg", publicResolver, ""},
		{"douyin byteimg", "douyin", "https://p9.byteimg.com/x.jpg", publicResolver, ""},
		{"douyin bytecdn", "douyin", "https://a.bytecdn.cn/x", publicResolver, ""},
		{"tiktok root", "tiktok", "https://www.tiktok.com/@u/v/1", publicResolver, ""},
		{"tiktok cdn", "tiktok", "https://v16.tiktokcdn.com/x.m4v", publicResolver, ""},
		{"tiktok tiktokv", "tiktok", "https://m.tiktokv.com/x", publicResolver, ""},
		{"tiktok byteoversea", "tiktok", "https://a.byteoversea.com/y", publicResolver, ""},
		{"tiktok ibytedtos", "tiktok", "https://a.ibytedtos.com/z", publicResolver, ""},
		{"uppercase host normalized", "tiktok", "https://V16.TIKTOKCDN.COM/x.m4v", publicResolver, ""},

		// scheme / userinfo / platform / host / port 错误
		{"http rejected", "xiaohongshu", "http://xhscdn.com/x", publicResolver, "scheme"},
		{"userinfo rejected", "xiaohongshu", "https://user:pass@xhscdn.com/x", publicResolver, "userinfo"},
		{"userinfo with query rejected", "xiaohongshu", "https://user:secret@xhscdn.com/x?token=abc", publicResolver, "userinfo"},
		{"unsupported platform", "myspace", "https://xhscdn.com/x", publicResolver, "platform"},
		{"disallowed host", "xiaohongshu", "https://evil.com/x?secret=1", publicResolver, "host"},
		{"empty host", "xiaohongshu", "https:///path", publicResolver, "host"},
		{"port too large", "xiaohongshu", "https://xhscdn.com:99999/x", publicResolver, "port"},
		{"port non-numeric", "xiaohongshu", "https://xhscdn.com:abc/x", publicResolver, "url"},
		{"port zero", "xiaohongshu", "https://xhscdn.com:0/x", publicResolver, "port"},

		// 后缀攻击
		{"suffix attack xhs", "xiaohongshu", "https://xiaohongshu.com.evil.test/x", publicResolver, "host"},
		{"suffix attack tiktok", "tiktok", "https://eviltiktok.com/x", publicResolver, "host"},

		// 通过 DNS 解析返回的私有/保留地址
		{"loopback v4", "xiaohongshu", "https://xhscdn.com/x", &fakeResolver{responses: [][]net.IPAddr{ips("127.0.0.1")}}, "address"},
		{"private 10", "xiaohongshu", "https://xhscdn.com/x", &fakeResolver{responses: [][]net.IPAddr{ips("10.0.0.1")}}, "address"},
		{"private 172", "xiaohongshu", "https://xhscdn.com/x", &fakeResolver{responses: [][]net.IPAddr{ips("172.16.0.1")}}, "address"},
		{"private 192", "xiaohongshu", "https://xhscdn.com/x", &fakeResolver{responses: [][]net.IPAddr{ips("192.168.1.1")}}, "address"},
		{"link-local v4 metadata", "xiaohongshu", "https://xhscdn.com/x", &fakeResolver{responses: [][]net.IPAddr{ips("169.254.169.254")}}, "address"},
		{"loopback v6", "xiaohongshu", "https://xhscdn.com/x", &fakeResolver{responses: [][]net.IPAddr{ips("::1")}}, "address"},
		{"private v6", "xiaohongshu", "https://xhscdn.com/x", &fakeResolver{responses: [][]net.IPAddr{ips("fc00::1")}}, "address"},
		{"link-local v6", "xiaohongshu", "https://xhscdn.com/x", &fakeResolver{responses: [][]net.IPAddr{ips("fe80::1")}}, "address"},
		{"v4-mapped v6 metadata", "xiaohongshu", "https://xhscdn.com/x", &fakeResolver{responses: [][]net.IPAddr{ips("::ffff:169.254.169.254")}}, "address"},
		{"empty DNS result", "xiaohongshu", "https://xhscdn.com/x", &fakeResolver{responses: [][]net.IPAddr{{}}}, "address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newDownloadURLGuardWithResolver(tt.resolver, &net.Dialer{Timeout: 5 * time.Second})
			_, err := g.Validate(context.Background(), tt.platform, tt.rawURL)
			switch {
			case tt.wantSub == "":
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
			case err == nil:
				t.Fatalf("expected error containing %q, got nil", tt.wantSub)
			case !strings.Contains(err.Error(), tt.wantSub):
				t.Fatalf("expected error containing %q, got %v", tt.wantSub, err)
			}
			if err != nil && leaksRawQuery(tt.rawURL, err.Error()) {
				t.Fatalf("error leaks raw URL data: %v", err)
			}
		})
	}
}

// TestDownloadRebinding 模拟 DNS 重绑定:Validate 时返回公网 IP,
// DialContext 时返回私有 IP,必须在拨号阶段被拒绝.
func TestDownloadRebinding(t *testing.T) {
	r := &fakeResolver{responses: [][]net.IPAddr{
		ips("93.184.216.34"), // Validate 调用
		ips("10.0.0.1"),      // DialContext 调用
	}}
	g := newDownloadURLGuardWithResolver(r, &net.Dialer{Timeout: time.Second})
	if _, err := g.Validate(context.Background(), "xiaohongshu", "https://test.xhscdn.com/x"); err != nil {
		t.Fatalf("Validate should pass on public IP: %v", err)
	}
	client := g.NewClient("xiaohongshu")
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.Transport)
	}
	if _, err := tr.DialContext(context.Background(), "tcp", "test.xhscdn.com:443"); err == nil {
		t.Fatal("DialContext should reject DNS-rebinding to private IP")
	}
}

func TestDownloadRedirect(t *testing.T) {
	mkGuard := func(r downloadResolver) *downloadURLGuard {
		return newDownloadURLGuardWithResolver(r, &net.Dialer{Timeout: time.Second})
	}

	t.Run("allowed same family", func(t *testing.T) {
		g := mkGuard(publicResolver)
		via := []*http.Request{mkReq("https://a.xhscdn.com/1")}
		if err := g.checkRedirect("xiaohongshu", mkReq("https://b.xhscdn.com/2"), via); err != nil {
			t.Fatalf("expected ok, got %v", err)
		}
	})

	t.Run("cross-family rejected", func(t *testing.T) {
		g := mkGuard(publicResolver)
		via := []*http.Request{mkReq("https://a.xhscdn.com/1")}
		err := g.checkRedirect("xiaohongshu", mkReq("https://www.tiktok.com/x"), via)
		if err == nil {
			t.Fatal("expected cross-family rejection")
		}
	})

	t.Run("redirect to localhost rejected", func(t *testing.T) {
		r := &hostMapResolver{
			m:        map[string][]net.IPAddr{"xhscdn.com": ips("127.0.0.1")},
			fallback: ips("93.184.216.34"),
		}
		g := mkGuard(r)
		via := []*http.Request{mkReq("https://a.xhscdn.com/1")}
		err := g.checkRedirect("xiaohongshu", mkReq("https://xhscdn.com/redirect"), via)
		if err == nil {
			t.Fatal("expected rejection of localhost via DNS")
		}
	})

	t.Run("redirect to private rejected", func(t *testing.T) {
		r := &hostMapResolver{
			m:        map[string][]net.IPAddr{"xhscdn.com": ips("10.0.0.1")},
			fallback: ips("93.184.216.34"),
		}
		g := mkGuard(r)
		via := []*http.Request{mkReq("https://a.xhscdn.com/1")}
		err := g.checkRedirect("xiaohongshu", mkReq("https://xhscdn.com/redirect"), via)
		if err == nil {
			t.Fatal("expected rejection of private IP via DNS")
		}
	})

	t.Run("sixth hop rejected", func(t *testing.T) {
		g := mkGuard(publicResolver)
		via := make([]*http.Request, 0, 5)
		for i := 0; i < 5; i++ {
			via = append(via, mkReq(fmt.Sprintf("https://h%d.xhscdn.com/%d", i, i)))
		}
		err := g.checkRedirect("xiaohongshu", mkReq("https://h5.xhscdn.com/5"), via)
		if err == nil {
			t.Fatal("expected too-many-redirects error")
		}
		if !strings.Contains(err.Error(), "redirect") {
			t.Fatalf("expected redirect-related error, got %v", err)
		}
	})

	t.Run("loop rejected", func(t *testing.T) {
		g := mkGuard(publicResolver)
		via := []*http.Request{
			mkReq("https://a.xhscdn.com/1"),
			mkReq("https://b.xhscdn.com/2"),
		}
		err := g.checkRedirect("xiaohongshu", mkReq("https://a.xhscdn.com/1"), via)
		if err == nil {
			t.Fatal("expected redirect loop error")
		}
		if !strings.Contains(err.Error(), "loop") {
			t.Fatalf("expected loop error, got %v", err)
		}
	})

	t.Run("error no raw query leak", func(t *testing.T) {
		g := mkGuard(publicResolver)
		via := []*http.Request{mkReq("https://a.xhscdn.com/1")}
		raw := "https://www.tiktok.com/x?secret=token&user=42"
		err := g.checkRedirect("xiaohongshu", mkReq(raw), via)
		if err == nil {
			t.Fatal("expected rejection")
		}
		if leaksRawQuery(raw, err.Error()) {
			t.Fatalf("error leaks raw URL data: %v", err)
		}
	})
}
