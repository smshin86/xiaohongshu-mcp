package cookies

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSaveCookiesPerm0600: 저장 파일 권한은 0600(비밀값 보호 회귀 방지).
func TestSaveCookiesPerm0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	c := NewLoadCookie(path)

	require.NoError(t, c.SaveCookies([]byte("[]")))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm(), "cookies file must be 0600")
}

// TestSaveLoadRoundtrip: 저장→로드 데이터 보존.
func TestSaveLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	c := NewLoadCookie(path)

	payload := []byte(`[{"name":"web_session","value":"secret"}]`)
	require.NoError(t, c.SaveCookies(payload))

	got, err := c.LoadCookies()
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

// TestSaveCookiesReplacesExisting: 짧은 내용으로 안전 교체(이전 잔류 없음).
func TestSaveCookiesReplacesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	c := NewLoadCookie(path)

	require.NoError(t, c.SaveCookies([]byte("old-longer-content")))
	require.NoError(t, c.SaveCookies([]byte("new")))

	got, err := c.LoadCookies()
	require.NoError(t, err)
	require.Equal(t, []byte("new"), got)
}

// TestDeleteCookiesIdempotent: 없는 파일 삭제도 nil.
func TestDeleteCookiesIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	c := NewLoadCookie(path)

	require.NoError(t, c.SaveCookies([]byte("[]")))
	require.NoError(t, c.DeleteCookies())

	// 파일 없음 확인
	_, err := os.Stat(path)
	require.True(t, os.IsNotExist(err))

	// 멱등
	require.NoError(t, c.DeleteCookies())
}
