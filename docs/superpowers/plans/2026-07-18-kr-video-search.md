# 샤오홍슈 영상 검색 페이지 (한국어 UI) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** xiaohongshu-mcp HTTP API 위에 한국어 UI 영상 검색 페이지를 구축한다 — 중국어 검색어로 영상 결과를 카드 목록으로 보여주고, 카드 재생 클릭 시 상세 API에서 영상 mp4 URL을 확보해 모달로 재생한다.

**Architecture:** Go 서버에 `web/` 정적 파일 서빙 라우트 2줄을 추가해 단일 서버에서 API + 페이지를 같은 출처로 제공한다. 상세 스크래퍼가 이미 읽는 `__INITIAL_STATE__`에서 영상 URL(`note.video.media.stream`)을 추가로 추출해 API 응답에 평탄화한다. 프론트는 빌드 없는 vanilla HTML/CSS/JS.

**Tech Stack:** Go 1.x (gin, go-rod, modelcontextprotocol/go-sdk, testify/require), vanilla HTML/CSS/JS.

**Spec:** `docs/superpowers/specs/2026-07-18-xhs-video-search-design.md`

## Global Constraints

- **브랜치**: `feat/kr-video-search` (이미 생성됨). 모든 커밋은 이 브랜치에서.
- **원격 푸시 금지**: 사용자 동의 없이 `git push` 하지 않는다 (CLAUDE.md).
- **Go 포맷**: 소스 변경 후 반드시 `gofmt -w` / `goimports -w` 적용 (CLAUDE.md).
- **주석 언어**: 신규 코드 주석은 **한국어**. 기존 중국어 주석은 건드리지 않는다.
- **검색 필터**: 항상 `{ note_type: "视频" }` 로 영상만 요청. 추가로 클라이언트에서 `noteCard.type === "video"` 안전망 필터 적용.
- **UI 언어**: 모든 UI 문구 한국어 (spec §6.2 카피 표 참조). 포인트 컬러 `#ff2442`.
- **서버 실행 위치**: 반드시 저장소 **루트에서** 실행 (`./web` 상대경로가 web 폻더를 가리키도록). 기본 포트 `:18060`, 헤드리스 `true`.
- **프론트엔드 테스트**: JS 테스트 러너를 도입하지 않는다 (오버엔지니어링 회피). 프론트 태스크는 브라우저 수동 검증으로 확인. Go 변경은 단위 테스트로 확인.
- **커밋 메시지**: conventional commits (영문), 끝에 `Co-Authored-By: Claude <noreply@anthropic.com>`.

---

## File Structure

**Backend (Go) — 수정:**
- `xiaohongshu/types.go` — `DetailVideo`/`DetailMedia`/`DetailStreamItem` 구조체 추가, `FeedDetail.Video` 필드 추가, `(*DetailVideo).VideoURL()` 메서드 추가.
- `types.go` (root, package main) — `FeedDetailResponse.VideoURL` 필드 추가.
- `service.go` — `GetFeedDetailWithConfig`에서 `response.VideoURL` 채우기.
- `routes.go` — 정적 서빙 라우트(`/`, `/static/*`) 추가.

**Backend (Go) — 신규 테스트:**
- `xiaohongshu/video_url_test.go` — `VideoURL()` + JSON 파싱 단위 테스트.
- `static_routes_test.go` (package main) — 정적 라우트 통합 테스트.

**Frontend — 신규:**
- `web/index.html` — 전체 마크업(헤더, 검색바, 결과 그리드, 로그인 섹션, 영상 모달). 모든 DOM id/class는 본 문서 §"DOM 계약"에 고정.
- `web/style.css` — 스타일(반응형 카드 그리드, 모달, 한국어 UI).
- `web/app.js` — 로직. api/state/render/login/modal 모듈로 구성. Task 5→6→7 에 걸쳐 점진 확장.

### DOM 계약 (index.html ↔ app.js 약속)

| id | 용도 |
|---|---|
| `#login-section` | 로그인 패널 컨테이너 |
| `#login-btn` | "QR 코드로 로그인" 버튼 |
| `#qrcode-img` | QR `<img>` |
| `#login-status` | 로그인 상태 텍스트 |
| `#search-section` | 검색 UI 컨테이너 |
| `#search-form` | 검색 폼 |
| `#keyword` | 검색어 input |
| `#status` | 검색 상태 메시지 |
| `#results` | 결과 카드 그리드 |
| `#video-modal` | 영상 모달 |
| `#video-player` | `<video>` |
| `#copy-url-btn` | "URL 복사" 버튼 |
| `#modal-close` | 모달 닫기 버튼 |

CSS 클래스: `.hidden`(숨김), `.card`, `.card-cover`, `.card-body`, `.card-title`, `.card-meta`, `.badge-video`, `.modal`, `.spinner`.

### API 응답 필드 계약 (app.js가 사용)

- 검색: `res.data.feeds[]` — 각 Feed: `id`, `xsecToken`, `noteCard.{type, displayTitle, user.{nickname,nickName,avatar}, cover.{urlDefault,urlPre}, interactInfo.{likedCount,commentCount,collectedCount}}`.
- 상세: `res.data.video_url` (string, 영상 mp4 직링크).
- 로그인 상태: `res.data.is_logged_in`, `res.data.username`.
- QR: `res.data.img` (base64; `data:` 접두사 없으면 보정).

---

## Task 1: 영상 URL 추출 — 구조체 + 헬퍼 + 단위 테스트

**Files:**
- Modify: `xiaohongshu/types.go` (`FeedDetail` 구조체 영역, 약 line 95-106)
- Create: `xiaohongshu/video_url_test.go`

**Interfaces:**
- Produces: `type DetailVideo struct`, `type DetailMedia struct`, `type DetailStreamItem struct`, method `func (v *DetailVideo) VideoURL() string`, field `FeedDetail.Video *DetailVideo`. 이후 Task 2가 `result.Note.Video.VideoURL()` 로 사용.

- [ ] **Step 1: 실패하는 테스트 작성**

`xiaohongshu/video_url_test.go` 생성:

```go
package xiaohongshu

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDetailVideo_VideoURL(t *testing.T) {
	tests := []struct {
		name  string
		video *DetailVideo
		want  string
	}{
		{
			name:  "nil 수신자는 빈 문자열",
			video: nil,
			want:  "",
		},
		{
			name:  "stream 없으면 빈 문자열",
			video: &DetailVideo{},
			want:  "",
		},
		{
			name: "h264 우선",
			video: &DetailVideo{Media: DetailMedia{Stream: map[string][]DetailStreamItem{
				"h264": {{MasterURL: "https://h264.mp4"}},
				"h265": {{MasterURL: "https://h265.mp4"}},
			}}},
			want: "https://h264.mp4",
		},
		{
			name: "h265 폴백",
			video: &DetailVideo{Media: DetailMedia{Stream: map[string][]DetailStreamItem{
				"h265": {{MasterURL: "https://h265.mp4"}},
				"av1":  {{MasterURL: "https://av1.mp4"}},
			}}},
			want: "https://h265.mp4",
		},
		{
			name: "av1 폴백",
			video: &DetailVideo{Media: DetailMedia{Stream: map[string][]DetailStreamItem{
				"av1": {{MasterURL: "https://av1.mp4"}},
			}}},
			want: "https://av1.mp4",
		},
		{
			name: "빈 masterUrl 건너뜀",
			video: &DetailVideo{Media: DetailMedia{Stream: map[string][]DetailStreamItem{
				"h264": {{MasterURL: ""}},
				"av1":  {{MasterURL: "https://av1.mp4"}},
			}}},
			want: "https://av1.mp4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.video.VideoURL())
		})
	}
}

// TestFeedDetailVideoUnmarshal 은 상세 페이지 JSON 구조(note.video.media.stream)와
// 구조체 매핑이 일치함을 고정한다.
func TestFeedDetailVideoUnmarshal(t *testing.T) {
	raw := `{"video":{"media":{"stream":{"h264":[{"masterUrl":"https://x.mp4"}]}}}}`
	var d FeedDetail
	require.NoError(t, json.Unmarshal([]byte(raw), &d))
	require.NotNil(t, d.Video)
	require.Equal(t, "https://x.mp4", d.Video.VideoURL())
}
```

- [ ] **Step 2: 테스트가 실패하는지 확인**

Run: `go test ./xiaohongshu/ -run TestDetailVideo_VideoURL -v`
Expected: 컴파일 실패 — `undefined: DetailVideo` (아직 구조체가 없음).

- [ ] **Step 3: 구현 — 구조체 + 메서드 추가**

`xiaohongshu/types.go` 의 `FeedDetail` 구조체 정의를 찾아(`type FeedDetail struct {`) `Video` 필드를 추가하고, 파일 끝에 새 구조체/메서드를 추가한다.

`FeedDetail` 에 필드 추가 (기존 필드는 그대로 두고 마지막에 추가):
```go
// FeedDetail 表示详情页的笔记内容
type FeedDetail struct {
	NoteID       string            `json:"noteId"`
	XsecToken    string            `json:"xsecToken"`
	Title        string            `json:"title"`
	Desc         string            `json:"desc"`
	Type         string            `json:"type"`
	Time         int64             `json:"time"`
	IPLocation   string            `json:"ipLocation"`
	User         User              `json:"user"`
	InteractInfo InteractInfo      `json:"interactInfo"`
	ImageList    []DetailImageInfo `json:"imageList"`
	Video        *DetailVideo      `json:"video,omitempty"` // 영상 정보(상세 페이지에서 추출)
}
```

파일 끝에 추가:
```go
// DetailVideo 영상 정보 (상세 페이지 __INITIAL_STATE__ 에서 추출)
type DetailVideo struct {
	Media DetailMedia `json:"media"`
}

// DetailMedia 영상 미디어 스트림 모음
type DetailMedia struct {
	// stream: { h264: [{masterUrl}], h265: [...], av1: [...] }
	Stream map[string][]DetailStreamItem `json:"stream"`
}

// DetailStreamItem 단일 스트림 항목
type DetailStreamItem struct {
	MasterURL string `json:"masterUrl"`
}

// VideoURL 은 h264 > h265 > av1 순으로 첫 번째 유효한 masterUrl 을 반환한다.
// 없으면 빈 문자열을 반환한다.
func (v *DetailVideo) VideoURL() string {
	if v == nil {
		return ""
	}
	for _, codec := range []string{"h264", "h265", "av1"} {
		if items := v.Media.Stream[codec]; len(items) > 0 && items[0].MasterURL != "" {
			return items[0].MasterURL
		}
	}
	return ""
}
```

- [ ] **Step 4: 테스트 통과 확인**

Run: `go test ./xiaohongshu/ -run "TestDetailVideo_VideoURL|TestFeedDetailVideoUnmarshal" -v`
Expected: PASS (두 테스트 모두).

- [ ] **Step 5: 포맷 + 전체 빌드**

Run: `gofmt -w xiaohongshu/types.go xiaohongshu/video_url_test.go && go build ./...`
Expected: 빌드 성공, 에러 없음.

- [ ] **Step 6: 커밋**

```bash
git add xiaohongshu/types.go xiaohongshu/video_url_test.go
git commit -m "feat(xhs): extract video URL from detail page state

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Task 2: 상세 API 응답에 video_url 노출

**Files:**
- Modify: `types.go` (root, package main) — `FeedDetailResponse` 구조체 (line 62-66)
- Modify: `service.go` — `GetFeedDetailWithConfig` 반환 부 (line 416-421)

**Interfaces:**
- Consumes: Task 1 의 `(*DetailVideo).VideoURL()`, `FeedDetail.Video`.
- Produces: `main.FeedDetailResponse.VideoURL string` (json `video_url`) — 프론트 Task 7이 `res.data.video_url` 로 사용.

- [ ] **Step 1: 응답 구조체에 필드 추가**

`types.go` (root) 의 `FeedDetailResponse` 수정:
```go
// FeedDetailResponse Feed详情响应
type FeedDetailResponse struct {
	FeedID   string `json:"feed_id"`
	Data     any    `json:"data"`
	VideoURL string `json:"video_url,omitempty"` // 재생 가능한 영상 직링크(상세 페이지에서 추출)
}
```

- [ ] **Step 2: service 에서 필드 채우기**

`service.go` 의 `GetFeedDetailWithConfig` 반환 부를 수정 (기존 `response := &FeedDetailResponse{FeedID: feedID, Data: result}` 부분):
```go
	response := &FeedDetailResponse{
		FeedID: feedID,
		Data:   result,
	}
	// 영상 URL 평탄화: 프론트가 간단히 쓸 수 있도록 최상위 필드로 노출
	if result.Note.Video != nil {
		response.VideoURL = result.Note.Video.VideoURL()
	}

	return response, nil
}
```

- [ ] **Step 3: 빌드 + 기존 테스트 회귀 확인**

Run: `gofmt -w types.go service.go && go build ./... && go test ./xiaohongshu/`
Expected: 빌드 성공, xiaohongshu 패키지 테스트 통과.

- [ ] **Step 4: 커밋**

```bash
git add types.go service.go
git commit -m "feat(api): expose video_url in feed detail response

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Task 3: 정적 파일 서빙 라우트 + 페이지 스켈레톤 (TDD)

**Files:**
- Modify: `routes.go` — `setupRoutes` 의 `return router` 직전 (line 54 부근)
- Create: `web/index.html`, `web/style.css`, `web/app.js` (스켈레톤)
- Create: `static_routes_test.go` (package main)

**Interfaces:**
- Produces: `GET /` → `web/index.html`, `GET /static/*` → `web/` 정적 파일. 이후 프론트 태스크가 동일 파일을 채운다.

- [ ] **Step 1: 실패하는 라우트 테스트 작성**

`static_routes_test.go` 생성:
```go
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestStaticRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	app := NewAppServer(NewXiaohongshuService())
	router := setupRoutes(app)

	t.Run("루트에서 index.html 제공", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		require.Contains(t, rr.Header().Get("Content-Type"), "text/html")
		require.Contains(t, rr.Body.String(), "<!doctype html>")
	})

	t.Run("/static/app.js 제공", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/static/app.js", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
	})
}
```

- [ ] **Step 2: 테스트 실패 확인**

Run: `go test ./ -run TestStaticRoutes -v`
Expected: FAIL — `/` 라우트 없음(404) 또는 `web/` 파일 없음.

- [ ] **Step 3: 웹 스켈레톤 파일 생성**

`web/index.html`:
```html
<!doctype html>
<html lang="ko">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>샤오홍슈 영상 검색</title>
  <link rel="stylesheet" href="/static/style.css">
</head>
<body>
  <header class="header">
    <h1>샤오홍슈 영상 검색</h1>
    <p class="subtitle">중국어 검색어로 영상을 찾아보세요</p>
  </header>
  <main id="app">
    <!-- 로그인 섹션 -->
    <section id="login-section" class="hidden">
      <button id="login-btn">QR 코드로 로그인</button>
      <img id="qrcode-img" alt="로그인 QR" hidden>
      <p id="login-status"></p>
    </section>
    <!-- 검색 섹션 -->
    <section id="search-section">
      <form id="search-form">
        <input id="keyword" type="text" placeholder="중국어 검색어 입력 (예: 美食 · 旅行 · 化妆)" autocomplete="off" required>
        <button type="submit">검색</button>
      </form>
      <p id="status"></p>
      <div id="results" class="grid"></div>
    </section>
    <!-- 영상 모달 -->
    <div id="video-modal" class="modal hidden">
      <div class="modal-body">
        <button id="modal-close" class="modal-close" aria-label="닫기">×</button>
        <video id="video-player" controls playsinline></video>
        <button id="copy-url-btn">URL 복사</button>
      </div>
    </div>
  </main>
  <script src="/static/app.js"></script>
</body>
</html>
```

`web/style.css` (최소 — Task 4에서 확장): `/* Task 4 에서 채움 */`

`web/app.js` (빈): `// Task 5 부터 채움`

- [ ] **Step 4: 라우트 추가**

`routes.go` 의 `setupRoutes` 에서 `return router` 직전에 추가:
```go
	// 한국어 검색 페이지 정적 서빙 (같은 출처)
	router.Static("/static", "./web")
	router.GET("/", func(c *gin.Context) {
		c.File("./web/index.html")
	})

	return router
}
```

- [ ] **Step 5: 테스트 통과 확인**

Run: `go test ./ -run TestStaticRoutes -v`
Expected: PASS (두 서브테스트).

- [ ] **Step 6: 포맷 + 전체 빌드/테스트**

Run: `gofmt -w routes.go static_routes_test.go && go build ./... && go test ./`
Expected: 빌드 성공, root 패키지 테스트 통과.

- [ ] **Step 7: 커밋**

```bash
git add routes.go static_routes_test.go web/
git commit -m "feat(web): serve Korean search page and add static routes

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Task 4: 스타일시트 작성

**Files:**
- Modify: `web/style.css` (전체 교체)

- [ ] **Step 1: style.css 전체 작성**

`web/style.css` 를 아래로 교체:
```css
:root {
  --red: #ff2442;
  --bg: #f7f7f8;
  --card: #ffffff;
  --text: #222;
  --muted: #888;
  --border: #eee;
}

* { box-sizing: border-box; }

body {
  margin: 0;
  font-family: -apple-system, "Apple SD Gothic Neo", "Malgun Gothic", sans-serif;
  background: var(--bg);
  color: var(--text);
}

.header {
  text-align: center;
  padding: 24px 16px 8px;
}
.header h1 { margin: 0; font-size: 22px; color: var(--red); }
.subtitle { margin: 6px 0 0; color: var(--muted); font-size: 14px; }

#app { max-width: 1100px; margin: 0 auto; padding: 16px; }

#search-form {
  display: flex;
  gap: 8px;
  margin-bottom: 12px;
}
#keyword {
  flex: 1;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: 10px;
  font-size: 15px;
}
#search-form button {
  padding: 12px 20px;
  border: none;
  border-radius: 10px;
  background: var(--red);
  color: #fff;
  font-size: 15px;
  cursor: pointer;
}

#status { color: var(--muted); padding: 8px 0; min-height: 20px; }

.spinner {
  display: inline-block;
  width: 16px; height: 16px;
  border: 2px solid var(--border);
  border-top-color: var(--red);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
  vertical-align: middle;
  margin-right: 6px;
}
@keyframes spin { to { transform: rotate(360deg); } }

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 16px;
}

.card {
  background: var(--card);
  border: 1px solid var(--border);
  border-radius: 12px;
  overflow: hidden;
  cursor: pointer;
  transition: transform 0.15s;
}
.card:hover { transform: translateY(-2px); }
.card-cover {
  position: relative;
  width: 100%;
  aspect-ratio: 4 / 3;
  object-fit: cover;
  background: #eee;
  display: block;
}
.badge-video {
  position: absolute;
  left: 8px; bottom: 8px;
  background: rgba(0,0,0,0.6);
  color: #fff;
  font-size: 12px;
  padding: 2px 8px;
  border-radius: 10px;
}
.card-body { padding: 10px 12px; }
.card-title {
  font-size: 14px;
  margin: 0 0 8px;
  line-height: 1.35;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.card-meta { font-size: 12px; color: var(--muted); display: flex; flex-direction: column; gap: 4px; }

#login-section {
  text-align: center;
  padding: 32px 16px;
}
#login-btn {
  padding: 12px 20px;
  border: none;
  border-radius: 10px;
  background: var(--red);
  color: #fff;
  font-size: 15px;
  cursor: pointer;
}
#qrcode-img { display: block; margin: 16px auto; max-width: 240px; }
#login-status { color: var(--muted); }

.modal {
  position: fixed;
  inset: 0;
  background: rgba(0,0,0,0.7);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 100;
}
.modal.hidden { display: none; }
.modal-body {
  background: #000;
  border-radius: 12px;
  padding: 12px;
  max-width: 90vw;
  position: relative;
}
#video-player { max-width: 80vw; max-height: 75vh; display: block; background: #000; }
.modal-close {
  position: absolute;
  top: -14px; right: -14px;
  width: 32px; height: 32px;
  border-radius: 50%;
  border: none;
  background: #fff;
  font-size: 18px;
  cursor: pointer;
}
#copy-url-btn {
  margin-top: 10px;
  padding: 8px 14px;
  border: 1px solid #fff;
  background: transparent;
  color: #fff;
  border-radius: 8px;
  cursor: pointer;
}

.hidden { display: none !important; }
```

- [ ] **Step 2: 수동 검증**

Run: `go run .` (저장소 루트에서). 브라우저로 `http://localhost:18060/` 열기.
Expected: 한국어 헤더·검색바가 스타일 적용되어 표시. 콘솔 에러 없음. (`Ctrl+C` 로 서버 중지.)

- [ ] **Step 3: 커밋**

```bash
git add web/style.css
git commit -m "style(web): add Korean UI styling for search page

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Task 5: app.js — API 클라이언트 + 검색 + 카드 렌더

**Files:**
- Modify: `web/app.js` (전체 교체, 빈 파일 → 첫 구현)

- [ ] **Step 1: app.js 전체 작성**

`web/app.js` 를 아래로 교체:
```js
// ====== API 클라이언트 (같은 출처) ======
const api = {
  async loginStatus() {
    const r = await fetch("/api/v1/login/status");
    return r.json();
  },
  async loginQrcode() {
    const r = await fetch("/api/v1/login/qrcode");
    return r.json();
  },
  async search(keyword) {
    const r = await fetch("/api/v1/feeds/search", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ keyword, filters: { note_type: "视频" } }),
    });
    return r.json();
  },
  async feedDetail(feedId, xsecToken) {
    const r = await fetch("/api/v1/feeds/detail", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        feed_id: feedId,
        xsec_token: xsecToken,
        load_all_comments: false,
      }),
    });
    return r.json();
  },
};

// ====== 상태 ======
const state = { loggedIn: false, username: "", loading: false };

// ====== DOM 헬퍼 ======
const $ = (id) => document.getElementById(id);
const show = (el) => el && el.classList.remove("hidden");
const hide = (el) => el && el.classList.add("hidden");

// ====== 상태 메시지 ======
function setStatus(msg, spinner) {
  const el = $("status");
  el.innerHTML = (spinner ? '<span class="spinner"></span>' : "") + (msg || "");
}

// ====== Feed 헬퍼 ======
const isVideo = (feed) => feed && feed.noteCard && feed.noteCard.type === "video";
const pickCover = (feed) => {
  const c = feed.noteCard.cover || {};
  return c.urlDefault || c.urlPre || "";
};
const pickNickname = (feed) => {
  const u = feed.noteCard.user || {};
  return u.nickname || u.nickName || "";
};

// ====== 카드 렌더 ======
function cardHTML(feed) {
  const i = feed.noteCard.interactInfo || {};
  const meta = [
    `❤ ${i.likedCount || 0}`,
    `💬 ${i.commentCount || 0}`,
    `🔖 ${i.collectedCount || 0}`,
  ].join(" · ");
  const cover = pickCover(feed);
  const nick = pickNickname(feed);
  return `
    <article class="card" data-id="${feed.id}" data-token="${feed.xsecToken || ""}">
      <div style="position:relative">
        ${cover ? `<img class="card-cover" src="${cover}" alt="" loading="lazy">` : `<div class="card-cover"></div>`}
        <span class="badge-video">▶ 영상</span>
      </div>
      <div class="card-body">
        <p class="card-title">${escapeHtml(feed.noteCard.displayTitle || "")}</p>
        <div class="card-meta"><span>${escapeHtml(nick)}</span><span>${meta}</span></div>
      </div>
    </article>`;
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[c]));
}

function renderCards(feeds) {
  const videos = (feeds || []).filter(isVideo);
  const grid = $("results");
  if (videos.length === 0) {
    grid.innerHTML = "";
    setStatus("검색 결과가 없습니다.");
    return;
  }
  setStatus(`영상 결과 ${videos.length}개`);
  grid.innerHTML = videos.map(cardHTML).join("");
}

// ====== 검색 ======
async function doSearch(keyword) {
  state.loading = true;
  setStatus("검색 중…", true);
  try {
    const res = await api.search(keyword);
    if (!res.success) {
      setStatus("오류가 발생했습니다: " + (res.message || ""));
      return;
    }
    renderCards(res.data && res.data.feeds);
  } catch (e) {
    setStatus("서버에 연결할 수 없습니다. 서버가 실행 중인지 확인해주세요.");
  } finally {
    state.loading = false;
  }
}

// ====== 초기화 ======
async function init() {
  // Task 6 에서 로그인 패널로 확장됨. 우선 미로그인이면 안내만.
  try {
    const res = await api.loginStatus();
    state.loggedIn = res.data && res.data.is_logged_in;
    state.username = (res.data && res.data.username) || "";
  } catch (e) {
    state.loggedIn = false;
  }
  if (!state.loggedIn) {
    setStatus("로그인이 필요합니다.");
  }

  $("search-form").addEventListener("submit", (ev) => {
    ev.preventDefault();
    if (!state.loggedIn) {
      setStatus("로그인이 필요합니다.");
      return;
    }
    const kw = $("keyword").value.trim();
    if (kw) doSearch(kw);
  });
}

document.addEventListener("DOMContentLoaded", init);
```

- [ ] **Step 2: 수동 검증 (로그인은 별도 수행)**

Run: `go run .` (저장소 루트). 먼저 로그인: 다른 터미널에서 `go run ./cmd/login` (또는 `curl http://localhost:18060/api/v1/login/qrcode` 로 QR 획득 후 샤오홍슈 앱 스캔). 로그인 후 브라우저에서 `http://localhost:18060/` 열고 `美食` 검색.
Expected: "영상 결과 N개" 표시 + 영상 카드 그리드 렌더(표지·제목·작성자·좋아요/댓글/저장). 비디오 타입만 표시.

- [ ] **Step 3: 커밋**

```bash
git add web/app.js
git commit -m "feat(web): add search and video card rendering

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Task 6: app.js — 로그인 패널 (QR + 폴링)

**Files:**
- Modify: `web/app.js` — 로그인 함수 추가 + `init` 확장

**Interfaces:**
- Consumes: Task 5 의 `api.loginStatus()`, `api.loginQrcode()`, `$`, `show`, `hide`, `state`.
- Produces: `startLogin()`, `pollLoginStatus()`; `#login-btn`/`#qrcode-img`/`#login-status` 동작.

- [ ] **Step 1: app.js 에 로그인 섹션 추가**

`web/app.js` 의 `// ====== 초기화 ======` 주석 **앞**에 아래 블록을 삽입:
```js
// ====== 로그인 ======
function renderLoginState() {
  if (state.loggedIn) {
    hide($("login-section"));
    show($("search-section"));
    setStatus(state.username ? `로그인됨: ${state.username}` : "");
  } else {
    show($("login-section"));
    $("login-status").textContent = "QR 코드로 로그인 버튼을 눌러주세요.";
    $("qrcode-img").hidden = true;
  }
}

let pollTimer = null;
async function startLogin() {
  $("login-status").textContent = "QR 코드를 가져오는 중…";
  const res = await api.loginQrcode();
  if (!res.success) {
    $("login-status").textContent = "오류: " + (res.message || "");
    return;
  }
  const data = res.data || {};
  if (data.is_logged_in) {
    state.loggedIn = true;
    renderLoginState();
    return;
  }
  // img 가 data: 접두사 없는 순수 base64 면 보정
  let src = data.img || "";
  if (src && !src.startsWith("data:")) src = "data:image/png;base64," + src;
  $("qrcode-img").src = src;
  $("qrcode-img").hidden = false;
  $("login-status").textContent = "샤오홍슈 앱으로 아래 QR을 스캔하세요.";
  pollLoginStatus();
}

async function pollLoginStatus() {
  if (pollTimer) clearTimeout(pollTimer);
  const res = await api.loginStatus();
  if (res.data && res.data.is_logged_in) {
    state.loggedIn = true;
    state.username = (res.data && res.data.username) || "";
    renderLoginState();
    return;
  }
  pollTimer = setTimeout(pollLoginStatus, 2000);
}
```

- [ ] **Step 2: init() 확장 — 로그인 분기 + 버튼 바인딩**

`web/app.js` 의 기존 `async function init()` 전체를 아래로 교체:
```js
// ====== 초기화 ======
async function init() {
  try {
    const res = await api.loginStatus();
    state.loggedIn = res.data && res.data.is_logged_in;
    state.username = (res.data && res.data.username) || "";
  } catch (e) {
    state.loggedIn = false;
  }
  renderLoginState();

  $("login-btn").addEventListener("click", startLogin);

  $("search-form").addEventListener("submit", (ev) => {
    ev.preventDefault();
    if (!state.loggedIn) {
      setStatus("로그인이 필요합니다.");
      return;
    }
    const kw = $("keyword").value.trim();
    if (kw) doSearch(kw);
  });
}

document.addEventListener("DOMContentLoaded", init);
```

- [ ] **Step 3: 수동 검증**

Run: `go run .`. 쿠키 삭제 후 재시작(`rm -f cookies.json` 후 `go run .`). 브라우저에서 `http://localhost:18060/` 열기.
Expected: 로그인 섹션 표시 → "QR 코드로 로그인" 클릭 → QR 이미지 표시 + "샤오홍슈 앱으로 아래 QR을 스캔하세요" → 샤오홍슈 앱으로 스캔 → 자동으로 검색 화면 전환 + "로그인됨: {username}".

- [ ] **Step 4: 커밋**

```bash
git add web/app.js
git commit -m "feat(web): add QR login panel with status polling

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Task 7: app.js — 영상 재생 모달 + URL 복사

**Files:**
- Modify: `web/app.js` — 모달 함수 추가 + 카드 클릭/모달 버튼 바인딩

**Interfaces:**
- Consumes: Task 5 의 `api.feedDetail()`, `state`, `$`. Task 5/6 의 DOM id.
- Produces: `playFeed(feed)`, `openVideoModal(url)`, `closeVideoModal()`, `copyVideoUrl()`; `#video-modal` 동작.

- [ ] **Step 1: app.js 에 모달 섹션 추가**

`web/app.js` 의 `// ====== 초기화 ======` 주석 **앞**에 아래 블록을 삽입 (Task 6 블록 뒤):
```js
// ====== 영상 재생 모달 ======
async function playFeed(feed) {
  setStatus("영상 불러오는 중…", true);
  try {
    const res = await api.feedDetail(feed.id, feed.xsecToken);
    if (!res.success || !(res.data && res.data.video_url)) {
      setStatus("영상 URL을 가져오지 못했습니다.");
      return;
    }
    openVideoModal(res.data.video_url);
    setStatus("");
  } catch (e) {
    setStatus("영상을 불러오는 중 오류가 발생했습니다.");
  }
}

let currentVideoUrl = "";
function openVideoModal(url) {
  currentVideoUrl = url;
  const v = $("video-player");
  v.src = url;
  show($("video-modal")); // .hidden 제거로 모달 표시
  v.play().catch(() => {});
}
function closeVideoModal() {
  const v = $("video-player");
  v.pause();
  v.removeAttribute("src");
  v.load();
  hide($("video-modal"));
}
async function copyVideoUrl() {
  try {
    await navigator.clipboard.writeText(currentVideoUrl);
    $("copy-url-btn").textContent = "복사됨 ✓";
    setTimeout(() => ($("copy-url-btn").textContent = "URL 복사"), 1500);
  } catch (e) {
    prompt("이 URL을 복사하세요:", currentVideoUrl);
  }
}
```

- [ ] **Step 2: init() 에 카드 클릭 + 모달 버튼 바인딩 추가**

`init()` 안의 `$("search-form").addEventListener(...)` 블록 **뒤**에 추가:
```js
  // 결과 그리드 클릭 위임 → 카드 재생
  $("results").addEventListener("click", (ev) => {
    const card = ev.target.closest(".card");
    if (!card) return;
    playFeed({
      id: card.getAttribute("data-id"),
      xsecToken: card.getAttribute("data-token"),
    });
  });

  // 모달 버튼
  $("modal-close").addEventListener("click", closeVideoModal);
  $("copy-url-btn").addEventListener("click", copyVideoUrl);
  $("video-modal").addEventListener("click", (ev) => {
    if (ev.target === $("video-modal")) closeVideoModal();
  });
  document.addEventListener("keydown", (ev) => {
    if (ev.key === "Escape") closeVideoModal();
  });
```

- [ ] **Step 3: 수동 검증**

Run: `go run .`. 로그인 후 `美食` 검색 → 영상 카드 클릭.
Expected: 모달 열림 + 영상 재생(`video_url` 로부터). "URL 복사" 클릭 → "복사됨 ✓" + 클립보드에 mp4 URL 복사. `Esc` 또는 바깥 영역 클릭/`×` 로 모달 닫힘.

- [ ] **Step 4: 커밋**

```bash
git add web/app.js
git commit -m "feat(web): add video playback modal with URL copy

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Task 8: 통합 검증 + 마무리

**Files:** (수정 없음, 검증 전용)

- [ ] **Step 1: 전체 빌드 + 테스트 + 포맷**

Run: `gofmt -w *.go xiaohongshu/*.go && go vet ./... && go build ./... && go test ./...`
Expected: 모든 명령 성공, 테스트 전부 PASS, vet 경고 없음.

- [ ] **Step 2: 통합 시나리오 수행**

Run: `go run .` (저장소 루트). `http://localhost:18060/` 에서 아래 체크:
- [ ] 미로그인 시 로그인 섹션 표시
- [ ] "QR 코드로 로그인" → QR 표시 → 앱 스캔 → 자동 전환 + "로그인됨: {username}"
- [ ] 중국어 키워드(예: `美食`) 검색 → "영상 결과 N개" + 카드 그리드
- [ ] 영상 타입만 표시(이미지 노트 섞이지 않음)
- [ ] 카드 클릭 → 모달 재생 + "URL 복사" 정상
- [ ] 빈 결과 키워드 → "검색 결과가 없습니다."
- [ ] 서버 중지 후 검색 시도 → "서버에 연결할 수 없습니다."

- [ ] **Step 3: 영상 URL 경로 실제 확인 (spec §8/§11)**

검색 → 재생 시 백엔드 로그/응답에서 `video_url` 이 비어있지 않은지 확인. **비어 있으면**(상태에 URL 없는 케이스) → spec §4.2 백업 수단(rod 네트워크 인터셉트) 도입을 사용자에게 보고.

- [ ] **Step 4: 최종 커밋 (있을 경우) + 상태 보고**

변경분이 남아 있지 않다면 스킵. 결과 요약: 구현 완료 브랜치 `feat/kr-video-search`, 푸시는 사용자 동의 시에만.

---

## Self-Review 결과 (작성자 점검)

- **Spec 커버리지**: 백엔드 정적 라우트(T3), 영상 URL 추출(T1/T2), 프론트 전체(헤더/검색바/로그인/상태/카드/모달 — T3-T7), 검증(T8). Phase 2(풀 상세/정렬/페이지네이션)은 명시적 제외. ✅
- **Placeholder**: 없음 — 모든 코드 블록 완결. ✅
- **타입/이름 일치**: DOM id 표와 app.js 사용 id 일치(`login-section` 등). `VideoURL()` 메서드명 T1 정의 → T2 사용 일치. `res.data.video_url`/`res.data.feeds` 계약 일치. ✅
- **주의**: 모달은 `.hidden` 토글로만 열고 닫는다 (`.open` 클래스 미사용).
