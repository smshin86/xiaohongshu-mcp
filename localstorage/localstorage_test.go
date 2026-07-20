package localstorage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
)

func tmpStorer(t *testing.T) Storer {
	t.Helper()
	return NewFileStorer(filepath.Join(t.TempDir(), "local_storage.json"))
}

// TestSaveLoadRoundtrip: 저장→로드가 엔트리를 보존한다(값 비교).
func TestSaveLoadRoundtrip(t *testing.T) {
	s := tmpStorer(t)
	in := []Entry{{Key: "a1", Value: "v1"}, {Key: "xsecappid", Value: "xhs-pcs-web"}}
	require.NoError(t, s.Save(in))

	out, err := s.Load()
	require.NoError(t, err)
	require.Equal(t, in, out)
}

// TestLoadMissing: 파일이 없으면 ErrNotFound (미로그인 graceful).
func TestLoadMissing(t *testing.T) {
	entries, err := tmpStorer(t).Load()
	require.True(t, errors.Is(err, ErrNotFound), "want ErrNotFound, got %v", err)
	require.Empty(t, entries)
}

// TestLoadMalformed: JSON 이 아니면 에러(ErrNotFound 아님).
func TestLoadMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local_storage.json")
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0600))
	_, err := NewFileStorer(path).Load()
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrNotFound))
}

// TestLoadOversize: 파일이 크기 상한 초과면 에러.
func TestLoadOversize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local_storage.json")
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("x", maxSize+1)), 0600))
	_, err := NewFileStorer(path).Load()
	require.Error(t, err)
}

// TestSaveOversize: 엔트리 합계가 상한 초과면 저장 거부.
func TestSaveOversize(t *testing.T) {
	s := tmpStorer(t)
	err := s.Save([]Entry{{Key: "k", Value: strings.Repeat("x", maxSize+1)}})
	require.Error(t, err)
}

// TestSaveRejectsEmptyKey: 빈 키는 저장 거부(방어적 검증).
func TestSaveRejectsEmptyKey(t *testing.T) {
	require.Error(t, tmpStorer(t).Save([]Entry{{Key: "", Value: "v"}}))
}

// TestLoadWrongOrigin: XHS 가 아닌 origin 이면 복원 거부.
func TestLoadWrongOrigin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local_storage.json")
	raw, err := json.Marshal(map[string]any{"origin": "https://evil.example", "entries": []Entry{}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0600))
	_, err = NewFileStorer(path).Load()
	require.Error(t, err)
}

// TestDelete: 삭제 후 Load 는 ErrNotFound; 재호출 멱등(nil).
func TestDelete(t *testing.T) {
	s := tmpStorer(t)
	require.NoError(t, s.Save([]Entry{{Key: "k", Value: "v"}}))
	require.NoError(t, s.Delete())

	_, err := s.Load()
	require.True(t, errors.Is(err, ErrNotFound))

	// 멱등: 없는 파일 삭제도 nil
	require.NoError(t, s.Delete())
}

// TestFilePermissions: 저장 파일 권한은 0600.
func TestFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local_storage.json")
	require.NoError(t, NewFileStorer(path).Save([]Entry{{Key: "k", Value: "v"}}))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
}
