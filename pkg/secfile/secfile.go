// Package secfile: 비밀값 파일의 원자적 쓰기 유틸(cookies/localStorage 공용).
// 같은 디렉토리의 임시 파일에 쓰고 권한을 설정한 뒤 rename 하여
// 부분 쓰기/권한 누락을 원천 방지한다.
package secfile

import (
	"os"
	"path/filepath"

	"github.com/pkg/errors"
)

// WriteFile: data 를 path 에 원자적으로(temp+rename) 기록하며 perm 권한 적용.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".sf-*")
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
