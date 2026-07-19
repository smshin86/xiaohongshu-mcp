// Package localstorage: XHS 인증 localStorage 를 로컬 secret 파일로 영속화.
// 보안: 값/토큰은 로그 금지(호출자 책임), 파일 0600, 원자적 write,
// 크기 상한 + JSON/schema 검증, XHS 정확한 origin 만 허용.
package localstorage

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/pkg/errors"
)

const (
	// XHSOrigin: 복원 허용 origin(정확히 일치해야 함).
	XHSOrigin = "https://www.xiaohongshu.com"

	// maxSize: 파일/엔트리 크기 상한(방어적). 1 MiB.
	maxSize = 1 << 20
)

// ErrNotFound: 저장 파일이 없음(미로그인 graceful 처리용 sentinel).
var ErrNotFound = errors.New("local storage file not found")

// Entry: localStorage 항목. Value 는 비밀값(로그 출력 금지).
type Entry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type payload struct {
	Origin  string  `json:"origin"`
	Entries []Entry `json:"entries"`
}

// Storer: XHS localStorage 영속화(테스트 가능한 작은 인터페이스).
type Storer interface {
	Save([]Entry) error
	Load() ([]Entry, error)
	Delete() error
}

type fileStorer struct {
	path string
}

// NewFileStorer: 주어진 경로를 쓰는 file 기반 Storer.
func NewFileStorer(path string) Storer {
	return &fileStorer{path: path}
}

// GetFilePath: 환경변수 LOCALSTORAGE_PATH 우선, 없으면 local_storage.json.
func GetFilePath() string {
	if p := os.Getenv("LOCALSTORAGE_PATH"); p != "" {
		return p
	}
	return "local_storage.json"
}

func validateEntries(entries []Entry) error {
	for _, e := range entries {
		if e.Key == "" {
			return errors.New("empty localStorage key")
		}
		if len(e.Value) > maxSize {
			// 길이만 로그(값 아님).
			return errors.Errorf("localStorage value too large: key length=%d", len(e.Key))
		}
	}
	return nil
}

// Save: 스키마/크기 검증 후 0600 으로 원자적 write(temp+rename).
func (f *fileStorer) Save(entries []Entry) error {
	if err := validateEntries(entries); err != nil {
		return err
	}
	data, err := json.Marshal(payload{Origin: XHSOrigin, Entries: entries})
	if err != nil {
		return errors.Wrap(err, "marshal local storage")
	}
	if len(data) > maxSize {
		return errors.Errorf("local storage payload too large: %d bytes", len(data))
	}
	return atomicWrite(f.path, data, 0600)
}

// Load: missing -> ErrNotFound; malformed/oversize/wrong-origin -> error.
func (f *fileStorer) Load() ([]Entry, error) {
	info, err := os.Stat(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, errors.Wrap(err, "stat local storage")
	}
	if info.Size() > maxSize {
		return nil, errors.Errorf("local storage file too large: %d bytes", info.Size())
	}
	data, err := os.ReadFile(f.path)
	if err != nil {
		return nil, errors.Wrap(err, "read local storage")
	}
	var p payload
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, errors.Wrap(err, "malformed local storage")
	}
	if p.Origin != XHSOrigin {
		// origin 만 로그(값 아님).
		return nil, errors.Errorf("refused non-XHS origin: %q", p.Origin)
	}
	if err := validateEntries(p.Entries); err != nil {
		return nil, err
	}
	return p.Entries, nil
}

// Delete: 멱등(없어도 nil).
func (f *fileStorer) Delete() error {
	if err := os.Remove(f.path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return errors.Wrap(err, "delete local storage")
	}
	return nil
}

// atomicWrite: 같은 디렉토리 임시 파일에 쓰고 perm 설정 뒤 rename(원자적 교체).
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".ls-*")
	if err != nil {
		return errors.Wrap(err, "create temp file")
	}
	tmp := f.Name()
	defer os.Remove(tmp) // rename 성공 시 이미 이동됨(no-op)

	if err := f.Chmod(perm); err != nil {
		_ = f.Close()
		return errors.Wrap(err, "chmod temp file")
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return errors.Wrap(err, "write temp file")
	}
	if err := f.Close(); err != nil {
		return errors.Wrap(err, "close temp file")
	}
	if err := os.Rename(tmp, path); err != nil {
		return errors.Wrap(err, "rename temp file")
	}
	return nil
}
