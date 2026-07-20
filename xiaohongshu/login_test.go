package xiaohongshu

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseAuthCheckResult: 인증 판정 JSON 계약 검증.
// ok=true 는 loggedIn AND 식별정보가 충족된 경우만 나와야 한다(false-positive 방지).
// 단순 loggedIn=true 만으로는 QR 중간 상태를 성공으로 오판할 수 있다.
func TestParseAuthCheckResult(t *testing.T) {
	// ok=true → 인증+식별 충족.
	require.True(t, parseAuthCheckResult(`{"ok":true}`))
	// ok=false → 미인증 또는 식별 정보 없음.
	require.False(t, parseAuthCheckResult(`{"ok":false}`))
	// 잘못된 JSON/빈 문자열 → 안전하게 false(미인증 처리).
	require.False(t, parseAuthCheckResult(`not-json`))
	require.False(t, parseAuthCheckResult(``))
}
