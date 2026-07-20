package search

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	xhserrors "github.com/xpzouying/xiaohongshu-mcp/errors"
)

// TestSideErrorMessageAuthLost: ErrAuthLost(XHS 위험제어 세션 무효화)는
// "로그인 세션 해제/재로그인 필요" 로 매핑되어야 한다. 원문/값 미포함 고정 메시지.
func TestSideErrorMessageAuthLost(t *testing.T) {
	// sentinel 직접 전달(fast-fail 경로: SearchFeeds 가 ErrAuthLost 를 그대로 반환).
	require.Equal(t, "로그인 세션 해제/재로그인 필요", SideErrorMessage("xiaohongshu", xhserrors.ErrAuthLost))
	// ErrAuthLost 는 deadline 이 아니므로 timeout 메시지로 가려지지 않는다.
	require.NotEqual(t, "응답 시간 초과", SideErrorMessage("xiaohongshu", xhserrors.ErrAuthLost))
	// 플랫폼 무관 동일 메시지(인증 단결은 공통 안내).
	require.Equal(t, "로그인 세션 해제/재로그인 필요", SideErrorMessage("douyin", xhserrors.ErrAuthLost))
}

// TestSideErrorMessageDeadlineNotShadowedByAuthLost: deadline 은 여전히 timeout 메시지.
// ErrAuthLost 케이스가 deadline 보다 먼저 매칭되지 않는지 회귀 방지(순서 독립성).
func TestSideErrorMessageDeadlineNotShadowedByAuthLost(t *testing.T) {
	require.Equal(t, "응답 시간 초과", SideErrorMessage("xiaohongshu", context.DeadlineExceeded))
}
