package xiaohongshu

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseAuthState: robust 인증 판정 계약 검증.
// isAuthStateComplete 는 loggedIn AND 식별 정보가 모두 충족된 경우만 true.
// 이 로직은 already 판정(FetchQrcodeImage) 과 fallback 판정(CheckLoginStatus)이
// 공통으로 사용한다 → weak 상태(loggedIn 만 true) 를 로그인으로 오판하지 않는다.
func TestParseAuthState(t *testing.T) {
	// (1) loggedIn + 식별 → complete (robust 인증).
	require.True(t, isAuthStateComplete(parseAuthState(`{"loggedIn":true,"hasIdentity":true}`)))

	// (2) loggedIn 만 true 고 식별 없음(weak / QR 중간 승인) → 미인증.
	require.False(t, isAuthStateComplete(parseAuthState(`{"loggedIn":true,"hasIdentity":false}`)))

	// (3) 둘 다 없음 → 미인증.
	require.False(t, isAuthStateComplete(parseAuthState(`{"loggedIn":false,"hasIdentity":false}`)))

	// (4) 잘못된 JSON / 빈 문자열 → zero state → 미인증(안전).
	require.False(t, isAuthStateComplete(parseAuthState(`not-json`)))
	require.False(t, isAuthStateComplete(parseAuthState(``)))
}
