# 샤오홍슈 영상 검색 페이지 (한국어 UI) — 설계 문서

- 날짜: 2026-07-18
- 상태: 승인됨 → 구현 계획 대기
- 대상 프로젝트: `xiaohongshu-mcp` (Go MCP + HTTP REST API 서버)

---

## 1. 배경 및 목표

사용자는 xiaohongshu-mcp 서비스를 활용해 **한국어 UI의 영상 검색 페이지**를 만들고자 한다.
사용자가 **중국어 검색어**를 입력하면, 샤오홍슈에서 해당 키워드로 검색된 **영상(video)** 결과를
웹 페이지에서 카드 목록으로 보고, 선택한 영상을 **재생(직접 mp4 URL 확보)** 할 수 있어야 한다.

> "한글화"는 기존 문자열을 번역하는 것이 아니라, **한국어 UI를 새로 구축**하는 것으로 달성한다.
> 따라서 MCP 메타데이터(도구 설명/jsonschema)·README·API 문서의 번역은 본 범위에서 제외한다.
> (이유: 웹 페이지는 MCP 프로토콜이 아닌 HTTP REST API를 직접 호출하므로, MCP 메타데이터가
> 웹 사용자에게 전혀 노출되지 않기 때문)

## 2. 범위

### In scope (MVP)
- Go 서버에 정적 파일 서빙 라우트 추가 → 단일 서버에서 API + 한국어 웹 페이지 제공
- 한국어 UI: 헤더, 검색바(중국어 입력), 로그인 패널(QR + 폴링), 로딩/빈/에러 상태
- 영상 카드 결과 목록 렌더 (표지·제목·작성자·좋아요/댓글/저장 수)
- 카드 "재생" 버튼 → 상세 API 호출 → **영상 mp4 URL 확보** → 모달 재생 + "URL 복사"
- 백엔드: 상세 스크래퍼에 영상 URL 추출 추가 (구조체 + 추출 코드)

### Out of scope (Phase 2 후보)
- 풀 상세 화면(본문 전문·이미지 목록·댓글 리스트 전체 보기)
- 정렬 필터 드롭다운(최신/인기), 페이지네이션/"더 보기"
- 결과 콘텐츠(제목·본문)의 한국어 번역
- 전화/SMS 자동 로그인, 다중 계정
- 배포용 Docker 이미지 변경(단일 바이너리 embed 등)

## 3. 아키텍처 개요

```
┌──────────────────────────────────────────────────────────┐
│  xiaohongshu-mcp Go 서버  (http://localhost:18060)       │
│                                                          │
│  /api/v1/*      기존 HTTP REST API (변경 없음 + α)        │
│  /mcp, /health  기존 (변경 없음)                          │
│  /              [신규] web/index.html 서빙               │
│  /static/*      [신규] web/style.css, web/app.js 서빙    │
│                                                          │
│  브라우저 자동화(go-rod) + cookies.json 자동 로드/저장    │
└──────────────────────────────────────────────────────────┘
              ▲ same-origin fetch (CORS 불필요)
              │
┌──────────────────────────────────────────────────────────┐
│  웹 페이지 (vanilla HTML/CSS/JS, 빌드 없음)              │
│  web/index.html · web/style.css · web/app.js             │
└──────────────────────────────────────────────────────────┘
```

- 같은 출처(same-origin)이므로 CORS 설정이나 API 주소 설정이 필요 없다.
- 프론트엔드는 프레임워크/빌드 도구 없이 순수 HTML/CSS/JS.

## 4. 백엔드 변경 (최소)

### 4.1 정적 파일 서빙 (`routes.go`)
`setupRoutes` 안, `return router` 직전에 추가:

```go
// 한국어 검색 페이지 정적 서빙
router.Static("/static", "./web")
router.GET("/", func(c *gin.Context) {
    c.File("./web/index.html")
})
```

- `/static` 접두사만 점유하므로 기존 `/api/v1/*`, `/mcp`, `/health`와 충돌 없음.
- `index.html`은 `/static/style.css`, `/static/app.js`를 참조.

### 4.2 영상 URL 추출 (`xiaohongshu/feed_detail.go` + `xiaohongshu/types.go`)

현재 `extractFeedDetail`은 `__INITIAL_STATE__.note.noteDetailMap[feedID]` 전체를 읽지만,
`FeedDetail` 구조체에 영상 필드가 없어 URL이 버려지고 있다.

변경:
1. `xiaohongshu/types.go`의 `FeedDetail`에 영상 필드 추가:
   ```go
   type FeedDetail struct {
       // ...기존 필드...
       Video *DetailVideo `json:"video,omitempty"`
   }

   type DetailVideo struct {
       Media DetailMedia `json:"media"`
   }

   type DetailMedia struct {
       // stream: { h264: [{masterUrl}], h265: [...], av1: [...] }
       Stream map[string][]struct {
           MasterURL string `json:"masterUrl"`
       } `json:"stream"`
   }
   ```
2. 영상 URL을 평탄화하는 헬퍼:
   ```go
   // VideoURL은 h264 > h265 > av1 순으로 첫 masterUrl을 반환한다.
   func (v *DetailVideo) VideoURL() string { ... }
   ```
3. HTTP 응답에서 프론트가 간단히 쓰도록 `main.FeedDetailResponse`에 평탄화된 필드 추가:
   ```go
   type FeedDetailResponse struct {
       FeedID   string `json:"feed_id"`
       Data     any    `json:"data"`
       VideoURL string `json:"video_url,omitempty"` // [신규]
   }
   ```
   `GetFeedDetailWithConfig`에서 `result.Note.Video`가 존재하면 `VideoURL` 채움.

- 예상 JSON 경로: `note.video.media.stream.h264[0].masterUrl` (fallback `h265`, `av1`).
- **첫 실제 실행 시 실제 데이터 구조에 맞춰 경로 확정.** 스크래퍼가 이미 전체 noteDetailMap을
  덤프하므로 로그 한 번이면 확인 가능.
- **백업 수단**: 상태에 URL이 없는 케이스 발생 시 → rod 네트워크 인터셉트로 `.mp4` 요청 캡처
  (Phase 1.5, 필요 시에만).

## 5. API 계약 (프론트엔드 사용 분)

모두 동일 출처 `http://localhost:18060`. 공통 응답 래퍼: `{ success: bool, data: any, message: string }`.

### 5.1 로그인 상태
- `GET /api/v1/login/status` → `data: { is_logged_in: bool, username: string }`

### 5.2 로그인 QR
- `GET /api/v1/login/qrcode` → `data: { timeout: string, is_logged_in: bool, img: string(base64, data URI prefix 포함) }`
- 호출 시 백엔드가 최대 4분간 로그인 대기 고루틴을 띄우고 완료되면 쿠키 저장. 프론트는 상태를 폴링.

### 5.3 검색
- `POST /api/v1/feeds/search`
- 요청: `{ "keyword": "<중국어>", "filters": { "note_type": "视频" } }`
- `data: { feeds: [Feed], count: int }`
- `Feed` 주요 필드: `id`, `xsecToken`, `noteCard.{ type, displayTitle, user.{nickname,nickName,avatar}, cover.urlDefault, interactInfo.{likedCount,commentCount,collectedCount} }`

### 5.4 상세 (영상 URL 확보용)
- `POST /api/v1/feeds/detail`
- 요청: `{ "feed_id": "...", "xsec_token": "...", "load_all_comments": false }`
- `data: { feed_id, data: { note, comments }, video_url }` ← `video_url`이 [신규] 영상 직링크

## 6. 프론트엔드 설계

### 6.1 파일 구성 (`web/`)
- `index.html` — 마크업 (헤더, 검색바, 결과 영역, 로그인 영역, 모달)
- `style.css` — 스타일 (반응형 카드 그리드, 포인트 컬러 빨강 `#ff2442`, 중립 배경)
- `app.js` — 로직. 내부 모듈:
  - `api`: `loginStatus()`, `loginQrcode()`, `search(keyword)`, `feedDetail(feedId, xsecToken)`
  - `state`: `{ loggedIn, username, results, loading }`
  - `render`: `renderLogin()`, `renderResults()`, `renderCard(feed)`, `openVideoModal(url)`, `setStatus()`
  - 이벤트 바인딩 (검색 submit, 카드 클릭, 모달 닫기, URL 복사)

### 6.2 화면 / 상태 (한국어 카피)
| 영역 | 한국어 문구 |
|---|---|
| 헤더 제목 | 샤오홍슈 영상 검색 |
| 헤더 안내 | 중국어 검색어로 영상을 찾아보세요 |
| 검색 placeholder | 중국어 검색어 입력 (예: 美食 · 旅行 · 化妆) |
| 검색 버튼 | 검색 |
| 로그인 버튼 | QR 코드로 로그인 |
| 로그인 안내 | 샤오홍슈 앱으로 아래 QR을 스캔하세요 |
| 로그인 성공 | 로그인됨: {username} |
| 로딩 | 검색 중… |
| 결과 없음 | 검색 결과가 없습니다. |
| 결과 수 | 영상 결과 {N}개 |
| 에러 | 오류가 발생했습니다: {msg} / 로그인이 필요합니다 |
| 카드 영상 배지 | ▶ 영상 |
| 모달 | 닫기 · URL 복사 |

### 6.3 핵심 흐름
1. 페이지 로드 → `loginStatus()` 호출.
   - 미로그인 → 로그인 패널 노출. "QR 코드로 로그인" 클릭 → `loginQrcode()` → QR 이미지 표시 →
     2초 간격 `loginStatus()` 폴링 → `is_logged_in` true 시 검색 화면으로 전환.
   - **QR 이미지 보정**: `img` 값이 `data:`로 시작하지 않으면(순수 base64인 경우)
     `data:image/png;base64,` 접두사를 붙여 `<img src>`에 적용.
   - 로그인됨 → 검색 화면.
2. 검색어 입력(중국어) + submit → `search(keyword)` → 응답 `feeds`에서
   `noteCard.type === "video"`만 필터(안전망) → 카드 렌더.
3. 카드 "재생" 클릭 → `feedDetail(feed.id, feed.xsecToken)` → `data.video_url` 획득 →
   모달 `<video src=video_url controls autoplay>` + "URL 복사" 버튼.
4. 빈 결과·에러·네트워크 오류 각각 한국어 상태 메시지.

## 7. 에러 처리
- API 응답 `success === false` → `message` 표시(한국어 래핑). 코드별 매핑:
  - `MISSING_KEYWORD` → "검색어를 입력해주세요."
  - `SEARCH_FEEDS_FAILED` / `GET_FEED_DETAIL_FAILED` → "검색/조회에 실패했습니다. 잠시 후 다시 시도해주세요."
  - 그 외 → "오류가 발생했습니다."
- 네트워크 오류(fetch reject) → "서버에 연결할 수 없습니다. 서버가 실행 중인지 확인해주세요."
- 미로그인으로 검색 시도 → 백엔드가 에러를 반환할 수 있으므로, 사전에 `loginStatus()`로 가드.

## 8. 알려진 한계 / 리스크
- **로그인 불가피**: 샤오홍슈는 계정 로그인 필수, 퍼블릭 API 없음. 단, 운영자 1회 QR 로그인 후
  `cookies.json`이 자동 로드/재사용되므로 방문자 UX엔 영향 없음. 쿠키는 며칠~몇 주 뒤 만료 → 재로그인.
- **영상 URL 경로 미확정**: `note.video.media.stream` 경로는 첫 실제 실행 시 확정. 실패 시 네트워크 인터셉트 백업.
- **상세 호출 비용**: 영상 재생 시마다 상세 스크래핑(브라우저 자동화)이 동작하므로 수 초 소요. 로딩 표시 필수.
- **클라이언트 필터 보조**: 백엔드 `note_type:"视频"` 필터가 정상 동작하더라도, 안전망으로 클라이언트에서
  `type!=="video"`는 제외. (영상이 아닌 결과가 섞이는 것 방지)

## 9. 검증 계획
1. `go build ./...` 통과 (라우트/구조체 변경 후 컴파일 확인)
2. `go vet ./...` 경고 없음
3. 서버 기동 후 `http://localhost:18060/` 접속 → 한국어 페이지 정상 노출
4. 미로그인 상태에서 로그인 패널 동작 (QR 표시 → 스캔 → 상태 전환)
5. 중국어 키워드(예: 美食) 검색 → 영상 카드 목록 렌더 (`type==="video"`만)
6. 카드 "재생" 클릭 → 모달에서 영상 재생 + URL 복사 동작
7. 빈 결과 키워드 / 네트워크 단절 시 한국어 상태 메시지 정상

## 10. 마일스톤
1. 백엔드: 정적 라우트 추가 + `go build` 검증
2. 백엔드: 영상 URL 추출 (구조체 + 헬퍼 + 응답 필드) + `go build`/`go vet`
3. 프론트: HTML/CSS 뼈대 + 검색 → 카드 렌더 (로그인은 수동 전제)
4. 프론트: 로그인 패널(QR + 폴링)
5. 프론트: 로딩/빈/에러 상태 + 영상 필터 안전망
6. 프론트: 영상 재생 모달 + URL 복사 (상세 API 연동)
7. 통합 검증(위 9단계) + 폴리싱

## 11. 결정 사항 / 보류
- **코드 주석 언어 (결정)**: 신규 코드 주석은 **한국어**로 작성 (사용자 결정). 기존 중국어 주석은 건드리지 않는다.
- **영상 URL 확보 실패 시 폴백**(네트워크 인터셉트) 도입 여부 → 첫 실행 결과 보고 후 결정.
