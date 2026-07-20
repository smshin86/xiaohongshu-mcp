package main

import (
	"encoding/json"

	"github.com/go-rod/rod"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/localstorage"
)

// captureXhsLocalStorage: 로그인된 페이지에서 XHS origin 의 localStorage 를 수집.
// 값/토큰은 반환된 엔트리에만 존재하며 로그에 절대 노출하지 않는다.
func captureXhsLocalStorage(page *rod.Page) ([]localstorage.Entry, error) {
	res, err := page.Eval(`() => {
		if (location.origin !== "https://www.xiaohongshu.com") {
			return JSON.stringify({origin: "", entries: []});
		}
		const entries = [];
		for (let i = 0; i < localStorage.length; i++) {
			const k = localStorage.key(i);
			if (!k) continue;
			entries.push({key: k, value: localStorage.getItem(k) || ""});
		}
		return JSON.stringify({origin: location.origin, entries: entries});
	}`)
	if err != nil {
		return nil, err
	}

	var p struct {
		Origin  string               `json:"origin"`
		Entries []localstorage.Entry `json:"entries"`
	}
	if err := json.Unmarshal([]byte(res.Value.String()), &p); err != nil {
		return nil, err
	}
	return p.Entries, nil
}

// saveXhsLocalStorage: 수집한 localStorage 를 0600 으로 영속화(값 미출력).
func saveXhsLocalStorage(page *rod.Page) error {
	entries, err := captureXhsLocalStorage(page)
	if err != nil {
		return err
	}
	storer := localstorage.NewFileStorer(localstorage.GetFilePath())
	if err := storer.Save(entries); err != nil {
		return err
	}
	logrus.Debugf("saved %d local storage entries", len(entries))
	return nil
}

// restoreXhsLocalStorage: 페이지 최초 네비게이션 전에 localStorage 재생.
// EvalOnNewDocument 로 page 스크립트보다 먼저 setItem 하므로 reload 불필요.
// 파일이 없거나 손상/과대면 graceful 하게 스킵(미로그인으로 처리). 값은 로그 금지.
// Must* 미사용, 에러 반환(context-safe).
func restoreXhsLocalStorage(page *rod.Page) error {
	storer := localstorage.NewFileStorer(localstorage.GetFilePath())
	entries, err := storer.Load()
	if err != nil {
		if errors.Is(err, localstorage.ErrNotFound) {
			return nil // 미로그인: 스킵
		}
		// malformed/oversize/wrong-origin: 원인만 로그(값 금지), 스킵.
		logrus.Warnf("skip local storage restore: %v", err)
		return nil
	}
	if len(entries) == 0 {
		return nil
	}

	// 엔트리를 JSON 리터럴로 삽입(JSON 은 유효한 JS, json.Marshal 이 이스케이프).
	jsEntries, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	js := `;(function () {
		if (location.origin !== "https://www.xiaohongshu.com") return;
		try {
			var entries = ` + string(jsEntries) + `;
			for (var i = 0; i < entries.length; i++) {
				localStorage.setItem(entries[i].key, entries[i].value);
			}
		} catch (e) {}
	})();`

	remove, err := page.EvalOnNewDocument(js)
	if err != nil {
		return err
	}
	_ = remove // 페이지 종료 시 함께 정리됨
	logrus.Debugf("registered restore for %d local storage entries", len(entries))
	return nil
}
