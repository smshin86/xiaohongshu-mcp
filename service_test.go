package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSavedCookiesAt: 쿠키 파일 존재/크기/디렉토리 여부로 fast path 진입 조건을 결정한다.
// 임시 경로로 결정적으로 검증(실제 쿠키 파일에 의존하지 않음).
func TestSavedCookiesAt(t *testing.T) {
	dir := t.TempDir()

	// (1) 존재하지 않는 경로 → false (가장 흔한 fast-path 진입 케이스).
	require.False(t, savedCookiesAt(filepath.Join(dir, "missing.json")))

	// (2) 빈 파일(크기 0) → false: 0바이트 쿠키는 미로그인과 동급.
	empty := filepath.Join(dir, "empty.json")
	require.NoError(t, os.WriteFile(empty, nil, 0o600))
	require.False(t, savedCookiesAt(empty))

	// (3) 내용이 있는 파일(크기 > 0) → true: 저장된 로그인 쿠키가 있으면 브라우저 검증 진행.
	full := filepath.Join(dir, "cookies.json")
	require.NoError(t, os.WriteFile(full, []byte(`{"a":1}`), 0o600))
	require.True(t, savedCookiesAt(full))

	// (4) 경로가 디렉토리 → false: 파일이 아니면 쿠키로 인정하지 않는다.
	require.False(t, savedCookiesAt(dir))
}
