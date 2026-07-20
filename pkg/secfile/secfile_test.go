package secfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWriteFileContentAndPerm: 내용 보존 + 요청한 권한 적용.
func TestWriteFileContentAndPerm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.json")
	require.NoError(t, WriteFile(path, []byte(`{"x":1}`), 0600))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, `{"x":1}`, string(data))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

// TestWriteFileReplacesExisting: 기존 파일을 짧은 내용으로 안전하게 교체(원자 rename).
func TestWriteFileReplacesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.json")
	require.NoError(t, WriteFile(path, []byte("old-longer-content-here"), 0600))
	require.NoError(t, WriteFile(path, []byte("new"), 0600))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "new", string(data))
}

// TestWriteFileLeavesNoTempBehind: 임시 파일이 디렉토리에 남지 않는다.
func TestWriteFileLeavesNoTempBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.json")
	require.NoError(t, WriteFile(path, []byte("ok"), 0600))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "secret.json", entries[0].Name())
}

// TestWriteFileRejectsBadDir: 존재하지 않는 디렉토리면 에러(조용한 실패 방지).
func TestWriteFileRejectsBadDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "secret.json")
	err := WriteFile(path, []byte("x"), 0600)
	require.Error(t, err)
}

// TestWriteFileLargePayload: 큰 페이로드도 온전히 기록.
func TestWriteFileLargePayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.json")
	payload := []byte(strings.Repeat("a", 64*1024))
	require.NoError(t, WriteFile(path, payload, 0600))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, int64(len(payload)), info.Size())
}
