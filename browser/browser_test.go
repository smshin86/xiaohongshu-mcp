package browser

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestValidateUserAgent: XHS_USER_AGENT 검증 계약.
// 빈 값은 미설정으로 허용(기본 동작 호환), 제어문자/CR/LF 와 과긴 값은 거부한다.
// UA 는 런치 인자(--user-agent) 로 전달되므로 CR/LF 가 인자/헤더 주입을 막는 게 핵심.
func TestValidateUserAgent(t *testing.T) {
	// 빈 → 미설정 허용(기본 Chrome/124 유지).
	require.NoError(t, validateUserAgent(""))

	// 정상 UA → 허용(시스템 Chrome 150 과 일치하는 형태).
	require.NoError(t, validateUserAgent(
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"))

	// CR/LF 거부(런치 인자/헤더 주입 방지).
	require.Error(t, validateUserAgent("Mozilla/5.0\r\nX-Inject: 1"))
	require.Error(t, validateUserAgent("line1\nline2"))
	// 탭 등 기타 제어문자도 거부.
	require.Error(t, validateUserAgent("tab\there"))
	// NUL 도 거부.
	require.Error(t, validateUserAgent("bad\x00ua"))
	// DEL(0x7f) 도 거부.
	require.Error(t, validateUserAgent("del\x7f"))

	// 길이 상한: 경계값(=최대) 허용, 초과 거부.
	require.NoError(t, validateUserAgent(strings.Repeat("a", maxUserAgentLen)))
	require.Error(t, validateUserAgent(strings.Repeat("a", maxUserAgentLen+1)))
}

// TestWithUserAgentOptionSetsConfig: WithUserAgent 옵션이 config 에 UA 를 기록한다.
func TestWithUserAgentOptionSetsConfig(t *testing.T) {
	cfg := &browserConfig{}
	WithUserAgent("Mozilla/5.0 Chrome/150")(cfg)
	require.Equal(t, "Mozilla/5.0 Chrome/150", cfg.userAgent)

	// 빈 옵션값도 그대로 반영(미설정 → NewBrowser 가 env fallback).
	cfg2 := &browserConfig{}
	WithUserAgent("")(cfg2)
	require.Equal(t, "", cfg2.userAgent)
}
