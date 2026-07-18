# 크로스플랫폼 참고영상 통합 검색 — M1 구현 계획 (rev 3, 승인본)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. **각 태스크는 [Task 0 의 브랜치 위에서] implementer → spec reviewer → code-quality reviewer → 수정 → 검증 순서로 실행**한다(아래 "Per-Task Execution Workflow" 참고).

**Goal:** XHS + Douyin (TikTok 토글) 통합 영상 검색을 위한 Go aggregator + 어댑터 + Python 사이드카 + 재설계된 프론트엔드를 TDD 로 구축한다 (M1: 통합 키워드 검색 + 상세필터 + 미리보기/원본). **모든 production 코드는 실제 동작해야 한다 — production TODO/빈 성공 응답 0건.**

**Architecture:** `search/` 순수 도메인 패키지(VideoItem / VideoAdapter / AggregatorService / 필터 / 머지 / sentinel 에러) → `main` 패키지 어댑터(XhsAdapter=go-rod 래핑, Douyin/TikTok=`SidecarClient` HTTP, **가용성은 사이드카 `/healthz` 조회로 판정**) → gin 핸들러 → vanilla JS 프론트(`lib.js` 순수함수 + `app.js` DOM). Python FastAPI 사이드카(`127.0.0.1:18061`)가 Douyin/TikTok **실제 검색**(Evil0ctal `abogus.py` 서명 · davidteather/TikTok-Api 7.3.3 async)을 담당. **비밀(DOUYIN_COOKIE/TT_MSTOKEN)은 Python 사이드카 env 에만 존재** — Go 는 평문을 모름.

**Tech Stack:** Go 1.24 (gin, go-rod, net/http, errors), Python FastAPI + httpx + davidteather/**TikTokApi==7.3.3** + Evil0ctal `abogus.py`(vendored) + playwright, vanilla HTML/CSS/JS (빌드 없음, Go embed same-origin). node:test (`web/lib.test.mjs`) 로 프론트 순수로직 자동 검증.

## Global Constraints

> 모든 태스크의 요구사항은 암묵적으로 이 섹션을 포함한다.

- **앱 사용자 인증 없음**: 이 로컬 앱 자체의 **회원가입/로그인은 없고 검색 시 인증을 요구하지 않는다**. 단, 플랫폼 세션까지 완전 무인증이거나 모든 콘텐츠 접근이 보장되는 것은 아님. 로컬 운영자가 **최초 1회** XHS cookies/QR · Douyin cookie · TikTok ms_token 을 준비하면 백엔드가 재사용, **만료 시에만 설정 영역**에서 갱신. 검색 UI 는 인증 화면에 막히지 않고 준비된 플랫폼은 즉시 검색, 미준비 플랫폼은 상태만 표시. **자격증명은 로컬 전용** — 로그·응답·repo commit 비노출.
- **플랫폼 세션 경계**: M1 은 익명 visitor session 자동 bootstrap 을 구현하거나 약속하지 않는다. 로컬 운영자가 XHS QR/cookies, Douyin cookie, TikTok `ms_token` 을 준비·갱신한다. 앱 사용자는 별도 계정이나 검색별 인증 없이 준비된 플랫폼을 사용하며, CAPTCHA/login wall 우회는 하지 않는다.
- **포트/bind**: Go `127.0.0.1:18060` (LAN 미공개); Python 사이드카 `127.0.0.1:18061` (외부 공개 금지, Go 전용).
- **비밀 격리**: `DOUYIN_COOKIE`·`TT_MSTOKEN`·XHS `cookies.json`(`cookies.GetCookiesFilePath()`) 은 **Python 사이드카/로컬 파일에만**. Go 는 직접 읽지 않고 사이드카 `/healthz` bool 로 가용성 판정. 평문은 query string·응답 JSON·로그·`SideResult.Error` 어디에도 노출 금지.
- **환경변수**: `SIDECAR_URL`(Go 전용, 기본 `http://127.0.0.1:18061`). 사이드카 측: `DOUYIN_COOKIE`·`TT_MSTOKEN`·(M3) `LLM_API_KEY`/`LLM_BASE`.
- **의존성 고정**: Python `TikTokApi==7.3.3`(7.x async; 진입은 `create_sessions(ms_tokens=[...])`). `abogus.py` 는 Evil0ctal repo commit **`42784ffc83a72a516bfe952153ad7e2a3998d16c`** 의 `crawlers/douyin/web/abogus.py` 를 **verbatim vendor** — 호출은 `ABogus().get_value(params)`(params=dict). **GPLv3 라이선스 + 원저작 JoeanAmier/TikTokDownloader 귀속 헤더 보존 필수**.
- **사이드카 HTTP 계약**: `POST /search` 는 **body 에 `platform`** 필드 포함(쿼리 파라미터 아님). 응답은 `{success,data:{videos,next_cursor,has_more}}` 래핑. `GET /healthz` → `{status:"ok",douyin,tiktok,llm}`.
- **정렬 키**: `"relevance"` | `"popularity"` | `"latest"` (그 외 → 400, 폴백 없음). **잘못된 `platforms` 값도 400**.
- **플랫폼명/초기 선택**: `"xiaohongshu"` | `"douyin"` | `"tiktok"`. **초기 플랫폼 칩 = XHS+Douyin on, TikTok off**(스펙; '전체' 토글 시 3개).
- **merge 규칙**: raw 지표 직접 비교 금지. adapter 입력 순서를 보존해 within-platform rank `r`(0부터) → score `1/(r+1)` → 동일 rank 는 XHS→Douyin→TikTok 라운드로빈. **merge 단계 재정렬 금지**. **cross-platform dedupe 금지**(플랫폼 내부 dedupe 만).
- **타임아웃**: 어댑터별 XHS 60s / Douyin 45s / TikTok 45s; AggregatorService 상위 75s; 사이드카 내부 HTTP 20s.
- **에러 sanitize**: `SideResult.Error` 는 `err.Error()` 원문 대신 고정 안전 메시지(`search.SideErrorMessage` 매핑). **어댑터 검색 오류 시 `SideResult.Available.Available=false`**.
- **UI 언어**: 한국어 chrome. 결과 제목·작성자·문구는 **원문(번역 X)**. 검색 UI 는 **항상 표시**. 카드/모달에 **원본 링크 상시 노출**(재생 실패 시 원본 열기 fallback).
- **비영속**: 검색 응답은 메모리에서만. 디스크/DB 기록 금지.
- **지표 null 규칙**: 미지원 지표는 항상 `null` 키(필드 생략 아님).
- **Go 포맷**: 모든 Go 소스 수정 후 `gofmt -w` 필수. 커밋 컨벤션 `feat(search|api|sidecar|web):`.

---

## File Structure

**Go 패키지 `search/`** (순수 도메인, fake adapter 단위 테스트):
- `search/types.go` — `VideoItem`, `SearchQuery`, `SearchFilters`, `AdapterSearchPage`, `Availability`, `SideResult`, `AggregatedResult`, `AggregatorRequest`, `VideoAdapter`.
- `search/errors.go` — sentinel `ErrUnavailable`/`ErrBadGateway`/`ErrUnreachable` + `SideErrorMessage(name, err)`(고정 안전 메시지 매핑).
- `search/normalize.go` — `int64Ptr`, `dedupeKey`, `assignDedupeKey`, `dedupeByKey`.
- `search/filter.go` — `applyPostFilters`(`date_to` 는 해당 일자 끝까지 포함).
- `search/merge.go` — `rankMerge`.
- `search/aggregator.go` — `AggregatorService`(fan-out·게이팅·타임아웃 격리, `SideErrorMessage` 로 에러 sanitize).

**`main` 패키지 신규** (어댑터 + 핸들러):
- `sidecar_client.go` — `SidecarClient`: `Search`(sentinel 매핑), `Healthz`(TTL 캐시, 503/502/네트워크 → search sentinel).
- `adapter_xhs.go` / `adapter_douyin.go` / `adapter_tiktok.go` — `search.VideoAdapter` 구현. Douyin/TikTok 의 `Available()` 는 `Healthz()` 조회.
- `capabilities.go` — 플랫폼별 capability 메타데이터.
- `handlers_search.go` — `POST /api/v1/search`, `GET /api/v1/search/capabilities` + 서버 wiring(AppServer 필드/findAdapter 포함, **구조적으로 단독 green 불가 → wiring 을 한 태스크로 통합**).

**`main` 수정**: `app_server.go`(필드), `routes.go`(라우트), `main.go`(구성 + `127.0.0.1:18060`), `static_routes_test.go`(시그니처).

**Python 사이드카 `tiktok-sidecar/`**: `app.py`(async `/search`), `models.py`, `douyin.py`(실 abogus 서명 + VIDEO_SEARCH 호추), `tiktok.py`(TikTokApi 7.3.3 async), `abogus.py`(Evil0ctal vendored), `sign.py`(abogus 래퍼, 테스트 stub 지점), `requirements.txt`, `README.md`, `tests/`.

**프론트 `web/`**: `index.html`, `lib.js`(순수 ESM 함수, node:test 대상), `lib.test.mjs`(node:test), `app.js`(DOM wiring, `lib.js` import), `style.css`. **HTML+JS 는 단독 green 불가 → 한 태스크로 통합**.

---

## Per-Task Execution Workflow (Subagent-Driven)

각 태스크는 다음 5단계로 실행한다:

1. **Implementer** — 체크박스 단계(TDD: 실패테스트 → 구현 → 통과)를 수행.
2. **Spec reviewer** — 산출물이 본 태스크의 Files/Interfaces/스펙 계약(필드명·경로·상태코드·sentinel·고정메시지)을 정확히 만족하는지 교차 검증. 위반 시 4 로.
3. **Code-quality reviewer** — CLAUDE.md(중국어 주석·간결함·gofmt), DRY/YAGNI, JS 주입 대신 go-rod 가능 여부, sanitize/SSRF/비밀노출 점검. 위반 시 4 로.
4. **수정** — 리뷰 위반사항 반영 후 재검증.
5. **검증** — `go test`/`pytest`/`node --test`/`node --check` + `go vet`/`go build` green 확인 후 커밋.

> 단독으로 green/commit 불가능한 경계는 한 태스크로 통합한다: **Task 10**(capabilities + 핸들러 + AppServer wiring), **Task 13**(HTML + JS + lib.js + node:test).

---

## Task 0: 사전 조건 및 브랜치 설정 (Subagent-Driven Step 0)

**Files:** 없음(환경 준비).

- [ ] **Step 1: 사전 조건 확인** — 현재 `feat/kr-video-search` HEAD 가 prerequisite.

```bash
cd /Users/mac/claude/shortcrawl/xiaohongshu-mcp
test "$(git rev-parse --short HEAD)" = "93d41a0" && echo "OK: feat/kr-video-search @ 93d41a0" || echo "WARN: HEAD 불일치, 확인 필요"
git status --short
# 예상: docs/superpowers/specs/2026-07-19-... 와 plans/2026-07-19-... 만 untracked
```
Expected: HEAD `93d41a0`; untracked 는 spec + plan 2개만.

- [ ] **Step 2: M1 브랜치 생성** — `feat/kr-video-search` 에서 분기.

```bash
git checkout -b feat/cross-platform-video-search-m1
```

- [ ] **Step 3: untracked spec/plan 보존 커밋** — 승인된 설계와 본 계획을 브랜치에 기록.

```bash
git add docs/superpowers/specs/2026-07-19-cross-platform-video-search-design.md \
        docs/superpowers/plans/2026-07-19-cross-platform-video-search-m1.md
git commit -m "docs: add cross-platform video search spec and M1 plan"
```

- [ ] **Step 4: clean status 확인**

```bash
git status --short   # 예상: 빈 출력(clean)
git log --oneline -1 # feat/cross-platform-video-search-m1 의 보존 커밋
```
Expected: clean working tree. 이후 Task 1 부터 착수.

---

## Task 1: VideoItem 도메인 타입 + VideoAdapter 인터페이스

**Files:**
- Create: `search/types.go`
- Test: `search/types_test.go`

**Interfaces:**
- Produces: `VideoItem`, `SearchFilters`, `SearchQuery`, `AdapterSearchPage`, `Availability`, `SideResult`, `AggregatedResult`, `AggregatorRequest`, `VideoAdapter`. 이후 모든 태스크의 기본 계약.

- [ ] **Step 1: 실패 테스트 작성** — `*int64` 지표가 `nil` 이면 JSON `null` 키로 직렬화(생략 아님).

```go
package search

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVideoItemNullMetricsSerializedAsNull(t *testing.T) {
	v := VideoItem{Platform: "xiaohongshu", PostID: "p1", Title: "t"}
	data, err := json.Marshal(v)
	require.NoError(t, err)
	s := string(data)
	for _, key := range []string{`"likes":null`, `"views":null`, `"comments":null`, `"favorites":null`, `"shares":null`} {
		require.True(t, strings.Contains(s, key), "want %s in %s", key, s)
	}
	require.False(t, strings.Contains(s, "dedupe_key"))
	require.False(t, strings.Contains(s, "rank_score"))
}

func TestVideoItemNonNullMetricSerialized(t *testing.T) {
	likes := int64(42)
	data, _ := json.Marshal(VideoItem{Platform: "douyin", PostID: "p2", Likes: &likes})
	require.True(t, strings.Contains(string(data), `"likes":42`))
}
```

- [ ] **Step 2: 테스트 실패 확인** — `go test ./search/ -run TestVideoItem -v` → FAIL.

- [ ] **Step 3: 구현** — `search/types.go`:

```go
// Package search 는 크로스플랫폼 영상 검색의 순수 도메인 모델과 aggregator 를 제공한다.
// 외부 네트워크/브라우저 의존을 두지 않아 fake adapter 로 단위 테스트가 가능하다.
package search

import "context"

// VideoItem 은 프론트와 공유하는 통합 결과 항목(JSON 계약).
// 미지원 지표는 pointer(*int64) + omitempty 미사용 → 항상 null 키로 응답.
type VideoItem struct {
	Platform      string  `json:"platform"`                  // xiaohongshu|douyin|tiktok
	PostID        string  `json:"post_id"`
	PostURL       string  `json:"post_url"`
	VideoURL      string  `json:"video_url,omitempty"`
	ThumbnailURL  string  `json:"thumbnail_url"`
	Title         string  `json:"title"`
	Description   string  `json:"description,omitempty"`
	Author        string  `json:"author"`
	PublishedAt   string  `json:"published_at,omitempty"` // RFC3339
	Duration      int     `json:"duration,omitempty"`      // 초, 0=미상
	Likes         *int64  `json:"likes"`
	Comments      *int64  `json:"comments"`
	Favorites     *int64  `json:"favorites"`
	Views         *int64  `json:"views"`   // XHS=null
	Shares        *int64  `json:"shares"`
	SourceKeyword string  `json:"source_keyword"`
	NeedsDetail   bool    `json:"needs_detail"` // true=XHS, false=Douyin/TikTok
	DetailToken   string  `json:"detail_token,omitempty"`
	DedupeKey     string  `json:"-"`
	RankScore     float64 `json:"-"`
}

type SearchFilters struct {
	IncludeKeywords  []string `json:"include_keywords"`
	ExcludeKeywords  []string `json:"exclude_keywords"`
	DateFrom         string   `json:"date_from"`
	DateTo           string   `json:"date_to"`
	DurationMin      int      `json:"duration_min"`
	DurationMax      int      `json:"duration_max"`
	MinLikes         int64    `json:"min_likes"`
	MinComments      int64    `json:"min_comments"`
	MinFavorites     int64    `json:"min_favorites"`
	MinViews         int64    `json:"min_views"`
	VideoOnly        bool     `json:"video_only"`
	PerPlatformLimit int      `json:"per_platform_limit"`
}

type SearchQuery struct {
	Keyword    string
	Sort       string
	Filters    SearchFilters
	Limit      int
	PageCursor string // opaque
}

type AdapterSearchPage struct {
	Items      []VideoItem
	NextCursor string
	HasMore    bool
}

type Availability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

type VideoAdapter interface {
	Name() string
	Available(ctx context.Context) Availability
	Search(ctx context.Context, q SearchQuery) (AdapterSearchPage, error)
}

type SideResult struct {
	Items      []VideoItem  `json:"items"`
	Error      string       `json:"error,omitempty"`
	Available  Availability `json:"available"`
	NextCursor string       `json:"next_cursor,omitempty"`
	HasMore    bool         `json:"has_more"`
}

type AggregatedResult struct {
	KeywordUsed string                `json:"keyword_used"`
	Sort        string                `json:"sort"`
	Items       []VideoItem           `json:"items"`
	Sides       map[string]SideResult `json:"sides"`
}

type AggregatorRequest struct {
	Keyword     string            `json:"keyword"`
	Platforms   []string          `json:"platforms"`
	Sort        string            `json:"sort"`
	PageCursors map[string]string `json:"page_cursors"`
	Filters     SearchFilters     `json:"filters"`
}
```

- [ ] **Step 4: 통과 확인** — `go test ./search/ -run TestVideoItem -v` → PASS.
- [ ] **Step 5: 커밋**

```bash
gofmt -w search/types.go search/types_test.go
git add search/types.go search/types_test.go
git commit -m "feat(search): add VideoItem domain types and VideoAdapter interface"
```

---

## Task 2: 정규화 헬퍼 (dedupe key, pointer)

**Files:** Create `search/normalize.go`, Test `search/normalize_test.go`.
**Interfaces:** Produces `int64Ptr`, `dedupeKey`, `assignDedupeKey`, `dedupeByKey`. Task 5 사용.

- [ ] **Step 1: 실패 테스트**

```go
package search

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDedupeKeyPrefersPostURL(t *testing.T) {
	require.Equal(t, "douyin:https://x.com/a", dedupeKey(VideoItem{Platform: "douyin", PostID: "p1", PostURL: "https://x.com/a"}))
}

func TestDedupeKeyFallsBackToPlatformPostID(t *testing.T) {
	require.Equal(t, "xiaohongshu:p1", dedupeKey(VideoItem{Platform: "xiaohongshu", PostID: "p1"}))
}

func TestDedupeByKeyKeepsFirstOccurrence(t *testing.T) {
	items := []VideoItem{
		{Platform: "douyin", PostID: "a", PostURL: "u1"},
		{Platform: "douyin", PostID: "b", PostURL: "u1"},
		{Platform: "douyin", PostID: "c", PostURL: "u2"},
	}
	got := dedupeByKey(items)
	require.Len(t, got, 2)
	require.Equal(t, "a", got[0].PostID)
	require.Equal(t, "c", got[1].PostID)
}

func TestAssignDedupeKeySetsKeywordAndKey(t *testing.T) {
	got := assignDedupeKey([]VideoItem{{Platform: "douyin", PostID: "p1"}}, "便携风扇")
	require.Equal(t, "便携风扇", got[0].SourceKeyword)
	require.Equal(t, "douyin:p1", got[0].DedupeKey)
}

func TestInt64Ptr(t *testing.T) {
	p := int64Ptr(7)
	require.NotNil(t, p)
	require.Equal(t, int64(7), *p)
}
```

- [ ] **Step 2: 실패 확인** — `go test ./search/ -run 'TestDedupe|TestAssign|TestInt64Ptr' -v` → FAIL.
- [ ] **Step 3: 구현** — `search/normalize.go`:

```go
package search

// int64Ptr 는 int64 값의 포인터를 반환한다(미지원 지표 구분용).
func int64Ptr(v int64) *int64 { return &v }

// dedupeKey 는 platform:post_url 우선, 없으면 platform:post_id. 크로스플랫폼 제거 금지.
func dedupeKey(v VideoItem) string {
	if v.PostURL != "" {
		return v.Platform + ":" + v.PostURL
	}
	return v.Platform + ":" + v.PostID
}

// assignDedupeKey 는 SourceKeyword 와 DedupeKey 를 채운다.
func assignDedupeKey(items []VideoItem, keyword string) []VideoItem {
	for i := range items {
		items[i].SourceKeyword = keyword
		items[i].DedupeKey = dedupeKey(items[i])
	}
	return items
}

// dedupeByKey 는 첫 등장 항목만 남긴다(입력 순서 보존).
func dedupeByKey(items []VideoItem) []VideoItem {
	seen := make(map[string]struct{}, len(items))
	out := make([]VideoItem, 0, len(items))
	for _, it := range items {
		if _, ok := seen[it.DedupeKey]; ok {
			continue
		}
		seen[it.DedupeKey] = struct{}{}
		out = append(out, it)
	}
	return out
}
```

- [ ] **Step 4: 통과** — `go test ./search/ -run 'TestDedupe|TestAssign|TestInt64Ptr' -v` → PASS.
- [ ] **Step 5: 커밋**

```bash
gofmt -w search/normalize.go search/normalize_test.go
git add search/normalize.go search/normalize_test.go
git commit -m "feat(search): add normalization helpers (dedupe key, pointer helper)"
```

---

## Task 3: 사후 필터 (포함/제외 키워드, 날짜, duration, 지표 임계치)

**Files:** Create `search/filter.go`, Test `search/filter_test.go`.
**Interfaces:** Consumes `VideoItem`, `SearchFilters` (Task 1). Produces `applyPostFilters`. Task 5 사용. **date_to 는 date-only 면 해당 일자 끝(23:59:59Z)까지 포함.**

- [ ] **Step 1: 실패 테스트**

```go
package search

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func ptrI(i int) *int64 { return int64Ptr(int64(i)) }

func TestApplyPostFiltersKeywordIncludeExclude(t *testing.T) {
	items := []VideoItem{
		{Platform: "douyin", PostID: "1", Title: "便携风扇 推荐", Likes: ptrI(10)},
		{Platform: "douyin", PostID: "2", Title: "桌面小风扇 避雷", Likes: ptrI(5)},
		{Platform: "douyin", PostID: "3", Title: "无关注键词", Likes: ptrI(100)},
	}
	f := SearchFilters{IncludeKeywords: []string{"风扇"}, ExcludeKeywords: []string{"避雷"}}
	got := applyPostFilters(items, f)
	require.Len(t, got, 1)
	require.Equal(t, "1", got[0].PostID)
}

func TestApplyPostFiltersDateToDateIsEndOfDay(t *testing.T) {
	// date_to 가 "2026-07-15" 이면 그날 23:59:59 까지 포함(경계 포함).
	items := []VideoItem{
		{Platform: "douyin", PostID: "a", PublishedAt: "2026-07-15T23:59:00Z"},
		{Platform: "douyin", PostID: "b", PublishedAt: "2026-07-16T00:00:01Z"},
	}
	f := SearchFilters{DateFrom: "2026-07-01", DateTo: "2026-07-15"}
	got := applyPostFilters(items, f)
	require.Len(t, got, 1)
	require.Equal(t, "a", got[0].PostID)
}

func TestApplyPostFiltersMinLikes(t *testing.T) {
	items := []VideoItem{
		{Platform: "douyin", PostID: "1", Likes: ptrI(100)},
		{Platform: "douyin", PostID: "2", Likes: ptrI(10)},
		{Platform: "douyin", PostID: "3"}, // likes=null → 제외
	}
	got := applyPostFilters(items, SearchFilters{MinLikes: 50})
	require.Len(t, got, 1)
	require.Equal(t, "1", got[0].PostID)
}

func TestApplyPostFiltersDurationRange(t *testing.T) {
	items := []VideoItem{
		{Platform: "douyin", PostID: "1", Duration: 30},
		{Platform: "douyin", PostID: "2", Duration: 120},
		{Platform: "douyin", PostID: "3", Duration: 600},
	}
	got := applyPostFilters(items, SearchFilters{DurationMin: 60, DurationMax: 300})
	require.Len(t, got, 1)
	require.Equal(t, "2", got[0].PostID)
}
```

- [ ] **Step 2: 실패 확인** — `go test ./search/ -run TestApplyPostFilters -v` → FAIL.
- [ ] **Step 3: 구현** — `search/filter.go`:

```go
package search

import (
	"strings"
	"time"
)

// applyPostFilters 는 반환 집합에 대해 포함/제외 키워드, 날째, duration, 지표 임계치를 적용한다.
func applyPostFilters(items []VideoItem, f SearchFilters) []VideoItem {
	out := make([]VideoItem, 0, len(items))
	from, to, hasDate := parseDateRange(f.DateFrom, f.DateTo)
	for _, it := range items {
		if len(f.IncludeKeywords) > 0 && !containsAnyFold(it.Title+" "+it.Description, f.IncludeKeywords) {
			continue
		}
		if len(f.ExcludeKeywords) > 0 && containsAnyFold(it.Title+" "+it.Description, f.ExcludeKeywords) {
			continue
		}
		if hasDate {
			if !matchDate(it.PublishedAt, from, to) {
				continue
			}
		}
		if f.DurationMin > 0 && it.Duration < f.DurationMin {
			continue
		}
		if f.DurationMax > 0 && it.Duration > f.DurationMax {
			continue
		}
		if f.MinLikes > 0 && (it.Likes == nil || *it.Likes < f.MinLikes) {
			continue
		}
		if f.MinComments > 0 && (it.Comments == nil || *it.Comments < f.MinComments) {
			continue
		}
		if f.MinFavorites > 0 && (it.Favorites == nil || *it.Favorites < f.MinFavorites) {
			continue
		}
		if f.MinViews > 0 && (it.Views == nil || *it.Views < f.MinViews) {
			continue
		}
		out = append(out, it)
	}
	return out
}

func containsAnyFold(haystack string, needles []string) bool {
	h := strings.ToLower(haystack)
	for _, n := range needles {
		if strings.Contains(h, strings.ToLower(n)) {
			return true
		}
	}
	return false
}

// parseDateRange 는 date-only/date-time 문자열을 RFC3339 time 으로 파싱.
// date-only 면 from 은 그날 00:00:00Z, to 는 그날 23:59:59Z 까지 포함(종일).
func parseDateRange(fromStr, toStr string) (from, to time.Time, ok bool) {
	layouts := []string{time.RFC3339, "2006-01-02"}
	var f, t time.Time
	var ferr, terr error
	if fromStr != "" {
		f, ferr = parseAny(fromStr, layouts, true) // start of day
	}
	if toStr != "" {
		t, terr = parseAny(toStr, layouts, false) // end of day
	}
	if fromStr == "" && toStr == "" {
		return time.Time{}, time.Time{}, false
	}
	if ferr != nil || terr != nil {
		return time.Time{}, time.Time{}, false
	}
	if fromStr == "" {
		f = time.Time{}
	}
	if toStr == "" {
		t = time.Time{}
	}
	return f, t, true
}

// parseAny 는 여러 레이아웃을 시도. date-only 면 endOfDay=true 이면 23:59:59Z, false 면 00:00:00Z.
func parseAny(s string, layouts []string, startOfDay bool) (time.Time, error) {
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			if l == "2006-01-02" {
				if startOfDay {
					return t, nil // 이미 00:00:00
				}
				return t.Add(24*time.Hour - time.Second), nil // 그날 23:59:59
			}
			return t, nil
		}
	}
	return time.Time{}, errInvalidDate
}

var errInvalidDate = &parseErr{"invalid date"}

type parseErr struct{ msg string }

func (e *parseErr) Error() string { return e.msg }

// matchDate 는 PublishedAt(RFC3339) 가 [from, to] 구간에 있는지.
func matchDate(publishedAt string, from, to time.Time) bool {
	if publishedAt == "" {
		return false
	}
	pt, err := time.Parse(time.RFC3339, publishedAt)
	if err != nil {
		return false
	}
	if !from.IsZero() && pt.Before(from) {
		return false
	}
	if !to.IsZero() && pt.After(to) {
		return false
	}
	return true
}
```

- [ ] **Step 4: 통과** — `go test ./search/ -run TestApplyPostFilters -v` → PASS.
- [ ] **Step 5: 커밋**

```bash
gofmt -w search/filter.go search/filter_test.go
git add search/filter.go search/filter_test.go
git commit -m "feat(search): add post filters with date_to end-of-day inclusion"
```

---

## Task 4: rank 머지 (within-platform rank + 라운드로빈, adapter 순서 보존)

**Files:** Create `search/merge.go`, Test `search/merge_test.go`.
**Interfaces:** Consumes `VideoItem`(Task 1), `ptrI`(Task 3 테스트 헬퍼). Produces `rankMerge`. Task 5 가 호출. **정규화 점수(max/epoch)·likes/time/PostID 재정렬 사용 금지 — within-platform rank `1/(r+1)` 규칙만. rankMerge 는 adapter 입력 순서를 그대로 보존(재정렬 금지). cross-platform dedupe 금지. limit/sortBy 인자 없음(per_platform_limit·사후필터는 호출자가 Task 5 에서 이미 적용).**

- [ ] **Step 1: 실패 테스트** — `search/merge_test.go`. `ptrI` 는 Task 3 `filter_test.go` 의 패키지 테스트 헬퍼 재사용(중복 정의 금지).

```go
package search

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// postIDs 는 결과 순서 검증용 헬퍼(merge_test.go 전용).
func postIDs(items []VideoItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.PostID
	}
	return out
}

func TestRankMergePreservesAdapterOrder(t *testing.T) {
	// 재정렬 금지: adapter 입력 순서가 곧 rank(likes 값과 무관).
	// XHS: b(r0,likes50), a(r1,likes100) / Douyin: d(r0,likes5), c(r1,likes10).
	// r0 라운드로빈[xhs,douyin] → b,d / r1 → a,c → [b,d,a,c].
	xhs := []VideoItem{
		{Platform: "xiaohongshu", PostID: "b", Likes: ptrI(50)},
		{Platform: "xiaohongshu", PostID: "a", Likes: ptrI(100)},
	}
	douyin := []VideoItem{
		{Platform: "douyin", PostID: "d", Likes: ptrI(5)},
		{Platform: "douyin", PostID: "c", Likes: ptrI(10)},
	}
	got := rankMerge(map[string][]VideoItem{"xiaohongshu": xhs, "douyin": douyin})
	require.Equal(t, []string{"b", "d", "a", "c"}, postIDs(got))
}

func TestRankMergeAssignsRankScoreOnCopy(t *testing.T) {
	// r(0부터) → RankScore=1/(r+1). 복사본에 설정(원본 슬라이스 미변경).
	xhs := []VideoItem{{Platform: "xiaohongshu", PostID: "a"}, {Platform: "xiaohongshu", PostID: "b"}}
	orig := xhs[0].RankScore
	got := rankMerge(map[string][]VideoItem{"xiaohongshu": xhs})
	require.Equal(t, "a", got[0].PostID)
	require.Equal(t, 1.0, got[0].RankScore)        // r=0 → 1/1
	require.InDelta(t, 0.5, got[1].RankScore, 1e-9) // r=1 → 1/2
	require.Equal(t, orig, xhs[0].RankScore)        // 원본 미변경
}

func TestRankMergeRoundRobinByPlatformOrder(t *testing.T) {
	// 입력 순서 = rank, 동점(동일 r)은 platformOrder 라운드로빈.
	xhs := []VideoItem{{Platform: "xiaohongshu", PostID: "a"}, {Platform: "xiaohongshu", PostID: "b"}}
	douyin := []VideoItem{{Platform: "douyin", PostID: "c"}}
	got := rankMerge(map[string][]VideoItem{"xiaohongshu": xhs, "douyin": douyin})
	require.Equal(t, []string{"a", "c", "b"}, postIDs(got))
}

func TestRankMergeNoCrossPlatformDedupe(t *testing.T) {
	// 같은 PostURL 이라도 플랫폼이 다르면 둘 다 유지(크로스플랫폼 dedupe 금지).
	xhs := []VideoItem{{Platform: "xiaohongshu", PostID: "a", PostURL: "u1"}}
	douyin := []VideoItem{{Platform: "douyin", PostID: "b", PostURL: "u1"}}
	got := rankMerge(map[string][]VideoItem{"xiaohongshu": xhs, "douyin": douyin})
	require.Len(t, got, 2)
}
```

- [ ] **Step 2: 실패 확인** — `go test ./search/ -run TestRankMerge -v` → FAIL(`rankMerge` undefined).

- [ ] **Step 3: 구현** — `search/merge.go`:

```go
package search

import "sort"

// platformOrder 는 rank 머지의 결정적 라운드로빈 순서(동점 타이브레이커).
// sort.Strings 로 platformOrder 자체를 정렬하면 douyin 이 xiaohongshu 보다 먼저 와 기대 순서가 깨지므로 금지.
var platformOrder = []string{"xiaohongshu", "douyin", "tiktok"}

// rankMerge 는 각 플랫폼 집합을 within-platform rank 기반으로 머지한다.
// 규칙(spec §통합 정렬): 각 플랫폼의 adapter 입력 순서를 그대로 보존해 rank r(0부터) 부여 →
// score = 1/(r+1). 같은 r(동점) 은 platformOrder 라운드로빈으로 배치.
// raw 지표(likes/time/PostID) 재정렬 금지 — adapter 순서가 곧 rank.
// 입력 groups 는 호출자(Task 5)가 filter→dedupe→per_platform_limit 한 결과이므로 여기서 재정렬/절단 금지.
// cross-platform dedupe 도 하지 않는다. RankScore 는 복사본에 설정(원본 슬라이스 미변경).
func rankMerge(groups map[string][]VideoItem) []VideoItem {
	order := presentPlatformOrder(groups)
	maxLen := 0
	for _, items := range groups {
		if len(items) > maxLen {
			maxLen = len(items)
		}
	}
	out := make([]VideoItem, 0)
	for r := 0; r < maxLen; r++ {
		for _, p := range order {
			items := groups[p]
			if r < len(items) {
				it := items[r]                     // 값 복사(VideoItem 은 값 타입)
				it.RankScore = 1.0 / float64(r+1)  // r=0 → 1.0, r=1 → 0.5, ...
				out = append(out, it)
			}
		}
	}
	return out
}

// presentPlatformOrder 는 platformOrder 중 존재하는 플랫폼을 그 순서로, 미등록 플랫폼은 이름순 뒤에.
func presentPlatformOrder(groups map[string][]VideoItem) []string {
	present := make(map[string]bool, len(groups))
	for p := range groups {
		present[p] = true
	}
	order := make([]string, 0, len(groups))
	for _, p := range platformOrder {
		if present[p] {
			order = append(order, p)
			delete(present, p)
		}
	}
	extras := make([]string, 0)
	for p := range present {
		extras = append(extras, p)
	}
	sort.Strings(extras) // 미등록 플랫폼만 결정적 정렬(platformOrder 자체 정렬 금지)
	return append(order, extras...)
}
```

- [ ] **Step 4: 통과** — `go test ./search/ -run TestRankMerge -v` → PASS(4 케이스).
- [ ] **Step 5: 커밋**

```bash
gofmt -w search/merge.go search/merge_test.go
git add search/merge.go search/merge_test.go
git commit -m "feat(search): rank merge preserves adapter order, assigns 1/(r+1) on copies"
```

---

## Task 5: AggregatorService + sentinel 에러 + sanitize 메시지

**Files:** Create `search/errors.go`, `search/aggregator.go`, Test `search/aggregator_test.go`.
**Interfaces:** Consumes `VideoAdapter`/`AggregatorRequest`/`SideResult`(Task 1), `applyPostFilters`(Task 3), `rankMerge`/`assignDedupeKey`/`dedupeByKey`(Task 2/4). Produces `AggregatorService`·`NewAggregatorService`·sentinel `ErrUnavailable`/`ErrBadGateway`/`ErrUnreachable`·`SideErrorMessage`. Task 6/7/8/9/10 사용.

> **sanitize 핵심**: 어댑터 에러는 `SideErrorMessage` 로 **고정 안전 메시지**로 변환해 `SideResult.Error`·`SideResult.Available.Reason` 에만 담는다. `err.Error()` 원문(쿼리/쿠키/스택 단편 포함 가능)은 응답에 절대 노출하지 않는다.
> **검색 에러 → Available=false**: 어댑터 `Search` 에러 시 해당 사이드는 `Available.Available=false` + sanitize 메시지. (가용성 probe 성공 여부와 무관하게, 이 응답에서는 해당 플랫폼 결과를 사용할 수 없음.)

- [ ] **Step 1: 실패 테스트** — `search/aggregator_test.go`. `sideOut` 타입은 구현측 패키지 레벨에 **한 번만** 정의(테스트에선 중복 정의 금지).

```go
package search

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeAdapter: 테스트용 VideoAdapter.
type fakeAdapter struct {
	name    string
	avail   Availability
	items   []VideoItem
	err     error
	nextCur string
	hasMore bool
}

func (f *fakeAdapter) Name() string { return f.name }
func (f *fakeAdapter) Available(ctx context.Context) Availability { return f.avail }
func (f *fakeAdapter) Search(ctx context.Context, q SearchQuery) (AdapterSearchPage, error) {
	if f.err != nil {
		return AdapterSearchPage{}, f.err
	}
	return AdapterSearchPage{Items: f.items, NextCursor: f.nextCur, HasMore: f.hasMore}, nil
}

func TestAggregatorSuccessMerges(t *testing.T) {
	svc := NewAggregatorService(map[string]VideoAdapter{
		"xiaohongshu": &fakeAdapter{name: "xiaohongshu", avail: Availability{Available: true}, items: []VideoItem{{Platform: "xiaohongshu", PostID: "a"}}},
		"douyin":      &fakeAdapter{name: "douyin", avail: Availability{Available: true}, items: []VideoItem{{Platform: "douyin", PostID: "b"}}},
	})
	res, err := svc.Search(context.Background(), AggregatorRequest{
		Keyword:   "便携风扇",
		Platforms: []string{"xiaohongshu", "douyin"},
		Sort:      "relevance",
	})
	require.NoError(t, err)
	require.Len(t, res.Items, 2) // rank 머지: a(r0), b(r0) → [a, b]
	require.True(t, res.Sides["xiaohongshu"].Available.Available)
}

func TestAggregatorIsolatesSideFailureSanitized(t *testing.T) {
	// Douyin 은 ErrBadGateway → Available=false + 고정 메시지; XHS 결과는 살아남음.
	svc := NewAggregatorService(map[string]VideoAdapter{
		"xiaohongshu": &fakeAdapter{name: "xiaohongshu", avail: Availability{Available: true}, items: []VideoItem{{Platform: "xiaohongshu", PostID: "a"}}},
		"douyin":      &fakeAdapter{name: "douyin", avail: Availability{Available: true}, err: ErrBadGateway},
	})
	res, _ := svc.Search(context.Background(), AggregatorRequest{Keyword: "k", Platforms: []string{"xiaohongshu", "douyin"}, Sort: "relevance"})
	// 검색 에러 → Available=false + sanitize 메시지.
	require.False(t, res.Sides["douyin"].Available.Available)
	require.Equal(t, "일시적으로 차단 — 잠시 후 재시도", res.Sides["douyin"].Error)
	require.NotContains(t, res.Sides["douyin"].Error, "bad gateway") // 원문 노출 금지
	require.Len(t, res.Items, 1)                                     // XHS 만 결과에 포함
}

func TestAggregatorUnavailableWhenAdapterDown(t *testing.T) {
	// Available() 가 false 면 Search 호출 없이 해당 사이드 미가용.
	svc := NewAggregatorService(map[string]VideoAdapter{
		"tiktok": &fakeAdapter{name: "tiktok", avail: Availability{Available: false, Reason: "ms_token 갱신 필요"}},
	})
	res, _ := svc.Search(context.Background(), AggregatorRequest{Keyword: "k", Platforms: []string{"tiktok"}, Sort: "relevance"})
	require.False(t, res.Sides["tiktok"].Available.Available)
	require.Contains(t, res.Sides["tiktok"].Available.Reason, "ms_token")
}

func TestAggregatorIntraPlatformDedupe(t *testing.T) {
	// 같은 플랫폼 내 동일 post_url 은 첫 것만(플랫폼 내부 dedupe).
	svc := NewAggregatorService(map[string]VideoAdapter{
		"douyin": &fakeAdapter{name: "douyin", avail: Availability{Available: true}, items: []VideoItem{
			{Platform: "douyin", PostID: "a", PostURL: "u1"},
			{Platform: "douyin", PostID: "b", PostURL: "u1"},
		}},
	})
	res, _ := svc.Search(context.Background(), AggregatorRequest{Keyword: "k", Platforms: []string{"douyin"}, Sort: "relevance"})
	require.Len(t, res.Items, 1)
	require.Equal(t, "a", res.Items[0].PostID)
}

func TestAggregatorFiltersBeforeDedupe(t *testing.T) {
	// 첫 중복이 필터 탈락, 둘째가 통과: dedupe→filter 이면 잘못 0개가 되므로 순서를 고정한다.
	svc := NewAggregatorService(map[string]VideoAdapter{
		"douyin": &fakeAdapter{name: "douyin", avail: Availability{Available: true}, items: []VideoItem{
			{Platform: "douyin", PostID: "a", PostURL: "u1", Likes: int64Ptr(1)},
			{Platform: "douyin", PostID: "b", PostURL: "u1", Likes: int64Ptr(100)},
		}},
	})
	res, _ := svc.Search(context.Background(), AggregatorRequest{
		Keyword: "k", Platforms: []string{"douyin"}, Sort: "relevance",
		Filters: SearchFilters{MinLikes: 50},
	})
	require.Equal(t, []string{"b"}, postIDs(res.Items))
	require.Equal(t, []string{"b"}, postIDs(res.Sides["douyin"].Items))
}

func TestAggregatorAppliesPerPlatformLimit(t *testing.T) {
	// per-platform limit 은 aggregator 에서 filter→dedupe 이후에 절단(방어적 이중 보장).
	// adapter 입력 순서(=rank)를 보존해 앞쪽 N 개만 유지.
	svc := NewAggregatorService(map[string]VideoAdapter{
		"douyin": &fakeAdapter{name: "douyin", avail: Availability{Available: true}, items: []VideoItem{
			{Platform: "douyin", PostID: "a"},
			{Platform: "douyin", PostID: "b"},
			{Platform: "douyin", PostID: "c"},
		}},
	})
	res, _ := svc.Search(context.Background(), AggregatorRequest{
		Keyword:   "k",
		Platforms: []string{"douyin"},
		Sort:      "relevance",
		Filters:   SearchFilters{PerPlatformLimit: 2},
	})
	require.Len(t, res.Items, 2)
	require.Equal(t, []string{"a", "b"}, postIDs(res.Items)) // 앞쪽 2개 보존(adapter 순서=rank)
	require.Len(t, res.Sides["douyin"].Items, 2)              // Sides.Items 도 동일 절단 결과
}

func TestSideErrorMessageMapping(t *testing.T) {
	// ErrUnavailable 은 플랫폼별 고정 메시지(spec Error Handling 표와 일치) — test/impl 하나로 통일.
	require.Equal(t, "샤오홍슈: 설정에서 QR 로그인 필요", SideErrorMessage("xiaohongshu", ErrUnavailable))
	require.Equal(t, "Douyin: 쿠키/서명 미설정", SideErrorMessage("douyin", ErrUnavailable))
	require.Equal(t, "TikTok: ms_token 갱신 필요(tiktok.com 쿠키)", SideErrorMessage("tiktok", ErrUnavailable))
	require.Equal(t, "플랫폼 준비 중", SideErrorMessage("unknown", ErrUnavailable))
	// 공통 sentinel.
	require.Equal(t, "일시적으로 차단 — 잠시 후 재시도", SideErrorMessage("douyin", ErrBadGateway))
	require.Equal(t, "서비스에 연결할 수 없습니다", SideErrorMessage("douyin", ErrUnreachable))
	// 타임아웃/취소.
	require.Equal(t, "응답 시간 초과", SideErrorMessage("douyin", context.DeadlineExceeded))
	require.Equal(t, "요청이 취소되었습니다", SideErrorMessage("douyin", context.Canceled))
	// 알 수 없는 에러 → 원문 노출 금지, 고정 안전 문구.
	msg := SideErrorMessage("douyin", errors.New("random raw with cookie=secret"))
	require.Equal(t, "검색 중 오류가 발생했습니다", msg)
	require.NotContains(t, msg, "secret")
}
```

- [ ] **Step 2: 실패 확인** — `go test ./search/ -run 'TestAggregator|TestSideError' -v` → FAIL(심볼 미정의).

- [ ] **Step 3: 구현** — `search/errors.go`:

```go
package search

import (
	"context"
	"errors"
)

// Sentinel 에러: 사이드카/어댑터 실패 유형. 원문 err.Error() 대신 이 들로 분기해 고정 메시지 매핑.
var (
	ErrUnavailable = errors.New("platform unavailable")   // 쿠키/ms_token 미설정·만료(503)
	ErrBadGateway  = errors.New("platform search failed") // 검색/서명/antibot 실패(502)
	ErrUnreachable = errors.New("platform unreachable")   // 연결 실패/네트워크
)

// SideErrorMessage 는 err 를 플랫폼별 고정 안전 한국어 메시지로 변환.
// 원문(쿠키/쿼리/스택)은 절대 포함하지 않는다. spec Error Handling 표와 일치.
func SideErrorMessage(name string, err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "응답 시간 초과"
	case errors.Is(err, context.Canceled):
		return "요청이 취소되었습니다"
	case errors.Is(err, ErrUnavailable):
		switch name {
		case "xiaohongshu":
			return "샤오홍슈: 설정에서 QR 로그인 필요"
		case "douyin":
			return "Douyin: 쿠키/서명 미설정"
		case "tiktok":
			return "TikTok: ms_token 갱신 필요(tiktok.com 쿠키)"
		}
		return "플랫폼 준비 중"
	case errors.Is(err, ErrBadGateway):
		return "일시적으로 차단 — 잠시 후 재시도"
	case errors.Is(err, ErrUnreachable):
		return "서비스에 연결할 수 없습니다"
	default:
		return "검색 중 오류가 발생했습니다"
	}
}
```

- `search/aggregator.go`(주의: `sideOut` 은 패키지 레벨에 **한 번만** 정의):

```go
package search

import (
	"context"
	"sync"
	"time"
)

// perAdapterTimeout: 어댑터별 최대 대기. 한 사이드 지연이 전체를 막지 않도록.
var perAdapterTimeout = map[string]time.Duration{
	"xiaohongshu": 60 * time.Second,
	"douyin":      45 * time.Second,
	"tiktok":      45 * time.Second,
}

const aggregatorOverallTimeout = 75 * time.Second

// sideOut 은 단일 플랫폼 fan-out 결과. 패키지 레벨에 한 번만 정의(중복 정의 금지).
type sideOut struct {
	name string
	res  SideResult
}

// AggregatorService 는 VideoAdapter 들을 fan-out 해 머지한다.
type AggregatorService struct {
	adapters map[string]VideoAdapter
}

func NewAggregatorService(adapters map[string]VideoAdapter) *AggregatorService {
	return &AggregatorService{adapters: adapters}
}

// Search 는 요청된 플랫폼에 대해 병렬 검색 후 머지/필터/정렬한다.
// 한 사이드 실패/미가용은 SideResult 로 흡수되고 전체는 실패하지 않는다(err 는 항상 nil).
func (s *AggregatorService) Search(ctx context.Context, req AggregatorRequest) (*AggregatedResult, error) {
	ctx, cancel := context.WithTimeout(ctx, aggregatorOverallTimeout)
	defer cancel()

	platforms := req.Platforms
	if len(platforms) == 0 {
		for n := range s.adapters {
			platforms = append(platforms, n)
		}
	}

	outs := make([]sideOut, len(platforms))
	var wg sync.WaitGroup
	for i, name := range platforms {
		wg.Add(1)
		go func(idx int, pname string) {
			defer wg.Done()
			outs[idx] = s.runSide(ctx, pname, req)
		}(i, name)
	}
	wg.Wait()

	result := &AggregatedResult{
		KeywordUsed: req.Keyword,
		Sort:        req.Sort,
		Sides:       map[string]SideResult{},
	}
	groups := map[string][]VideoItem{}
	for _, o := range outs {
		// per-side 파이프라인(spec 순서): filter → dedupe → per_platform_limit.
		// Sides[name].Items 와 groups[name] 은 동일(필터+절단된) 결과를 공유한다.
		// 크로스플랫폼 dedupe 금지 — 플랫폼 내부 dedupe 만.
		if o.res.Available.Available && len(o.res.Items) > 0 {
			items := applyPostFilters(o.res.Items, req.Filters)
			items = dedupeByKey(assignDedupeKey(items, req.Keyword))
			items = truncatePerPlatform(items, req.Filters.PerPlatformLimit)
			o.res.Items = items
			groups[o.name] = items
		}
		result.Sides[o.name] = o.res
	}

	// rank 머지는 마지막: within-platform adapter 순서 보존, 재정렬/merged-limit 없음.
	result.Items = rankMerge(groups)
	return result, nil
}

// truncatePerPlatform 은 per-platform 최대 개수로 절단(0 이하 = 제한 없음).
// 어댑터가 Limit 힌트를 받더라도 aggregator 가 권위 있는 상한으로 한 번 더 절단(방어적 이중 보장).
// adapter 입력 순서(=rank)를 보존해 앞쪽을 유지한다.
func truncatePerPlatform(items []VideoItem, limit int) []VideoItem {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

// runSide 는 단일 어댑터를 가용성 확인 후 검색(타임아웃 격리).
// 검색 에러 시 Available=false + sanitize 메시지.
func (s *AggregatorService) runSide(ctx context.Context, name string, req AggregatorRequest) sideOut {
	ad, ok := s.adapters[name]
	if !ok {
		return sideOut{name: name, res: SideResult{Available: Availability{Available: false, Reason: "지원하지 않는 플랫폼"}}}
	}
	timeout := perAdapterTimeout[name]
	if timeout == 0 {
		timeout = 45 * time.Second
	}
	sctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	avail := ad.Available(sctx)
	if !avail.Available {
		return sideOut{name: name, res: SideResult{Available: avail}}
	}

	page, err := ad.Search(sctx, SearchQuery{
		Keyword:    req.Keyword,
		Sort:       req.Sort,
		Filters:    req.Filters,
		Limit:      req.Filters.PerPlatformLimit,
		PageCursor: req.PageCursors[name],
	})
	if err != nil {
		msg := SideErrorMessage(name, err)
		return sideOut{name: name, res: SideResult{
			Available: Availability{Available: false, Reason: msg},
			Error:     msg,
		}}
	}
	return sideOut{name: name, res: SideResult{
		Items:      page.Items,
		Available:  Availability{Available: true},
		NextCursor: page.NextCursor,
		HasMore:    page.HasMore,
	}}
}
```

- [ ] **Step 4: 통과** — `go test ./search/ -run 'TestAggregator|TestSideError' -v` → PASS(5 케이스).
- [ ] **Step 5: 커밋**

```bash
gofmt -w search/errors.go search/aggregator.go search/aggregator_test.go
git add search/errors.go search/aggregator.go search/aggregator_test.go
git commit -m "feat(search): add AggregatorService with single sideOut, error→unavailable, sanitized messages"
```


---

## Task 6: SidecarClient (body platform + `{success,data}` 래핑 decode, /healthz TTL)

**Files:** Create `sidecar_client.go`, Test `sidecar_client_test.go`.
**Interfaces:** Consumes `search.ErrUnavailable/ErrBadGateway/ErrUnreachable`·`search.SearchFilters`(Task 1/5). Produces `SidecarClient`(`Search`/`Healthz`), `SidecarSearchRequest`, `sidecarSearchData`, `sidecarVideo`, `sidecarHealth`. Task 7/8/10 사용.

> **핵심(spec 계약 정확 일치)**: (1) `POST /search` body 에 **`platform`** 필드 포함(쿼리 아님). (2) 응답은 **`{success:true,data:{videos,next_cursor,has_more}}`** 래핑 — `data` 안쪽만 decode. (3) `GET /healthz` → `{status:"ok",douyin,tiktok,llm}`(TTL 캐시, 매 검색마다 HTTP 중복 방지). (4) HTTP 503→`ErrUnavailable`, 502/500/4xx→`ErrBadGateway`, 네트워크→`ErrUnreachable`(ctx deadline/canceled 는 원문 그대로 상위로). 원문 body·비밀 평문 미노출.

- [ ] **Step 1: 실패 테스트** — `sidecar_client_test.go`. 응답은 항상 `{success,data:{...}}` 래핑으로 작성.

```go
package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/search"
)

func TestSidecarSearchMapsStatusToSentinels(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{503, search.ErrUnavailable},
		{502, search.ErrBadGateway},
		{500, search.ErrBadGateway},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" {
				w.WriteHeader(200)
				return
			}
			w.WriteHeader(c.status)
		}))
		defer srv.Close()
		cli := NewSidecarClient(srv.URL, 0)
		_, err := cli.Search(context.Background(), SidecarSearchRequest{Platform: "douyin", Q: "x"})
		require.ErrorIs(t, err, c.want, "status %d", c.status)
	}
}

func TestSidecarSearchParsesWrappedVideosAndBodyPlatform(t *testing.T) {
	// platform 은 body 에 전달되어야 함(쿼리 아님). 응답은 {success,data:{...}} 래핑.
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		require.Empty(t, r.URL.Query().Get("platform"), "platform 은 쿼리가 아닌 body")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"videos":[{"platform":"douyin","post_id":"a"}],"next_cursor":"eyIi","has_more":true}}`))
	}))
	defer srv.Close()
	cli := NewSidecarClient(srv.URL, 0)
	resp, err := cli.Search(context.Background(), SidecarSearchRequest{Platform: "douyin", Q: "x"})
	require.NoError(t, err)
	require.Len(t, resp.Videos, 1)
	require.True(t, resp.HasMore)
	require.Equal(t, "eyIi", resp.NextCursor)
	require.Equal(t, "douyin", gotBody["platform"], "platform 이 body 에 있어야 함")
}

func TestSidecarSearchNetworkErrorUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()
	cli := NewSidecarClient(srv.URL, 0)
	_, err := cli.Search(context.Background(), SidecarSearchRequest{Platform: "douyin", Q: "x"})
	require.ErrorIs(t, err, search.ErrUnreachable)
}

func TestSidecarHealthzCached(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","douyin":true,"tiktok":false,"llm":false}`))
	}))
	defer srv.Close()
	cli := NewSidecarClient(srv.URL, 5*time.Second)
	for i := 0; i < 3; i++ {
		h, err := cli.Healthz(context.Background())
		require.NoError(t, err)
		require.True(t, h.Douyin)
	}
	require.Equal(t, 1, calls, "healthz 는 TTL 내 1회만 호출")
}
```

- [ ] **Step 2: 실패 확인** — `go test ./ -run TestSidecar -v` → FAIL(심볼 미정의).

- [ ] **Step 3: 구현** — `sidecar_client.go`:

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/xpzouying/xiaohongshu-mcp/search"
)

// SidecarSearchRequest: 사이드카 POST /search body(spec 계약). platform 은 body 에 포함.
type SidecarSearchRequest struct {
	Platform string             `json:"platform"` // douyin|tiktok
	Q        string             `json:"q"`
	Count    int                `json:"count"`
	Sort     string             `json:"sort"`
	Cursor   string             `json:"cursor"`
	Filters  search.SearchFilters `json:"filters"`
}

// sidecarSearchEnvelope: {success,data:{...}} 래핑 응답.
type sidecarSearchEnvelope struct {
	Success bool              `json:"success"`
	Data    sidecarSearchData `json:"data"`
}

// sidecarSearchData: 래핑 안쪽 데이터.
type sidecarSearchData struct {
	Videos     []sidecarVideo `json:"videos"`
	NextCursor string         `json:"next_cursor"`
	HasMore    bool           `json:"has_more"`
}

// sidecarVideo: 사이드카가 통일 매핑한 VideoItem JSON. search.VideoItem 과 동일 스키마.
type sidecarVideo struct {
	Platform     string `json:"platform"`
	PostID       string `json:"post_id"`
	PostURL      string `json:"post_url"`
	VideoURL     string `json:"video_url,omitempty"`
	ThumbnailURL string `json:"thumbnail_url"`
	Title        string `json:"title"`
	Description  string `json:"description,omitempty"`
	Author       string `json:"author"`
	PublishedAt  string `json:"published_at,omitempty"`
	Duration     int    `json:"duration,omitempty"`
	Likes        *int64 `json:"likes"`
	Comments     *int64 `json:"comments"`
	Favorites    *int64 `json:"favorites"`
	Views        *int64 `json:"views"`
	Shares       *int64 `json:"shares"`
	NeedsDetail  bool   `json:"needs_detail"`
}

// sidecarHealth: /healthz 응답. 비밀(cookie/ms_token) 평문 없이 bool 만.
type sidecarHealth struct {
	Status string `json:"status"`
	Douyin bool   `json:"douyin"`
	Tiktok bool   `json:"tiktok"`
	Llm    bool   `json:"llm"`
}

// SidecarClient: Python 사이드카 HTTP 클라이언트.
type SidecarClient struct {
	baseURL string
	http    *http.Client

	mu      sync.Mutex
	hzCache sidecarHealth
	hzAt    time.Time
	hzTTL   time.Duration
}

// NewSidecarClient: baseURL 과 healthz TTL(0=기본 10s) 로 생성.
func NewSidecarClient(baseURL string, healthzTTL time.Duration) *SidecarClient {
	if healthzTTL == 0 {
		healthzTTL = 10 * time.Second
	}
	return &SidecarClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 25 * time.Second},
		hzTTL:   healthzTTL,
	}
}

// Search: 사이드카 POST /search 호출. platform 은 body. 상태코드/네트워크 에러를 search sentinel 로 매핑.
// 응답 {success,data:{videos,next_cursor,has_more}} 의 data 를 반환.
func (c *SidecarClient) Search(ctx context.Context, req SidecarSearchRequest) (sidecarSearchData, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return sidecarSearchData{}, fmt.Errorf("%w: %v", search.ErrBadGateway, err)
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/search", bytes.NewReader(body))
	if err != nil {
		return sidecarSearchData{}, search.ErrUnreachable
	}
	hr.Header.Set("Content-Type", "application/json")
	// platform 은 body 에 포함 — 쿼리 파라미터 사용 금지(spec 계약).

	resp, err := c.http.Do(hr)
	if err != nil {
		// ctx deadline/canceled 는 원문 그대로 상위로(aggregator 가 SideErrorMessage 처리).
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return sidecarSearchData{}, err
		}
		return sidecarSearchData{}, search.ErrUnreachable
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusServiceUnavailable:
		return sidecarSearchData{}, search.ErrUnavailable
	case resp.StatusCode >= 400:
		return sidecarSearchData{}, search.ErrBadGateway
	}

	var env sidecarSearchEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return sidecarSearchData{}, search.ErrBadGateway
	}
	if !env.Success {
		return sidecarSearchData{}, search.ErrBadGateway
	}
	return env.Data, nil
}

// Healthz: 사이드카 /healthz 조회(TTL 캐시). 가용성 bool 만.
func (c *SidecarClient) Healthz(ctx context.Context) (sidecarHealth, error) {
	c.mu.Lock()
	if !c.hzAt.IsZero() && time.Since(c.hzAt) < c.hzTTL {
		hz := c.hzCache
		c.mu.Unlock()
		return hz, nil
	}
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return sidecarHealth{}, search.ErrUnreachable
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return sidecarHealth{}, search.ErrUnreachable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return sidecarHealth{}, search.ErrUnreachable
	}
	var hz sidecarHealth
	if err := json.NewDecoder(resp.Body).Decode(&hz); err != nil {
		return sidecarHealth{}, search.ErrBadGateway
	}

	c.mu.Lock()
	c.hzCache = hz
	c.hzAt = time.Now()
	c.mu.Unlock()
	return hz, nil
}
```

> module path(go.mod): `github.com/xpzouying/xiaohongshu-mcp`. 검색 패키지 import: `github.com/xpzouying/xiaohongshu-mcp/search`.

- [ ] **Step 4: 통과** — `go test ./ -run TestSidecar -v` → PASS(4 케이스).
- [ ] **Step 5: 커밋**

```bash
gofmt -w sidecar_client.go sidecar_client_test.go
git add sidecar_client.go sidecar_client_test.go
git commit -m "feat(api): add SidecarClient with body platform, success/data envelope decode, healthz TTL"
```



---

## Task 7: Douyin 어댑터 (opaque cursor + /healthz 가용성)

**Files:** Create `adapter_douyin.go`(공유 helper 포함), Test `adapter_douyin_test.go`.
**Interfaces:** Consumes `SidecarClient`/`SidecarSearchRequest`/`sidecarSearchData`/`sidecarHealth`/`sidecarVideo`(Task 6), `search.VideoAdapter`/`search.SearchQuery`(Task 1). Produces `DouyinAdapter` + 공유 `sidecarSearcher` 인터페이스/`buildSidecarRequest`/`toVideoItem`. Task 8/10 사용.

> **핵심**: `Available()` 가 `os.Getenv` 가 아닌 `cli.Healthz(ctx).Douyin` 을 읽는다(비밀은 Python 에만). **Douyin 은 opaque cursor 페이지네이션 지원**(사이드카 `next_cursor`/`has_more` 그대로 매핑). platform 은 body(`SidecarSearchRequest.Platform`).

- [ ] **Step 1: 실패 테스트** — fake `sidecarSearcher` 로 가용성 게이팅 + 매핑 + body platform 검증.

```go
package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/search"
)

// fakeSidecar: 어댑터 테스트용 sidecarSearcher(TikTok 어댑터 테스트가 공유).
type fakeSidecar struct {
	hz     sidecarHealth
	hzErr  error
	resp   sidecarSearchData
	sErr   error
	gotReq SidecarSearchRequest
}

func (f *fakeSidecar) Healthz(ctx context.Context) (sidecarHealth, error) { return f.hz, f.hzErr }
func (f *fakeSidecar) Search(ctx context.Context, req SidecarSearchRequest) (sidecarSearchData, error) {
	f.gotReq = req
	return f.resp, f.sErr
}

func TestDouyinAvailableFromHealthz(t *testing.T) {
	a := NewDouyinAdapter(&fakeSidecar{hz: sidecarHealth{Douyin: true}})
	require.True(t, a.Available(context.Background()).Available)

	a2 := NewDouyinAdapter(&fakeSidecar{hz: sidecarHealth{Douyin: false}})
	av := a2.Available(context.Background())
	require.False(t, av.Available)
	require.Contains(t, av.Reason, "쿠키/서명")
}

func TestDouyinSearchMapsVideosWithCursor(t *testing.T) {
	likes := int64(1200)
	fs := &fakeSidecar{
		hz:   sidecarHealth{Douyin: true},
		resp: sidecarSearchData{Videos: []sidecarVideo{{Platform: "douyin", PostID: "a", Title: "t", Likes: &likes}}, NextCursor: "cur1", HasMore: true},
	}
	a := NewDouyinAdapter(fs)
	page, err := a.Search(context.Background(), search.SearchQuery{Keyword: "k", Sort: "popularity"})
	require.NoError(t, err)
	// platform 이 body 에 전달(쿼리 아님).
	require.Equal(t, "douyin", fs.gotReq.Platform)
	require.Equal(t, "k", fs.gotReq.Q)
	require.Len(t, page.Items, 1)
	require.Equal(t, int64(1200), *page.Items[0].Likes)
	require.Equal(t, "cur1", page.NextCursor) // Douyin opaque cursor 매핑
	require.True(t, page.HasMore)
	require.False(t, page.Items[0].NeedsDetail) // Douyin 은 video_url 바로 제공
}

func TestDouyinSearchPropagatesSentinel(t *testing.T) {
	a := NewDouyinAdapter(&fakeSidecar{hz: sidecarHealth{Douyin: true}, sErr: search.ErrBadGateway})
	_, err := a.Search(context.Background(), search.SearchQuery{Keyword: "k"})
	require.ErrorIs(t, err, search.ErrBadGateway)
}
```

- [ ] **Step 2: 실패 확인** — `go test ./ -run TestDouyin -v` → FAIL.

- [ ] **Step 3: 구현** — `adapter_douyin.go`:

```go
package main

import (
	"context"

	"github.com/xpzouying/xiaohongshu-mcp/search"
)

// sidecarSearcher: 어댑터가 의존하는 사이드카 인터페이스(SidecarClient 구현, 테스트는 fake).
type sidecarSearcher interface {
	Healthz(ctx context.Context) (sidecarHealth, error)
	Search(ctx context.Context, req SidecarSearchRequest) (sidecarSearchData, error)
}

// buildSidecarRequest: SearchQuery → 사이드카 /search body(Douyin/TikTok 공용).
// platform 은 body 필드로 지정(쿼리 아님). count 기본 15.
func buildSidecarRequest(platform string, q search.SearchQuery) SidecarSearchRequest {
	count := q.Limit
	if count <= 0 {
		count = 15
	}
	return SidecarSearchRequest{
		Platform: platform,
		Q:        q.Keyword,
		Sort:     q.Sort,
		Cursor:   q.PageCursor,
		Count:    count,
		Filters:  q.Filters,
	}
}

// toVideoItem: 사이드카 sidecarVideo → search.VideoItem(스키마 동일).
func toVideoItem(v sidecarVideo) search.VideoItem {
	return search.VideoItem{
		Platform:     v.Platform,
		PostID:       v.PostID,
		PostURL:      v.PostURL,
		VideoURL:     v.VideoURL,
		ThumbnailURL: v.ThumbnailURL,
		Title:        v.Title,
		Description:  v.Description,
		Author:       v.Author,
		PublishedAt:  v.PublishedAt,
		Duration:     v.Duration,
		Likes:        v.Likes,
		Comments:     v.Comments,
		Favorites:    v.Favorites,
		Views:        v.Views,
		Shares:       v.Shares,
		NeedsDetail:  v.NeedsDetail,
	}
}

// DouyinAdapter: Douyin 영상 검색 어댑터(사이드카 래핑, opaque cursor).
type DouyinAdapter struct {
	cli sidecarSearcher
}

func NewDouyinAdapter(cli sidecarSearcher) *DouyinAdapter { return &DouyinAdapter{cli: cli} }

func (a *DouyinAdapter) Name() string { return "douyin" }

// Available: 사이드카 /healthz 의 douyin bool. 비밀 평문 없음.
func (a *DouyinAdapter) Available(ctx context.Context) search.Availability {
	hz, err := a.cli.Healthz(ctx)
	if err != nil {
		return search.Availability{Available: false, Reason: "사이드카 연결 불가"}
	}
	if !hz.Douyin {
		return search.Availability{Available: false, Reason: "Douyin: 쿠키/서명 미설정"}
	}
	return search.Availability{Available: true}
}

func (a *DouyinAdapter) Search(ctx context.Context, q search.SearchQuery) (search.AdapterSearchPage, error) {
	resp, err := a.cli.Search(ctx, buildSidecarRequest("douyin", q))
	if err != nil {
		return search.AdapterSearchPage{}, err
	}
	items := make([]search.VideoItem, 0, len(resp.Videos))
	for _, v := range resp.Videos {
		items = append(items, toVideoItem(v))
	}
	// Douyin opaque cursor: 사이드카 next_cursor/has_more 를 그대로 매핑.
	return search.AdapterSearchPage{Items: items, NextCursor: resp.NextCursor, HasMore: resp.HasMore}, nil
}
```

- [ ] **Step 4: 통과** — `go test ./ -run TestDouyin -v` → PASS(3 케이스).
- [ ] **Step 5: 커밋**

```bash
gofmt -w adapter_douyin.go adapter_douyin_test.go
git add adapter_douyin.go adapter_douyin_test.go
git commit -m "feat(api): add Douyin adapter with healthz availability and opaque cursor"
```

---

## Task 8: TikTok 어댑터 (M1 단일 페이지 + /healthz 가용성)

**Files:** Create `adapter_tiktok.go`, Test `adapter_tiktok_test.go`.
**Interfaces:** Consumes `sidecarSearcher`/`buildSidecarRequest`/`toVideoItem`(Task 7), `search.VideoAdapter`. Produces `TikTokAdapter`. Task 10 사용.

> **M1 한계(정직한 표시)**: TikTok-Api 7.3.3 `search.search_type` 의 async generator 가 cursor/has_more 를 안정적으로 노출하지 않아 **M1 은 단일 페이지**(`HasMore=false`, `NextCursor=""`). Douyin 만 opaque cursor(M1). ms_token 누락/만료 시 사이드카 503→`ErrUnavailable`. PostURL/VideoURL 은 사이드카가 채워 반환(미획득 시 빈 값 → 프론트가 원본 보기 fallback).

- [ ] **Step 1: 실패 테스트**

```go
package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/search"
)

func TestTikTokAvailableFromHealthz(t *testing.T) {
	require.True(t, NewTikTokAdapter(&fakeSidecar{hz: sidecarHealth{Tiktok: true}}).Available(context.Background()).Available)
	av := NewTikTokAdapter(&fakeSidecar{hz: sidecarHealth{Tiktok: false}}).Available(context.Background())
	require.False(t, av.Available)
	require.Contains(t, av.Reason, "ms_token")
}

func TestTikTokSearchMapsVideosSinglePage(t *testing.T) {
	views := int64(5000)
	fs := &fakeSidecar{
		hz:   sidecarHealth{Tiktok: true},
		// 사이드카는 TikTok 에 대해 단일 페이지(has_more=false, next_cursor="") 반환.
		resp: sidecarSearchData{Videos: []sidecarVideo{{Platform: "tiktok", PostID: "t1", Views: &views, PostURL: "https://www.tiktok.com/@u/video/t1"}}, NextCursor: "", HasMore: false},
	}
	a := NewTikTokAdapter(fs)
	page, err := a.Search(context.Background(), search.SearchQuery{Keyword: "k"})
	require.NoError(t, err)
	require.Equal(t, "tiktok", fs.gotReq.Platform)
	require.Len(t, page.Items, 1)
	require.False(t, page.HasMore)  // M1 단일 페이지
	require.Empty(t, page.NextCursor)
	require.NotEmpty(t, page.Items[0].PostURL) // 사이드카가 PostURL 채움(원본 보기 fallback)
}

func TestTikTokSearchMsTokenUnavailable(t *testing.T) {
	a := NewTikTokAdapter(&fakeSidecar{hz: sidecarHealth{Tiktok: true}, sErr: search.ErrUnavailable})
	_, err := a.Search(context.Background(), search.SearchQuery{Keyword: "k"})
	require.ErrorIs(t, err, search.ErrUnavailable)
}
```

- [ ] **Step 2: 실패 확인** — `go test ./ -run TestTikTok -v` → FAIL.

- [ ] **Step 3: 구현** — `adapter_tiktok.go`:

```go
package main

import (
	"context"

	"github.com/xpzouying/xiaohongshu-mcp/search"
)

// TikTokAdapter: TikTok 영상 검색 어댑터(사이드카 래핑, M1 단일 페이지).
type TikTokAdapter struct {
	cli sidecarSearcher
}

func NewTikTokAdapter(cli sidecarSearcher) *TikTokAdapter { return &TikTokAdapter{cli: cli} }

func (a *TikTokAdapter) Name() string { return "tiktok" }

// Available: 사이드카 /healthz 의 tiktok bool(TT_MSTOKEN 설정 여부).
func (a *TikTokAdapter) Available(ctx context.Context) search.Availability {
	hz, err := a.cli.Healthz(ctx)
	if err != nil {
		return search.Availability{Available: false, Reason: "사이드카 연결 불가"}
	}
	if !hz.Tiktok {
		return search.Availability{Available: false, Reason: "TikTok: ms_token 갱신 필요"}
	}
	return search.Availability{Available: true}
}

func (a *TikTokAdapter) Search(ctx context.Context, q search.SearchQuery) (search.AdapterSearchPage, error) {
	resp, err := a.cli.Search(ctx, buildSidecarRequest("tiktok", q))
	if err != nil {
		return search.AdapterSearchPage{}, err
	}
	items := make([]search.VideoItem, 0, len(resp.Videos))
	for _, v := range resp.Videos {
		items = append(items, toVideoItem(v))
	}
	// M1: TikTok 단일 페이지 — 사이드카가 has_more=false/next_cursor="" 반환(강제 아님, 계약).
	return search.AdapterSearchPage{Items: items, NextCursor: resp.NextCursor, HasMore: resp.HasMore}, nil
}
```

- [ ] **Step 4: 통과** — `go test ./ -run TestTikTok -v` → PASS(3 케이스).
- [ ] **Step 5: 커밋**

```bash
gofmt -w adapter_tiktok.go adapter_tiktok_test.go
git add adapter_tiktok.go adapter_tiktok_test.go
git commit -m "feat(api): add TikTok adapter with healthz availability (M1 single-page)"
```



---

## Task 9: XhsAdapter (PostURL + Duration + 실제 로그인 readiness)

**Files:** Create `adapter_xhs.go`, Test `adapter_xhs_test.go`.
**Interfaces:** Consumes `XiaohongshuService.SearchFeeds(...)`와 기존 `XiaohongshuService.CheckLoginStatus(ctx) (*LoginStatusResponse,error)`, `xiaohongshu.Feed`/`NoteCard`/`Video`(`*Video`, nil 가능)/`VideoCapability.Duration`, `search.VideoAdapter`. Produces `XhsAdapter`, `XhsService` 인터페이스, `xhsFeedToVideoItem`, `xhsPostURL`, `parseCount`/`parseChineseCount`, `xhsPublishBucket`. Task 10 사용.

> **수정 사항(이슈 3 + 감사)**:
> 1. **PostURL 추가**: `xhsPostURL(feed.ID, feed.XsecToken)` → `https://www.xiaohongshu.com/explore/{id}?xsec_token=...&xsec_source=pc_feed`(원본 보기 fallback).
> 2. **Duration 추가**: `nc.Video` 가 `*Video`(nil 가능) → nil guard 후 `nc.Video.Capa.Duration`(초 단위).
> 3. **실제 readiness**: `Available()` 은 파일 존재를 추정하지 않고 기존 `CheckLoginStatus` 결과만 사용한다. QR 로그인 후 `IsLoggedIn=true`이면 즉시 검색 가능하고, 미로그인·probe 오류는 이유를 숨긴 안전 메시지와 함께 `available:false`. **cookies 경로 중복 구현 금지** — adapter 가 직접 cookies 를 읽지 않는다.

- [ ] **Step 1: 실패 테스트**

```go
package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/search"
	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

// fakeXhsSearcher: 실제 SearchFeeds 시그니처 (*FeedsListResponse, error) 흉내.
type fakeXhsSearcher struct {
	resp *FeedsListResponse
	err  error
	last []xiaohongshu.FilterOption
	loggedIn bool
	loginErr error
}

func (f *fakeXhsSearcher) SearchFeeds(ctx context.Context, keyword string, filters ...xiaohongshu.FilterOption) (*FeedsListResponse, error) {
	f.last = filters
	return f.resp, f.err
}

func (f *fakeXhsSearcher) CheckLoginStatus(ctx context.Context) (*LoginStatusResponse, error) {
	return &LoginStatusResponse{IsLoggedIn: f.loggedIn}, f.loginErr
}

func TestXhsSearchMapsFeedToVideoItem(t *testing.T) {
	resp := &FeedsListResponse{Feeds: []xiaohongshu.Feed{{
		ID: "abc", XsecToken: "tok",
		NoteCard: xiaohongshu.NoteCard{
			Type:         "video",
			DisplayTitle: "便携风扇",
			User:         xiaohongshu.User{Nickname: "테스터"},
			InteractInfo: xiaohongshu.InteractInfo{LikedCount: "1.2万", CommentCount: "30"},
			Cover:        xiaohongshu.Cover{URLDefault: "https://x/c.jpg"},
			Video:        &xiaohongshu.Video{Capa: xiaohongshu.VideoCapability{Duration: 45}},
		},
	}}}
	a := NewXhsAdapter(&fakeXhsSearcher{resp: resp})
	page, err := a.Search(context.Background(), search.SearchQuery{Keyword: "便携风扇", Sort: "relevance"})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	it := page.Items[0]
	require.Equal(t, "xiaohongshu", it.Platform)
	require.Equal(t, "abc", it.PostID)
	require.Equal(t, "https://x/c.jpg", it.ThumbnailURL)
	require.Equal(t, int64(12000), *it.Likes) // "1.2万" → 12000
	require.Equal(t, int64(30), *it.Comments)
	require.Nil(t, it.Views)                                  // XHS 미제공(항상 null 키)
	require.Equal(t, 45, it.Duration)                         // nc.Video.Capa.Duration
	require.Equal(t, "https://www.xiaohongshu.com/explore/abc?xsec_source=pc_feed&xsec_token=tok", it.PostURL)
	require.True(t, it.NeedsDetail)
	require.Equal(t, "tok", it.DetailToken)
}

func TestXhsSearchVideoNilDurationZero(t *testing.T) {
	// Video 가 nil 이면 Duration=0(생략). PostURL 은 token 없이도 생성.
	resp := &FeedsListResponse{Feeds: []xiaohongshu.Feed{{ID: "x1", NoteCard: xiaohongshu.NoteCard{Type: "video"}}}}
	a := NewXhsAdapter(&fakeXhsSearcher{resp: resp})
	page, _ := a.Search(context.Background(), search.SearchQuery{Keyword: "k"})
	require.Equal(t, "https://www.xiaohongshu.com/explore/x1", page.Items[0].PostURL)
	require.Zero(t, page.Items[0].Duration)
}

func TestXhsSearchSetsVideoNoteTypeAndSort(t *testing.T) {
	fs := &fakeXhsSearcher{resp: &FeedsListResponse{}}
	a := NewXhsAdapter(fs)
	_, _ = a.Search(context.Background(), search.SearchQuery{Keyword: "k", Sort: "latest"})
	require.Len(t, fs.last, 1)
	require.Equal(t, "视频", fs.last[0].NoteType)
	require.Equal(t, "最新", fs.last[0].SortBy) // latest → 最新
}

func TestXhsNoUnconditionalPublishBucket(t *testing.T) {
	// date_from 없으면 PublishTime 강제 없음(빈값=不限).
	fs := &fakeXhsSearcher{resp: &FeedsListResponse{}}
	a := NewXhsAdapter(fs)
	_, _ = a.Search(context.Background(), search.SearchQuery{Keyword: "k"})
	require.Empty(t, fs.last[0].PublishTime, "date_from 없으면 PublishTime 강제 금지")
}

func TestXhsSafePublishBucketFromRecentDate(t *testing.T) {
	// date_from 이 오늘 → 一天内 (안전 매핑).
	require.Equal(t, "一天内", xhsPublishBucket(time.Now().Format(time.RFC3339), ""))
}

func TestXhsAvailableUsesRealLoginStatus(t *testing.T) {
	a := NewXhsAdapter(&fakeXhsSearcher{resp: &FeedsListResponse{}, loggedIn: true})
	require.True(t, a.Available(context.Background()).Available)
}

func TestXhsUnavailableWhenLoggedOutOrProbeFails(t *testing.T) {
	loggedOut := NewXhsAdapter(&fakeXhsSearcher{resp: &FeedsListResponse{}, loggedIn: false})
	require.False(t, loggedOut.Available(context.Background()).Available)
	probeFail := NewXhsAdapter(&fakeXhsSearcher{resp: &FeedsListResponse{}, loginErr: errors.New("secret detail")})
	av := probeFail.Available(context.Background())
	require.False(t, av.Available)
	require.NotContains(t, av.Reason, "secret detail")
}
```

- [ ] **Step 2: 실패 확인** — `go test ./ -run TestXhs -v` → FAIL.

- [ ] **Step 3: 구현** — `adapter_xhs.go`(adapter 가 cookies 를 직접 읽지 않음 — 중복 구현 금지):

```go
package main

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/xpzouying/xiaohongshu-mcp/search"
	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

// XhsService: 검색과 실제 로그인 readiness 를 함께 제공하는 최소 인터페이스.
type XhsService interface {
	SearchFeeds(ctx context.Context, keyword string, filters ...xiaohongshu.FilterOption) (*FeedsListResponse, error)
	CheckLoginStatus(ctx context.Context) (*LoginStatusResponse, error)
}

// XhsAdapter: XHS(go-rod) 영상 검색 어댑터. 영상 URL 은 detail 호출에서 획득(NeedsDetail=true).
type XhsAdapter struct {
	service XhsService
}

func NewXhsAdapter(s XhsService) *XhsAdapter {
	return &XhsAdapter{service: s}
}

func (a *XhsAdapter) Name() string { return "xiaohongshu" }

// Available: 기존 QR/cookies 세션을 실제 페이지 probe 로 검증한다.
func (a *XhsAdapter) Available(ctx context.Context) search.Availability {
	status, err := a.service.CheckLoginStatus(ctx)
	if err != nil || status == nil || !status.IsLoggedIn {
		return search.Availability{Available: false, Reason: "샤오홍슈 로그인이 필요합니다. 설정에서 QR 로그인을 완료해 주세요."}
	}
	return search.Availability{Available: true}
}

func (a *XhsAdapter) Search(ctx context.Context, q search.SearchQuery) (search.AdapterSearchPage, error) {
	opt := xiaohongshu.FilterOption{NoteType: "视频"} // 영상만
	switch q.Sort {
	case "popularity":
		opt.SortBy = "最多点赞"
	case "latest":
		opt.SortBy = "最新"
	}
	opt.PublishTime = xhsPublishBucket(q.Filters.DateFrom, q.Filters.DateTo)

	resp, err := a.service.SearchFeeds(ctx, q.Keyword, opt)
	if err != nil {
		return search.AdapterSearchPage{}, err
	}
	items := make([]search.VideoItem, 0, len(resp.Feeds))
	for _, feed := range resp.Feeds {
		items = append(items, xhsFeedToVideoItem(feed))
	}
	// XHS 검색은 M1 단일 페이지(SearchFeeds 가 cursor 미지원).
	return search.AdapterSearchPage{Items: items, NextCursor: "", HasMore: false}, nil
}

// xhsFeedToVideoItem: xiaohongshu.Feed → search.VideoItem.
func xhsFeedToVideoItem(feed xiaohongshu.Feed) search.VideoItem {
	nc := feed.NoteCard
	cover := nc.Cover.URLDefault
	if cover == "" {
		cover = nc.Cover.URLPre
	}
	author := nc.User.Nickname
	if author == "" {
		author = nc.User.NickName
	}
	vi := search.VideoItem{
		Platform:     "xiaohongshu",
		PostID:       feed.ID,
		PostURL:      xhsPostURL(feed.ID, feed.XsecToken),
		ThumbnailURL: cover,
		Title:        nc.DisplayTitle,
		Author:       author,
		Likes:        parseCount(nc.InteractInfo.LikedCount),
		Comments:     parseCount(nc.InteractInfo.CommentCount),
		Favorites:    parseCount(nc.InteractInfo.CollectedCount),
		Shares:       parseCount(nc.InteractInfo.SharedCount),
		Views:        nil, // XHS 미지원(항상 null 키)
		NeedsDetail:  true,
		DetailToken:  feed.XsecToken,
	}
	if nc.Video != nil { // Video 는 *Video(nil 가능)
		vi.Duration = nc.Video.Capa.Duration // 초 단위
	}
	return vi
}

// xhsPostURL: XHS explore URL(note_id + xsec_token). 원본 보기 fallback 용.
func xhsPostURL(noteID, xsecToken string) string {
	if noteID == "" {
		return ""
	}
	u := &url.URL{Scheme: "https", Host: "www.xiaohongshu.com", Path: "/explore/" + noteID}
	if xsecToken != "" {
		q := u.Query()
		q.Set("xsec_token", xsecToken)
		q.Set("xsec_source", "pc_feed")
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// parseCount: "1.2万"/"5千"/"100" 같은 XHS 문자열 카운트 → *int64. 빈값/해석불가 → nil.
func parseCount(s string) *int64 {
	n, ok := parseChineseCount(s)
	if !ok {
		return nil
	}
	return &n
}

// parseChineseCount: 한자 단위(万/亿/千/百) + 소수 파싱.
func parseChineseCount(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "亿"):
		mult = 100_000_000
		s = strings.TrimSuffix(s, "亿")
	case strings.HasSuffix(s, "万"):
		mult = 10_000
		s = strings.TrimSuffix(s, "万")
	case strings.HasSuffix(s, "千"):
		mult = 1_000
		s = strings.TrimSuffix(s, "千")
	case strings.HasSuffix(s, "百"):
		mult = 100
		s = strings.TrimSuffix(s, "百")
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return int64(f * float64(mult)), true
}

// xhsPublishBucket: date_from 기반 안전 XHS publish_time 버킷. 무조건 一周内 강제 금지.
func xhsPublishBucket(dateFrom, dateTo string) string {
	if dateFrom == "" {
		return "" // 不限
	}
	t, ok := tryParseAnyDate(dateFrom)
	if !ok {
		return ""
	}
	ageDays := time.Since(t).Hours() / 24
	switch {
	case ageDays <= 1:
		return "一天内"
	case ageDays <= 7:
		return "一周内"
	case ageDays <= 180:
		return "半年内"
	default:
		return "" // 不限(반년 초과)
	}
}

// tryParseAnyDate: date-only 또는 RFC3339 파싱.
func tryParseAnyDate(s string) (time.Time, bool) {
	for _, l := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
```

- [ ] **Step 4: 통과** — `go test ./ -run TestXhs -v` → PASS(7 케이스).
- [ ] **Step 5: 커밋**

```bash
gofmt -w adapter_xhs.go adapter_xhs_test.go
git add adapter_xhs.go adapter_xhs_test.go
git commit -m "feat(api): add XHS adapter with real login readiness, PostURL, and Duration"
```



---

## Task 10: capabilities + 통합 검색 핸들러 + 서버 wiring (통합)

> **통합 사유**: capabilities/handlers 와 AppServer 필드/aggregator wiring 은 단독으로 green/commit 불가. 한 태스크로 통합.

**Files:**
- Create: `capabilities.go`, `handlers_search.go`, Test `handlers_search_test.go`
- Modify: `app_server.go`(aggregator 필드 + 빌드), `routes.go`(라우트 2개), `search/aggregator.go`(`Availability` 메서드 추가)

**Interfaces:** Consumes `search.AggregatorService`/`search.AggregatorRequest`(Task 5), `SidecarClient`(Task 6), 3 어댑터(Task 7/8/9). Produces `POST /api/v1/search`, `GET /api/v1/search/capabilities`, `searchCapabilitiesData`, AppServer.aggregator.

> **capability 계약(spec §HTTP endpoints)**: 응답 data 는 `{platforms:{platform: PlatformCapability}, merge_rule:"per_platform_rank"}`. 정렬·지표·native/post 필터·pagination·가용성은 플랫폼별로 둔다. `pagination`: XHS/TikTok=`single_page`, Douyin=`opaque_cursor`. `available` 은 각 어댑터 `Available()`의 동적 결과다.

- [ ] **Step 1: 실패 테스트**

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/search"
)

// mainFakeAdapter: 핸들러 테스트용(search 패키지 fake 과 별개).
type mainFakeAdapter struct {
	name  string
	items []search.VideoItem
}

func (f *mainFakeAdapter) Name() string { return f.name }
func (f *mainFakeAdapter) Available(ctx context.Context) search.Availability {
	return search.Availability{Available: true}
}
func (f *mainFakeAdapter) Search(ctx context.Context, q search.SearchQuery) (search.AdapterSearchPage, error) {
	return search.AdapterSearchPage{Items: f.items}, nil
}

func newTestApp(t *testing.T) *AppServer {
	gin.SetMode(gin.TestMode)
	app := NewAppServer(NewXiaohongshuService())
	app.aggregator = search.NewAggregatorService(map[string]search.VideoAdapter{
		"xiaohongshu": &mainFakeAdapter{name: "xiaohongshu", items: []search.VideoItem{{Platform: "xiaohongshu", PostID: "a"}}},
		"douyin":      &mainFakeAdapter{name: "douyin", items: []search.VideoItem{{Platform: "douyin", PostID: "b"}}},
	})
	return app
}

func TestUnifiedSearchReturnsMergedItems(t *testing.T) {
	app := newTestApp(t)
	router := setupRoutes(app)
	body, _ := json.Marshal(search.AggregatorRequest{Keyword: "便携风扇", Platforms: []string{"xiaohongshu", "douyin"}, Sort: "relevance"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	var wrap struct {
		Success bool                     `json:"success"`
		Data    *search.AggregatedResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &wrap))
	require.True(t, wrap.Success)
	require.Len(t, wrap.Data.Items, 2)
}

func TestUnifiedSearchRejectsInvalidSort(t *testing.T) {
	app := newTestApp(t)
	router := setupRoutes(app)
	body, _ := json.Marshal(search.AggregatorRequest{Keyword: "k", Sort: "bogus"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestUnifiedSearchRejectsInvalidPlatforms(t *testing.T) {
	app := newTestApp(t)
	router := setupRoutes(app)
	body, _ := json.Marshal(search.AggregatorRequest{Keyword: "k", Platforms: []string{"instagram"}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestUnifiedSearchRejectsEmptyKeyword(t *testing.T) {
	app := newTestApp(t)
	router := setupRoutes(app)
	body, _ := json.Marshal(search.AggregatorRequest{Keyword: "  "})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestSearchCapabilitiesEndpoint(t *testing.T) {
	app := newTestApp(t)
	router := setupRoutes(app)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/search/capabilities", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	var wrap struct {
		Success bool                    `json:"success"`
		Data    searchCapabilitiesData `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &wrap))
	require.True(t, wrap.Success)
	require.Equal(t, "per_platform_rank", wrap.Data.MergeRule)
	require.Equal(t, "opaque_cursor", wrap.Data.Platforms["douyin"].Pagination)
	require.Equal(t, "single_page", wrap.Data.Platforms["xiaohongshu"].Pagination)
	require.Equal(t, "single_page", wrap.Data.Platforms["tiktok"].Pagination)
	require.Contains(t, wrap.Data.Platforms["douyin"].Sorts, "popularity")
	require.Contains(t, wrap.Data.Platforms["douyin"].NativeFilters, "duration")
	require.True(t, wrap.Data.Platforms["douyin"].Available) // 동적 가용성(fake=true)
}
```

- [ ] **Step 2: 실패 확인** — `go test ./ -run 'TestUnifiedSearch|TestSearchCapabilities' -v` → FAIL(컴파일 에러 포함).

- [ ] **Step 3a: capabilities 구현** — `capabilities.go`:

```go
package main

import "github.com/xpzouying/xiaohongshu-mcp/search"

type platformCapability struct {
	Sorts           []string `json:"sorts"`
	Metrics         []string `json:"metrics"`
	NativeFilters   []string `json:"native_filters"`
	PostFilters     []string `json:"post_filters"`
	Pagination      string   `json:"pagination"` // single_page|opaque_cursor
	Available       bool     `json:"available"`
	UnsupportedNote string   `json:"unsupported_note"`
}

type searchCapabilitiesData struct {
	Platforms map[string]platformCapability `json:"platforms"`
	MergeRule string                        `json:"merge_rule"`
}

func searchPlatformCapabilities(avail map[string]search.Availability) map[string]platformCapability {
	commonPost := []string{"include_keywords", "exclude_keywords", "min_likes", "min_comments", "min_favorites"}
	return map[string]platformCapability{
		"xiaohongshu": {
			Sorts: []string{"relevance", "popularity", "latest"},
			Metrics: []string{"likes", "comments", "favorites", "shares", "duration"},
			NativeFilters: []string{"keyword", "sort", "publish_time", "video_only"},
			PostFilters: append(append([]string{}, commonPost...), "date_from", "date_to", "duration_min", "duration_max"),
			Pagination: "single_page", Available: avail["xiaohongshu"].Available,
			UnsupportedNote: "조회수 미지원; M1 단일 페이지",
		},
		"douyin": {
			Sorts: []string{"relevance", "popularity", "latest"},
			Metrics: []string{"likes", "comments", "favorites", "views", "shares", "duration"},
			NativeFilters: []string{"keyword", "sort", "publish_time", "duration", "video_only"},
			PostFilters: append(append([]string{}, commonPost...), "min_views", "date_from", "date_to", "duration_min", "duration_max"),
			Pagination: "opaque_cursor", Available: avail["douyin"].Available,
		},
		"tiktok": {
			Sorts: []string{"relevance", "popularity", "latest"},
			Metrics: []string{"likes", "comments", "favorites", "views", "shares", "duration"},
			NativeFilters: []string{"keyword", "video_only"},
			PostFilters: append(append([]string{}, commonPost...), "min_views", "date_from", "date_to", "duration_min", "duration_max"),
			Pagination: "single_page", Available: avail["tiktok"].Available,
			UnsupportedNote: "인기/최신은 반환 집합 후처리; M1 단일 페이지",
		},
	}
}
```

- [ ] **Step 3b: 핸들러 구현** — `handlers_search.go`:

```go
package main

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/xpzouying/xiaohongshu-mcp/search"
)

var validPlatformSet = map[string]bool{
	"xiaohongshu": true,
	"douyin":      true,
	"tiktok":      true,
}

// validSort: 허용 정렬 키(폴백 없음).
func validSort(s string) bool {
	return s == "relevance" || s == "popularity" || s == "latest"
}

// validPlatforms: 모든 platform 이 허용 집합에 포함되는지.
func validPlatforms(ps []string) bool {
	for _, p := range ps {
		if !validPlatformSet[p] {
			return false
		}
	}
	return true
}

// searchCapabilitiesHandler: GET /api/v1/search/capabilities
func (s *AppServer) searchCapabilitiesHandler(c *gin.Context) {
	avail := s.aggregator.Availability(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{"success": true, "data": searchCapabilitiesData{
		Platforms: searchPlatformCapabilities(avail),
		MergeRule:  "per_platform_rank",
	}})
}

// unifiedSearchHandler: POST /api/v1/search — 통합 영상 검색.
func (s *AppServer) unifiedSearchHandler(c *gin.Context) {
	var req search.AggregatorRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "잘못된 요청입니다."})
		return
	}
	req.Keyword = strings.TrimSpace(req.Keyword)
	if req.Keyword == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "검색어를 입력해 주세요."})
		return
	}
	if req.Sort == "" {
		req.Sort = "relevance"
	}
	if !validSort(req.Sort) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "sort 는 relevance|popularity|latest 만 허용됩니다."})
		return
	}
	if !validPlatforms(req.Platforms) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "platforms 는 xiaohongshu|douyin|tiktok 만 허용됩니다."})
		return
	}
	if req.Filters.PerPlatformLimit <= 0 {
		req.Filters.PerPlatformLimit = 15
	}
	req.Filters.VideoOnly = true // M1 은 영상 검색 전용; false 입력도 서버에서 true 로 정규화

	res, err := s.aggregator.Search(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "검색 중 오류가 발생했습니다."})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": res})
}
```

- [ ] **Step 3c: aggregator.Availability 추가** — `search/aggregator.go`에 메서드 추가(capability 동적 가용성용):

```go
// Availability 는 각 어댑터의 현재 가용성을 반환(capability endpoint 용).
func (s *AggregatorService) Availability(ctx context.Context) map[string]Availability {
	out := make(map[string]Availability, len(s.adapters))
	for name, ad := range s.adapters {
		out[name] = ad.Available(ctx)
	}
	return out
}
```

- [ ] **Step 3d: AppServer wiring** — `app_server.go` 수정: 구조체에 `aggregator *search.AggregatorService` 필드 추가, `NewAppServer` 에서 sidecar+어댑터+aggregator 빌드.

```go
// app_server.go (수정분 발췌) — 기존 필드/초기화는 유지하고 아래만 추가.
type AppServer struct {
	// ...기존 필드...
	aggregator *search.AggregatorService
}

func NewAppServer(svc *XiaohongshuService) *AppServer {
	// ...기존 초기화 유지...
	app := &AppServer{
		// ...기존 필드...
	}
	// 통합 검색 aggregator 조립.
	sidecarURL := "http://127.0.0.1:18061"
	if v := os.Getenv("SIDECAR_URL"); v != "" {
		sidecarURL = v
	}
	sidecar := NewSidecarClient(sidecarURL, 0)
	app.aggregator = search.NewAggregatorService(map[string]search.VideoAdapter{
		"xiaohongshu": NewXhsAdapter(svc), // *XiaohongshuService 는 XhsService 만족
		"douyin":      NewDouyinAdapter(sidecar),
		"tiktok":      NewTikTokAdapter(sidecar),
	})
	return app
}
```
> 기존 NewAppServer 본문/필드는 유지하고 위 필드·조립 코드를 추가. `os` import 추가(미사용 시 제거).

- [ ] **Step 3e: 라우트 추가** — `routes.go` 의 `/api/v1` 그룹에:

```go
api.POST("/search", appServer.unifiedSearchHandler)
api.GET("/search/capabilities", appServer.searchCapabilitiesHandler)
```

- [ ] **Step 4: 통과** — `go test ./ -run 'TestUnifiedSearch|TestSearchCapabilities|TestStaticRoutes' -v` → PASS. `go build ./...` 성공.
- [ ] **Step 5: 커밋**

```bash
gofmt -w capabilities.go handlers_search.go handlers_search_test.go search/aggregator.go app_server.go routes.go
git add capabilities.go handlers_search.go handlers_search_test.go search/aggregator.go app_server.go routes.go
git commit -m "feat(api): add unified search + capabilities endpoints with dynamic availability and 400 validation"
```



---

## Task 11: Python 사이드카 scaffold + /healthz

**Files (Create `tiktok-sidecar/`):**
- `app.py` — FastAPI 앱 + `/healthz` + SYNC `/search`(라우팅만, 구현은 Task 12).
- `models.py` — `SearchFilters`, `SearchRequest`(body `platform` 포함).
- `requirements.txt` — `fastapi`, `uvicorn`, `httpx`, `TikTokApi==7.3.3`, `playwright`, `gmssl`.
- `README.md` — 실행/환경변수/Playwright 설치.
- `tests/test_healthz.py`.

**Interfaces (Go Task 6 와 정확 일치):**
- `/healthz` → `{status:"ok", douyin, tiktok, llm}` bool(평문 비밀 미포함).
- `/search` SYNC 라우트. body `SearchRequest{platform,q,count,sort,cursor,filters}`. 응답 래핑 `{success:true,data:{videos,next_cursor,has_more}}`. 503=unavailable, 502=bad gateway, 400=unsupported platform.
- `SearchFilters` 필드는 Go `search.SearchFilters` 와 1:1.
- `search_douyin(payload, signer=Signer())`/`search_tiktok(payload)` 는 모두 **SYNC** 함수(dict 반환). Douyin signer 는 라우트가 명시적으로 주입한다. Task 12 구현.

- [ ] **Step 1: 실패 테스트** — `tests/test_healthz.py`:

```python
import importlib

def _reload_app(monkeypatch, env):
    for k in ("DOUYIN_COOKIE", "TT_MSTOKEN", "LLM_API_KEY"):
        monkeypatch.delenv(k, raising=False)
    for k, v in env.items():
        monkeypatch.setenv(k, v)
    import app
    importlib.reload(app)
    return app

def test_healthz_shape_and_all_false(monkeypatch):
    app = _reload_app(monkeypatch, {})
    assert app.health_state() == {"status": "ok", "douyin": False, "tiktok": False, "llm": False}

def test_healthz_reflects_env(monkeypatch):
    app = _reload_app(monkeypatch, {"DOUYIN_COOKIE": "x=1", "TT_MSTOKEN": "tok"})
    # health_state 는 항상 status:"ok" 포함(Go sidecarHealth.Status 계약) — 누락 금지.
    assert app.health_state() == {"status": "ok", "douyin": True, "tiktok": True, "llm": False}

def test_healthz_endpoint_ok_status(client, monkeypatch):
    _reload_app(monkeypatch, {"DOUYIN_COOKIE": "x=1"})
    r = client.get("/healthz")
    assert r.status_code == 200
    body = r.json()
    assert body["status"] == "ok"          # Go sidecarHealth.Status 계약
    assert body["douyin"] is True
    assert "x=1" not in r.text             # 평문 비밀 미노출
```

> `conftest.py` 에 `client` fixture(TestClient, reload 된 app) 추가.

- [ ] **Step 2: 실패 확인** — `cd tiktok-sidecar && pytest tests/test_healthz.py -v` → FAIL(모듈 없음).

- [ ] **Step 3: 구현**

`tiktok-sidecar/models.py` — Go `search.SearchFilters`/`SidecarSearchRequest` 와 1:1:
```python
from pydantic import BaseModel, Field

class SearchFilters(BaseModel):
    include_keywords: list[str] = Field(default_factory=list)
    exclude_keywords: list[str] = Field(default_factory=list)
    date_from: str = ""
    date_to: str = ""
    duration_min: int = 0
    duration_max: int = 0
    min_likes: int = 0
    min_comments: int = 0
    min_favorites: int = 0
    min_views: int = 0
    video_only: bool = False
    per_platform_limit: int = 0            # 0=미지정(상한 없음)

class SearchRequest(BaseModel):
    platform: str                          # douyin|tiktok (body, query 사용 금지)
    q: str
    count: int = 15
    sort: str = "relevance"
    cursor: str = ""
    filters: SearchFilters = Field(default_factory=SearchFilters)

# VideoItem 은 dict 로 직접 구성(Go sidecarVideo 와 동일 스키마). 별도 모델 불필요.
```

`tiktok-sidecar/app.py`:
```python
import os
from fastapi import FastAPI
from fastapi.responses import JSONResponse
from models import SearchRequest

app = FastAPI(title="video-search-sidecar")

# 평문 비밀은 절대 응답/로그에 노출하지 않는다.
def health_state() -> dict:
    return {
        "status": "ok",
        "douyin": bool(os.getenv("DOUYIN_COOKIE", "")),
        "tiktok": bool(os.getenv("TT_MSTOKEN", "")),
        "llm": bool(os.getenv("LLM_API_KEY", "")),
    }

@app.get("/healthz")
def healthz():
    return JSONResponse(health_state())

def _ok(data: dict) -> dict:
    # Go sidecarSearchEnvelope 계약: {success:true,data:{...}}
    return {"success": True, "data": data}

def _fail(status: int) -> JSONResponse:
    return JSONResponse({"success": False, "data": {}}, status_code=status)

# SYNC 라우트. search_douyin/search_tiktok 모두 동기 함수(dict 반환).
@app.post("/search")
def search(payload: SearchRequest):
    if payload.platform == "douyin":
        from douyin import search_douyin, DouyinUnavailable, DouyinSearchFailed
        from sign import Signer
        try:
            # search_douyin 은 signer 필수 — 라우트가 기본 Signer() 를 주입하지 않으면 production 500.
            return _ok(search_douyin(payload, signer=Signer()))
        except DouyinUnavailable:
            return _fail(503)
        except DouyinSearchFailed:
            return _fail(502)
    if payload.platform == "tiktok":
        from tiktok import search_tiktok, TikTokUnavailable, TikTokSearchFailed
        try:
            return _ok(search_tiktok(payload))
        except TikTokUnavailable:
            return _fail(503)
        except TikTokSearchFailed:
            return _fail(502)
    return _fail(400)

if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="127.0.0.1", port=18061)
```

`tiktok-sidecar/requirements.txt`:
```
fastapi==0.115.0
uvicorn==0.30.0
httpx==0.27.0
TikTokApi==7.3.3
playwright==1.45.0
gmssl==3.2.2
pytest==8.3.0
```

`tiktok-sidecar/conftest.py`:
```python
import importlib
import pytest
from fastapi.testclient import TestClient

@pytest.fixture
def client(monkeypatch):
    import app
    importlib.reload(app)
    # app 은 모듈, app.app 은 FastAPI 인스턴스 — TestClient 에는 인스턴스를 넘긴다.
    return TestClient(app.app)
```

`tiktok-sidecar/README.md`:
```markdown
# video-search-sidecar

Go 메인 서버(127.0.0.1:18060) 전용 Douyin/TikTok 검색 사이드카(127.0.0.1:18061, 외부 미공개).

## 실행
\`\`\`bash
python -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
playwright install chromium   # TikTokApi 7.x 세션 생성에 필요
export DOUYIN_COOKIE='ttwid=...; sessionid=...'   # Douyin(선택)
export TT_MSTOKEN='...'        # TikTok(선택, 갱신 필요)
uvicorn app:app --host 127.0.0.1 --port 18061
\`\`\`

## 엔드포인트
- GET /healthz → {status:"ok", douyin, tiktok, llm} bool(평문 비밀 미포함)
- POST /search (body: {platform,q,count,sort,cursor,filters}) → {success:true,data:{videos,next_cursor,has_more}}

## 비밀
DOUYIN_COOKIE/TT_MSTOKEN 은 본 프로세스 env 에만 존재. Go 는 /healthz bool 만 읽음.
```

- [ ] **Step 4: 통과** — `cd tiktok-sidecar && pytest tests/test_healthz.py -v` → PASS.
- [ ] **Step 5: 커밋**

```bash
git add tiktok-sidecar/
git commit -m "feat(sidecar): add FastAPI scaffold with /healthz (status:ok) and SYNC /search"
```

---

## Task 12: 사이드카 /search 실제 구현 (Douyin abogus + TikTok-Api 7.3.3)

> **production TODO/빈 응답 0건**: 실제 Douyin web search(item) + a_bogus 서명, 실제 TikTok-Api 7.3.3 async video 검색. 단위 테스트는 signer/client/http 를 stub(외부 네트워크 미의존).

**Files:**
- Create: `douyin.py`, `tiktok.py`, `sign.py`, `abogus.py`(vendored), `tests/test_douyin.py`, `tests/test_tiktok.py`.
- Vendor: Evil0ctal `crawlers/douyin/web/abogus.py`(verbatim).

**Interfaces:** Consumes `models.SearchRequest`, env `DOUYIN_COOKIE`/`TT_MSTOKEN`. Produces `/search` 응답 `{videos, next_cursor, has_more}`. Go SidecarClient(Task 6) 와 JSON 계약 일치.

### 12-A: Douyin 실제 검색

- [ ] **Step A1: 실패 테스트** — `tests/test_douyin.py`(stub signer + stub http):

```python
import base64, json
import douyin
from sign import FakeSigner
from models import SearchRequest, SearchFilters

class FakeResp:
    def __init__(self, payload, status=200): self._p = payload; self.status_code = status
    def json(self): return self._p

def test_douyin_maps_aweme_to_videoitem(monkeypatch):
    monkeypatch.setenv("DOUYIN_COOKIE", "ttwid=x")
    payload = {"data": [
        {"aweme_info": {
            "aweme_id": "7001", "desc": "便携风扇", "share_url": "https://v.douyin.com/a",
            "create_time": 1700000000, "duration": 120000,
            "author": {"nickname": "테스터"},
            "statistics": {"digg_count": 1200, "comment_count": 30, "play_count": 9999, "collect_count": 5, "share_count": 2},
            "video": {"cover": {"url_list": ["https://c.jpg"]}, "play_addr": {"url_list": ["https://play.mp4"]}},
        }},
    ], "has_more": 1}
    def fake_get(url, params=None, headers=None, timeout=None): return FakeResp(payload)
    req = SearchRequest(platform="douyin", q="便携风扇", sort="popularity", count=15)
    out = douyin.search_douyin(req, signer=FakeSigner(), http_get=fake_get)
    assert len(out["videos"]) == 1
    v = out["videos"][0]
    assert v["platform"] == "douyin"
    assert v["post_id"] == "7001"
    assert v["likes"] == 1200
    assert v["views"] == 9999
    assert v["duration"] == 120  # ms→s
    assert v["thumbnail_url"] == "https://c.jpg"
    assert v["needs_detail"] is False
    assert out["has_more"] is True
    # 커서는 offset 인코딩(base64 url-safe json)
    raw = json.loads(base64.urlsafe_b64decode(out["next_cursor"]))
    assert raw["offset"] == 15

def test_douyin_cursor_roundtrip(monkeypatch):
    monkeypatch.setenv("DOUYIN_COOKIE", "ttwid=x")
    payload = {"data": [], "has_more": 0}
    captured = {}
    def fake_get(url, params=None, headers=None, timeout=None):
        captured["offset"] = params["offset"]; return FakeResp(payload)
    cur = base64.urlsafe_b64encode(json.dumps({"offset": 45}).encode()).decode()
    out = douyin.search_douyin(SearchRequest(platform="douyin", q="k", cursor=cur), signer=FakeSigner(), http_get=fake_get)
    assert captured["offset"] == "45"
    assert out["next_cursor"] == ""  # has_more=false → 빈 커서

def test_douyin_unavailable_without_cookie(monkeypatch):
    monkeypatch.delenv("DOUYIN_COOKIE", raising=False)
    import pytest
    with pytest.raises(douyin.DouyinUnavailable):
        douyin.search_douyin(SearchRequest(platform="douyin", q="k"), signer=FakeSigner(), http_get=lambda *a, **k: FakeResp({}))

def test_douyin_signer_called(monkeypatch):
    monkeypatch.setenv("DOUYIN_COOKIE", "ttwid=x")
    calls = {}
    class RecSigner:
        def sign(self, params, ua): calls["signed"] = params.copy(); return "SIG"
    def fake_get(url, params=None, headers=None, timeout=None): return FakeResp({"data": [], "has_more": 0})
    douyin.search_douyin(SearchRequest(platform="douyin", q="k"), signer=RecSigner(), http_get=fake_get)
    assert "signed" in calls

def test_douyin_signer_and_http_share_abogus_ua(monkeypatch):
    # a_bogus(vendored abogus) 의 ua_code 는 Windows Chrome 90 UA 로 하드코딩.
    # HTTP User-Agent 와 signer 에 넘긴 UA 가 이 값과 정확히 일치해야 Douyin 이 서명을 수용한다.
    monkeypatch.setenv("DOUYIN_COOKIE", "ttwid=x")
    seen = {}

    class UAProbeSigner:
        def sign(self, params, ua):
            seen["sign_ua"] = ua
            return "SIG"

    def fake_get(url, params=None, headers=None, timeout=None):
        seen["http_ua"] = headers["User-Agent"]
        return FakeResp({"data": [], "has_more": 0})

    douyin.search_douyin(SearchRequest(platform="douyin", q="k"), signer=UAProbeSigner(), http_get=fake_get)
    assert seen["sign_ua"] == seen["http_ua"]          # signer/HTTP 동일 UA
    assert seen["http_ua"] == douyin.UA                # 단일 상수 사용
    assert "Windows NT 10.0" in seen["http_ua"]        # abogus ua_code 일치
    assert "Chrome/90.0.4430.212" in seen["http_ua"]
```

- [ ] **Step A2: 실패 확인** — `pytest tests/test_douyin.py -v` → FAIL.
- [ ] **Step A3: 구현** — `tiktok-sidecar/douyin.py`:

```python
import base64
import json
import os
from datetime import datetime, timezone
import httpx
from models import SearchRequest

VIDEO_SEARCH = "https://www.douyin.com/aweme/v1/web/search/item/"
# a_bogus(vendored abogus) 의 ua_code 는 Windows Chrome 90 UA 로 하드코딩된다
# (ABogus.__init__ 의 user_agent 인수는 주석 처리됨). 따라서 HTTP User-Agent 도
# 반드시 이 값과 동일해야 Douyin 이 서명을 수용한다(불일치 → 서명 거부 → 검색 실패).
UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/90.0.4430.212 Safari/537.36")


class DouyinUnavailable(Exception):
    pass


class DouyinSearchFailed(Exception):
    pass


def _decode_offset(cursor: str) -> int:
    if not cursor:
        return 0
    try:
        raw = base64.urlsafe_b64decode(cursor.encode()).decode()
        return int(json.loads(raw).get("offset", 0))
    except Exception:
        return 0


def _encode_offset(offset: int) -> str:
    return base64.urlsafe_b64encode(json.dumps({"offset": offset}).encode()).decode()


def search_douyin(req: SearchRequest, signer, http_get=httpx.get) -> dict:
    cookie = os.getenv("DOUYIN_COOKIE", "")
    if not cookie:
        raise DouyinUnavailable("cookie missing")
    offset = _decode_offset(req.cursor)
    count = req.count or 15
    params = _build_params(req, offset, count)
    params["a_bogus"] = signer.sign(params, UA)  # a_bogus 서명(vendored abogus)
    headers = {
        "User-Agent": UA,
        "Referer": f"https://www.douyin.com/search/{req.q}",
        "Cookie": cookie,
    }
    try:
        r = http_get(VIDEO_SEARCH, params=params, headers=headers, timeout=20)
    except Exception as e:
        raise DouyinSearchFailed("network") from e
    if r.status_code != 200:
        raise DouyinSearchFailed(f"http {r.status_code}")
    try:
        data = r.json()
    except Exception:
        raise DouyinSearchFailed("decode")
    items = [_map_aweme(a.get("aweme_info") or {})
             for a in data.get("data", []) if a.get("aweme_info")]
    has_more = bool(data.get("has_more", 0))
    return {
        "videos": items,
        "next_cursor": _encode_offset(offset + count) if has_more else "",
        "has_more": has_more,
    }


def _build_params(req: SearchRequest, offset: int, count: int) -> dict:
    f = req.filters
    has_filter = bool(f.date_from or f.duration_min or f.duration_max)
    return {
        "device_platform": "webapp",
        "aid": "6383",
        "channel": "channel_pc_web",
        "search_channel": "aweme_general",
        "keyword": req.q,
        "search_source": "normal_search",
        "query_correct_type": "1",
        "is_filter_search": "1" if has_filter else "0",
        "offset": str(offset),
        "count": str(count),
        "sort_type": _douyin_sort(req.sort),
        "publish_time": _douyin_publish_time(f.date_from, f.date_to),
        "filter_duration": _douyin_duration(f.duration_min, f.duration_max),
        "cookie_enabled": "true",
        "screen_resolution": "1920x1080",
    }


def _douyin_sort(sort: str) -> str:
    # 0 综合(relevance) / 1 最新(latest) / 2 最多点赞(popularity)
    return {"latest": "1", "popularity": "2"}.get(sort, "0")


def _douyin_publish_time(date_from: str, date_to: str) -> str:
    # 안전 버킷(무조건 강제 금지): 0 不限 1 一天内 7 一周内 182 半年内
    if not date_from:
        return "0"
    try:
        t = datetime.fromisoformat(date_from.replace("Z", "+00:00"))
        if t.tzinfo is None:
            t = t.replace(tzinfo=timezone.utc)
    except Exception:
        return "0"
    days = (datetime.now(timezone.utc) - t).days
    if days <= 1:
        return "1"
    if days <= 7:
        return "7"
    if days <= 182:
        return "182"
    return "0"


def _douyin_duration(dmin: int, dmax: int) -> str:
    # 근사 매핑: 0 不限 1 一分钟以下 2 一到五分钟 3 五分钟以上
    if dmin <= 0 and dmax <= 0:
        return "0"
    if dmax and dmax <= 60:
        return "1"
    if dmin and dmin >= 300:
        return "3"
    return "2"


def _map_aweme(aw: dict) -> dict:
    stats = aw.get("statistics") or {}
    author = aw.get("author") or {}
    video = aw.get("video") or {}
    cover_url = ""
    for key in ("origin_cover", "cover"):
        c = video.get(key) or {}
        urls = c.get("url_list") or []
        if urls:
            cover_url = urls[0]
            break
    play_urls = (video.get("play_addr") or {}).get("url_list") or []
    return {
        "platform": "douyin",
        "post_id": str(aw.get("aweme_id", "")),
        "post_url": aw.get("share_url", ""),
        "video_url": play_urls[0] if play_urls else "",
        "thumbnail_url": cover_url,
        "title": aw.get("desc", ""),
        "author": author.get("nickname", ""),
        "published_at": _epoch_to_rfc3339(aw.get("create_time")),
        "duration": int((aw.get("duration") or video.get("duration") or 0) // 1000),  # ms → 초 (aweme_info 우선)
        "likes": _to_int(stats.get("digg_count")),
        "comments": _to_int(stats.get("comment_count")),
        "favorites": _to_int(stats.get("collect_count")),
        "views": _to_int(stats.get("play_count")),
        "shares": _to_int(stats.get("share_count")),
        "needs_detail": False,
    }


def _to_int(v):
    try:
        return int(v)
    except (TypeError, ValueError):
        return None


def _epoch_to_rfc3339(ts) -> str:
    if not ts:
        return ""
    try:
        return datetime.fromtimestamp(int(ts), tz=timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    except Exception:
        return ""
```

`tiktok-sidecar/sign.py`(abogus 래퍼 + 테스트 stub):
```python
# sign.py: a_bogus 서명 래퍼. Evil0ctal vendored abogus.py 의 업스트림 변동을 격리.

class Signer:
    """프로덕션 a_bogus 서명기. vendored abogus(verbatim) 의 ABogus 호출."""

    def sign(self, params: dict, user_agent: str) -> str:
        from abogus import ABogus  # Evil0ctal vendored(verbatim)
        # ABogus(platform=None) 생성자; get_value(url_params: dict|str, method="GET") -> str
        return ABogus().get_value(params)


class FakeSigner:
    """단위 테스트용 고정 서명(외부 의존 제거)."""

    def __init__(self, value: str = "fake_a_bogus"):
        self.value = value

    def sign(self, params: dict, user_agent: str) -> str:
        return self.value
```

> **abogus.py vendoring**: Evil0ctal `Douyin_TikTok_Download_API` 의 `crawlers/douyin/web/abogus.py` 를 commit `42784ffc83a72a516bfe952153ad7e2a3998d16c` 기준 verbatim 복사 → `tiktok-sidecar/abogus.py`. 해당 파일은 원저작자 **JoeanAmier/TikTokDownloader** 의 **GPLv3** 코드(Evil0ctal 수정본)이므로 GPLv3 라이선스와 원저작자 귀속 표기를 `abogus.py` 상단 헤더(이미 포함) 및 README 에 유지한다. 확정 진입점: `ABogus().get_value(params)` (`params` 는 query dict). 의존: `gmssl`(sm3). 본 태스크의 매핑·HTTP·커서 로직은 위 테스트로 검증되며 `Signer.sign` 은 이 확정 진입점만 사용한다(impl 시 조정 항목 없음).

- [ ] **Step A4: 통과** — `pytest tests/test_douyin.py -v` → PASS.

### 12-B: TikTok 실제 검색 (TikTokApi 7.3.3 async)

- [ ] **Step B1: 실패 테스트** — `tests/test_tiktok.py`(fake client):

```python
import tiktok
from models import SearchRequest

class FakeVideo:
    def __init__(self, d): self._d = d
    @property
    def as_dict(self): return self._d

class FakeSearch:
    def __init__(self, items): self._items = items
    async def search_type(self, term, obj_type, count=15, cursor=0):
        for it in self._items:
            yield FakeVideo(it)

class FakeTikTok:
    def __init__(self, items):
        self.search = FakeSearch(items)
        self.created = False; self.closed = False
    async def create_sessions(self, **kw): self.created = True
    async def close_sessions(self): self.closed = True

def test_tiktok_maps_items(monkeypatch):
    monkeypatch.setenv("TT_MSTOKEN", "tok")
    items = [{
        "id": "7100", "desc": "fan review", "createTime": 1700000000,
        "author": {"nickname": "creator"},
        "stats": {"diggCount": 50, "commentCount": 3, "playCount": 1000, "shareCount": 1, "collectCount": 2},
        "video": {"duration": 30, "cover": "https://c.jpg", "playAddr": "https://p.mp4"},
    }]
    fake = FakeTikTok(items)
    out = tiktok.search_tiktok(SearchRequest(platform="tiktok", q="fan", sort="popularity", count=15), client_factory=lambda: fake)
    assert fake.created and fake.closed
    assert len(out["videos"]) == 1
    v = out["videos"][0]
    assert v["platform"] == "tiktok"
    assert v["post_id"] == "7100"
    assert v["likes"] == 50
    assert v["views"] == 1000
    assert v["needs_detail"] is False
    assert out["has_more"] is False  # M1 단일 페이지
    assert out["next_cursor"] == ""

def test_tiktok_popularity_resort(monkeypatch):
    monkeypatch.setenv("TT_MSTOKEN", "tok")
    items = [
        {"id": "1", "stats": {"diggCount": 5}},
        {"id": "2", "stats": {"diggCount": 500}},
    ]
    out = tiktok.search_tiktok(SearchRequest(platform="tiktok", q="k", sort="popularity"), client_factory=lambda: FakeTikTok(items))
    assert out["videos"][0]["post_id"] == "2"  # 좋아요 많은 게 먼저

def test_tiktok_unavailable_without_mstoken(monkeypatch):
    monkeypatch.delenv("TT_MSTOKEN", raising=False)
    import pytest
    with pytest.raises(tiktok.TikTokUnavailable):
        tiktok.search_tiktok(SearchRequest(platform="tiktok", q="k"), client_factory=lambda: FakeTikTok([]))

def test_tiktok_unavailable_when_session_setup_fails(monkeypatch):
    # create_sessions 실패(만료/무효 세션) → platform unavailable(503). 502 가 아니다.
    monkeypatch.setenv("TT_MSTOKEN", "tok")
    class BoomSession(FakeTikTok):
        async def create_sessions(self, **kw): raise RuntimeError("session expired")
    import pytest
    with pytest.raises(tiktok.TikTokUnavailable):
        tiktok.search_tiktok(SearchRequest(platform="tiktok", q="k"), client_factory=lambda: BoomSession([]))

def test_tiktok_search_failed_on_iteration(monkeypatch):
    # 세션은 정상, 검색 반복 중 오류 → bad gateway(502).
    monkeypatch.setenv("TT_MSTOKEN", "tok")
    class RaisingSearch:
        async def search_type(self, *a, **k):
            raise RuntimeError("iter boom")
            yield  # async generator 표식(도달 안 함)
    class BoomIterClient(FakeTikTok):
        def __init__(self):
            super().__init__([])
            self.search = RaisingSearch()
    import pytest
    with pytest.raises(tiktok.TikTokSearchFailed):
        tiktok.search_tiktok(SearchRequest(platform="tiktok", q="k"), client_factory=lambda: BoomIterClient())

def test_tiktok_constructs_post_url_when_shareurl_missing(monkeypatch):
    # shareUrl 이 없으면 https://www.tiktok.com/@{uniqueId}/video/{id} 원본 URL 을 생성한다.
    monkeypatch.setenv("TT_MSTOKEN", "tok")
    items = [{
        "id": "7100", "desc": "fan", "createTime": 1700000000,
        "author": {"uniqueId": "creator88"},
        "stats": {"diggCount": 5},
        "video": {"duration": 5, "cover": "https://c.jpg", "playAddr": "https://p.mp4"},
    }]
    out = tiktok.search_tiktok(SearchRequest(platform="tiktok", q="k"), client_factory=lambda: FakeTikTok(items))
    assert out["videos"][0]["post_url"] == "https://www.tiktok.com/@creator88/video/7100"
```

- [ ] **Step B2: 실패 확인** — `pytest tests/test_tiktok.py -v` → FAIL.
- [ ] **Step B3: 구현** — `tiktok-sidecar/tiktok.py`:

```python
import asyncio
import os
from datetime import datetime, timezone
from models import SearchRequest


class TikTokUnavailable(Exception):
    pass


class TikTokSearchFailed(Exception):
    pass


async def _search_async(req: SearchRequest, client_factory):
    ms = os.getenv("TT_MSTOKEN", "")
    if not ms:
        raise TikTokUnavailable("ms_token missing")
    api = client_factory()
    try:
        # TikTokApi 7.3.3: create_sessions(ms_tokens=[...], num_sessions=1) (복수형).
        # 세션 생성 실패 = 만료/무효 세션 → platform unavailable(503). 검색 반복 실패(502) 와 구분.
        try:
            await api.create_sessions(ms_tokens=[ms], num_sessions=1)
        except Exception as e:
            raise TikTokUnavailable(f"session: {e}") from e
        videos = []
        async for v in api.search.search_type(req.q, "item", count=req.count or 15):
            videos.append(_map_video(v))
        return videos
    finally:
        # 7.3.3 정리: close_sessions() (stop_playwright() 는 public API 아님 — 사용 금지).
        # 세션 생성 실패 직후에도 안전 정리(정리 중 예외가 원본 unavailable 을 masking 하지 않도록 무시).
        try:
            await api.close_sessions()
        except Exception:
            pass


def search_tiktok(req: SearchRequest, client_factory=None) -> dict:
    if client_factory is None:
        from TikTokApi import TikTokApi
        client_factory = lambda: TikTokApi()
    try:
        videos = asyncio.run(_search_async(req, client_factory))
    except TikTokUnavailable:
        raise
    except Exception as e:
        raise TikTokSearchFailed(str(e))
    videos = _apply_sort(videos, req.sort)
    # M1: TikTok-Api async generator 가 cursor/has_more 를 캡슐화해 안정적 페이지네이션 미지원 → 단일 페이지.
    return {"videos": videos, "next_cursor": "", "has_more": False}


def _apply_sort(videos: list, sort: str) -> list:
    if sort == "popularity":
        return sorted(videos, key=lambda v: (v.get("likes") or 0), reverse=True)
    if sort == "latest":
        return sorted(videos, key=lambda v: v.get("published_at") or "", reverse=True)
    return videos


def _map_video(v) -> dict:
    d = _as_dict(v)
    stats = d.get("stats") or {}
    author = d.get("author") or {}
    video = d.get("video") or {}
    play = _first_url(video, "playAddr") or _first_url(video, "downloadAddr")
    cover = _first_url(video, "cover") or _first_url(video, "originCover") or _first_url(video, "dynamicCover")
    post_id = str(d.get("id") or d.get("aweme_id") or "")
    # shareUrl 이 없으면 @uniqueId/video/{id} 원본 URL 생성(원본보기/복사 fallback 용).
    post_url = d.get("shareUrl") or ""
    if not post_url:
        unique_id = author.get("uniqueId") or author.get("unique_id") or ""
        if unique_id and post_id:
            post_url = f"https://www.tiktok.com/@{unique_id}/video/{post_id}"
    return {
        "platform": "tiktok",
        "post_id": post_id,
        "post_url": post_url,
        "video_url": play,
        "thumbnail_url": cover,
        "title": d.get("desc") or "",
        "author": author.get("nickname") or author.get("uniqueId") or author.get("unique_id") or "",
        "published_at": _epoch_to_rfc3339(d.get("createTime")),
        "duration": int(video.get("duration") or 0),  # 초
        "likes": _to_int(stats.get("diggCount")),
        "comments": _to_int(stats.get("commentCount")),
        "favorites": _to_int(stats.get("collectCount")),
        "views": _to_int(stats.get("playCount")),
        "shares": _to_int(stats.get("shareCount")),
        "needs_detail": False,
    }


def _as_dict(v) -> dict:
    ad = getattr(v, "as_dict", None)
    if ad is not None:
        return ad() if callable(ad) else ad
    if isinstance(v, dict):
        return v
    return {}


def _first_url(container: dict, key: str) -> str:
    val = container.get(key)
    if isinstance(val, list) and val:
        return val[0]
    if isinstance(val, str):
        return val
    return ""


def _to_int(v):
    try:
        return int(v)
    except (TypeError, ValueError):
        return None


def _epoch_to_rfc3339(ts) -> str:
    if not ts:
        return ""
    try:
        return datetime.fromtimestamp(int(ts), tz=timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    except Exception:
        return ""
```

- [ ] **Step B4: 통과** — `pytest tests/test_tiktok.py tests/test_douyin.py tests/test_healthz.py -v` → PASS 전체.
### 12-C: /search 라우트 통합(기본 Signer 주입 + platform required)

- [ ] **Step C1: 실패 테스트** — `tests/test_search_route.py`(TestClient 통합):

```python
# tests/test_search_route.py — /search 라우트 통합.
# (1) 라우트가 기본 Signer() 를 주입해 production 500 을 방지하는지,
# (2) platform 이 required(누락 시 422), 미지원 platform 은 400 인지 검증.
import douyin
import sign
import tiktok


def test_search_route_douyin_injects_default_signer(client, monkeypatch):
    # 라우트가 search_douyin(payload) 처럼 signer 없이 호출하면 TypeError → 500.
    # route 가 기본 Signer() 를 주입하는지 통합 검증.
    captured = {}

    def fake_search(req, signer=None, http_get=None):
        captured["signer"] = signer
        return {"videos": [], "next_cursor": "", "has_more": False}

    monkeypatch.setattr(douyin, "search_douyin", fake_search)
    r = client.post("/search", json={"platform": "douyin", "q": "fan"})
    assert r.status_code == 200
    body = r.json()
    assert body["success"] is True
    assert body["data"] == {"videos": [], "next_cursor": "", "has_more": False}
    assert isinstance(captured["signer"], sign.Signer)  # 기본 Signer 주입 확인


def test_search_route_tiktok_passthrough(client, monkeypatch):
    monkeypatch.setattr(tiktok, "search_tiktok",
                        lambda req, client_factory=None: {"videos": [], "next_cursor": "", "has_more": False})
    r = client.post("/search", json={"platform": "tiktok", "q": "fan"})
    assert r.status_code == 200
    assert r.json()["success"] is True


def test_search_route_unsupported_platform_400(client):
    r = client.post("/search", json={"platform": "youtube", "q": "x"})
    assert r.status_code == 400


def test_search_route_missing_platform_422(client):
    # platform 은 required — 누락 시 pydantic 422.
    r = client.post("/search", json={"q": "x"})
    assert r.status_code == 422
```

- [ ] **Step C2: 실패 확인** — `pytest tests/test_search_route.py -v` → FAIL(app.py 가 아직 Signer 주입 전이면 500/TypeError).
- [ ] **Step C3: 통과** — Task 11 app.py 라우트(`search_douyin(payload, signer=Signer())`) 와 함께 `pytest tests/test_search_route.py -v` → PASS(4 케이스).

- [ ] **Step B5: 통합 확인(수동, README)** — 사이드카 기동 후 `curl -s 'http://127.0.0.1:18061/healthz'` 로 bool 확인(비밀 설정 시). 실제 Douyin/TikTok 호출은 antibot/ms_token 상태에 따라 가변적 → M1 acceptance(Task 14) 에서 side 별 unavailable/success 격리로 검증.
- [ ] **Step B6: 커밋**

```bash
git add tiktok-sidecar/douyin.py tiktok-sidecar/tiktok.py tiktok-sidecar/sign.py tiktok-sidecar/abogus.py tiktok-sidecar/tests/
git commit -m "feat(sidecar): implement real Douyin(abogus) and TikTok(7.3.3) search"
```

---

## Task 13: 프론트엔드 재설계 (HTML + lib.js + node:test + app.js + 설정 분리) — 통합

> **통합 사유**: HTML 과 JS(lib.js/app.js) 는 단독 green 불가. 한 태스크로 통합. 사용자가 지적한 app.js 버그 전부 수정 + 순수 로직을 `lib.js`(ESM) 로 분리해 `node:test` 자동 검증. **QR 로그인은 검색 화면에서 제거하고 `/settings` 로컬 설정 영역으로 분리**(검색 사용자에게 인증 프롬프트 미노출).

**Files:**
- Create: `web/lib.js`(순수 ESM), `web/lib.test.mjs`(node:test), `web/package.json`(ESM 선언), `web/settings.html`, `web/settings.js`(QR 로그인, 분리).
- Rewrite: `web/index.html`(로그인 UI 제거), `web/app.js`(검색 전용).
- Modify: `routes.go`(`GET /settings` 라우트 1줄 추가).
- (스타일은 Task 14 에서 `web/style.css` 추가/수정.)

**Interfaces:** Consumes `POST /api/v1/search`(Task 10), `GET /api/v1/search/capabilities`, 기존 `POST /api/v1/feeds/detail`(XHS video URL), `GET/POST /api/v1/login/*`(settings.js 만). Produces 통합 검색 UI(인증 프롬프트 없음).

### 13-A: 순수 로직 lib.js + node:test

- [ ] **Step A1: 실패 테스트** — `web/lib.test.mjs`(node:test):

```js
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  initialState, togglePlatform, toggleAllPlatforms, platformsFromState,
  pendingPlatforms, visibleItems, selectLoadMorePlatforms,
  resetForNewSearch, ingestItems, dedupeKey, parseFilters,
} from "./lib.js";

test("initial platforms are XHS + Douyin and individual toggle keeps one", () => {
  let s = initialState();
  assert.deepEqual(platformsFromState(s), ["xiaohongshu", "douyin"]);
  s = togglePlatform(s, "douyin");
  s = togglePlatform(s, "xiaohongshu"); // 마지막 1개는 해제되지 않음
  assert.deepEqual(platformsFromState(s), ["xiaohongshu"]);
});

test("all toggle selects all and toggling again restores core defaults", () => {
  let s = toggleAllPlatforms(initialState());
  assert.deepEqual(platformsFromState(s), ["xiaohongshu", "douyin", "tiktok"]);
  s = toggleAllPlatforms(s);
  assert.deepEqual(platformsFromState(s), ["xiaohongshu", "douyin"]);
});

test("off hides locally and newly-on platform is pending until search", () => {
  let s = initialState();
  s.items = [
    { platform: "xiaohongshu", post_id: "x" },
    { platform: "douyin", post_id: "d" },
    { platform: "tiktok", post_id: "t" },
  ];
  s.lastReqPlatforms = ["xiaohongshu", "douyin"];
  s = togglePlatform(s, "douyin");
  assert.deepEqual(visibleItems(s).map((v) => v.post_id), ["x"]); // 재검색 없이 숨김
  s = togglePlatform(s, "tiktok");
  assert.deepEqual(pendingPlatforms(s), ["tiktok"]);
});

test("resetForNewSearch clears cursors/items/hasMore", () => {
  let s = initialState();
  s.pageCursors = { xiaohongshu: "x", douyin: "y", tiktok: "z" };
  s.items = [{ platform: "douyin", post_id: "1" }];
  s.hasMore = { xiaohongshu: true, douyin: true, tiktok: true };
  s = resetForNewSearch(s, "k", "latest");
  assert.equal(s.keyword, "k");
  assert.equal(s.sort, "latest");
  assert.equal(s.pageCursors.douyin, "");
  assert.equal(s.items.length, 0);
  assert.equal(s.hasMore.douyin, false);
});

test("ingestItems appends merged data.items and clears cursor when next_cursor empty", () => {
  let s = initialState();
  s.pageCursors.douyin = "old";
  const agg = {
    items: [{ platform: "douyin", post_id: "a" }],          // 서버 머지 결과(탑레벨)
    sides: { douyin: { available: { available: true }, next_cursor: "", has_more: false } },
  };
  s = ingestItems(s, agg, ["douyin"]);
  assert.equal(s.items.length, 1);                           // data.items 에서 append
  assert.equal(s.pageCursors.douyin, "");
  assert.equal(s.hasMore.douyin, false);
});

test("selectLoadMorePlatforms filters by has_more and lastReq", () => {
  let s = toggleAllPlatforms(initialState());
  s.hasMore = { xiaohongshu: false, douyin: true, tiktok: true };
  s.lastReqPlatforms = ["douyin", "tiktok"];
  assert.deepEqual(selectLoadMorePlatforms(s), ["douyin", "tiktok"]);
  s.lastReqPlatforms = ["douyin"];
  assert.deepEqual(selectLoadMorePlatforms(s), ["douyin"]);
});

test("dedupeKey includes platform even when post_url exists", () => {
  assert.equal(dedupeKey({ platform: "douyin", post_id: "1", post_url: "u" }), "douyin:u");
  assert.equal(dedupeKey({ platform: "xiaohongshu", post_id: "1" }), "xiaohongshu:1");
});

test("ingestItems preserves same url across different platforms", () => {
  let s = initialState();
  s = ingestItems(s, { items: [{ platform: "xiaohongshu", post_id: "a", post_url: "u1" }] }, ["xiaohongshu"]);
  s = ingestItems(s, { items: [{ platform: "douyin", post_id: "b", post_url: "u1" }] }, ["douyin"]);
  assert.equal(s.items.length, 2);
});

test("parseFilters splits keywords, parses numbers, video_only, per_platform_limit", () => {
  const f = parseFilters({ include: "风扇 便携", exclude: "避雷", min_likes: "50", duration_min: "60", per_platform_limit: "20", video_only: true });
  assert.deepEqual(f.include_keywords, ["风扇", "便携"]);
  assert.equal(f.min_likes, 50);
  assert.equal(f.duration_min, 60);
  assert.equal(f.per_platform_limit, 20);
  assert.equal(f.video_only, true);
});

test("parseFilters defaults per_platform_limit and fixes video_only true for M1", () => {
  const f = parseFilters({});
  assert.equal(f.per_platform_limit, 15);
  assert.equal(f.video_only, true);
});
```

- [ ] **Step A2: 실패 확인** — `node --test web/lib.test.mjs` → FAIL(모듈 없음).
- [ ] **Step A3: 구현** — `web/lib.js`:

```js
// web/lib.js — 순수 로직(DOM 무의존). node:test 와 app.js 공유.
export const PLATFORMS = ["xiaohongshu", "douyin", "tiktok"];

export function initialState() {
  return {
    keyword: "",
    sort: "relevance",
    selected: new Set(["xiaohongshu", "douyin"]),
    items: [],
    pageCursors: { xiaohongshu: "", douyin: "", tiktok: "" },
    hasMore: { xiaohongshu: false, douyin: false, tiktok: false },
    lastReqPlatforms: [],
  };
}

// togglePlatform: 칩 토글. 최소 1개 유지(모두 해제 금지).
export function togglePlatform(state, name) {
  const next = new Set(state.selected);
  if (next.has(name)) {
    if (next.size === 1) return state;
    next.delete(name);
  } else {
    next.add(name);
  }
  return { ...state, selected: next };
}

export function platformsFromState(state) {
  return PLATFORMS.filter((p) => state.selected.has(p));
}

// '전체'는 3개를 모두 켠다. 이미 전체면 핵심 기본(XHS+Douyin)으로 되돌린다.
export function toggleAllPlatforms(state) {
  const allSelected = PLATFORMS.every((p) => state.selected.has(p));
  return { ...state, selected: new Set(allSelected ? ["xiaohongshu", "douyin"] : PLATFORMS) };
}

// 마지막 검색에 포함되지 않았지만 현재 켜진 플랫폼은 자동 검색하지 않고 pending 표시.
export function pendingPlatforms(state) {
  if (!state.lastReqPlatforms.length) return [];
  return platformsFromState(state).filter((p) => !state.lastReqPlatforms.includes(p));
}

// off 된 플랫폼은 서버 재검색 없이 현재 그리드에서 즉시 숨긴다.
export function visibleItems(state) {
  return state.items.filter((it) => state.selected.has(it.platform));
}

// selectLoadMorePlatforms: has_more 이고 직전 요청에 포함됐던 플랫폼만.
export function selectLoadMorePlatforms(state) {
  return PLATFORMS.filter(
    (p) => state.selected.has(p) && state.hasMore[p] && state.lastReqPlatforms.includes(p)
  );
}

// resetForNewSearch: 새 검색 시 커서/hasMore/아이템 초기화(칩 선택은 유지).
export function resetForNewSearch(state, keyword, sort) {
  return {
    ...initialState(),
    keyword,
    sort,
    selected: state.selected,
  };
}

// ingestItems: 서버가 머지+사후필터한 data.items 를 append. sides 는 status/cursor 용도만.
// 빈 next_cursor 는 커서 클리어(항상 반영). 중복은 dedupeKey 로 제거.
export function ingestItems(state, aggregated, requestedPlatforms) {
  const items = [...state.items, ...(aggregated.items || [])];
  const pageCursors = { ...state.pageCursors };
  const hasMore = { ...state.hasMore };
  for (const p of requestedPlatforms) {
    const side = aggregated.sides && aggregated.sides[p];
    if (!side) continue;
    pageCursors[p] = side.next_cursor || "";
    hasMore[p] = !!(side.has_more && side.next_cursor);
  }
  const seen = new Set();
  const deduped = [];
  for (const it of items) {
    const k = dedupeKey(it);
    if (seen.has(k)) continue;
    seen.add(k);
    deduped.push(it);
  }
  return { ...state, items: deduped, pageCursors, hasMore };
}

export function dedupeKey(it) {
	if (it.post_url) return `${it.platform}:${it.post_url}`;
  return `${it.platform}:${it.post_id}`;
}

// parseFilters: 폼 값 → filters JSON(body). video_only(bool), per_platform_limit(기본 15).
export function parseFilters(values) {
  const f = {};
  const split = (s) => (s || "").split(/[,\s]+/).filter(Boolean);
  const inc = split(values.include);
  const exc = split(values.exclude);
  if (inc.length) f.include_keywords = inc;
  if (exc.length) f.exclude_keywords = exc;
  if (values.date_from) f.date_from = values.date_from;
  if (values.date_to) f.date_to = values.date_to;
  if (values.duration_min) f.duration_min = Number(values.duration_min);
  if (values.duration_max) f.duration_max = Number(values.duration_max);
  if (values.min_likes) f.min_likes = Number(values.min_likes);
  if (values.min_comments) f.min_comments = Number(values.min_comments);
  if (values.min_favorites) f.min_favorites = Number(values.min_favorites);
  if (values.min_views) f.min_views = Number(values.min_views);
  f.video_only = true; // M1 은 영상 검색 전용
  const ppl = Number(values.per_platform_limit);
  f.per_platform_limit = ppl > 0 ? ppl : 15;
  return f;
}
```

`web/package.json`(`.js` 를 ESM 으로 취급 → `node --test` 가 `./lib.js` import 해석):
```json
{
  "type": "module",
  "private": true
}
```

> `web/` 는 `router.Static("/static","./web")` 로 서빙되므로 `/static/package.json` 도 공개되나 비밀 없음(`private:true`). 정적 서빙에 영향 없음.

- [ ] **Step A4: 통과** — `node --test web/lib.test.mjs` → PASS.

### 13-B: HTML 재설계 (검색 전용, 인증 프롬프트 없음)

- [ ] **Step B1: 재작성** — `web/index.html`:

```html
<!doctype html>
<html lang="ko">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>참고영상 통합 검색</title>
  <link rel="stylesheet" href="/static/style.css">
</head>
<body>
  <header class="header">
    <h1>참고영상 통합 검색</h1>
    <p class="subtitle">XHS · Douyin · TikTok — 중국어/영어 검색어로 영상을 찾아보세요</p>
    <a class="settings-link" href="/settings">⚙ 설정 (플랫폼 로그인)</a>
  </header>
  <main id="app">
    <!-- 검색: 인증 프롬프트 없음. 플랫폼 로그인은 /settings 에서. -->
    <section id="search-section">
      <form id="search-form">
        <input id="keyword" type="text" placeholder="검색어 (예: 便携风扇 · travel fan)" autocomplete="off" required>

        <fieldset class="chips" id="platform-chips">
          <legend>플랫폼</legend>
          <button id="platform-all" type="button" aria-pressed="false">전체</button>
          <label><input type="checkbox" data-platform="xiaohongshu" checked> XHS</label>
          <label><input type="checkbox" data-platform="douyin" checked> Douyin</label>
          <label><input type="checkbox" data-platform="tiktok"> TikTok</label>
        </fieldset>

        <label class="sort">정렬
          <select id="sort">
            <option value="relevance">관련도</option>
            <option value="popularity">인기</option>
            <option value="latest">최신</option>
          </select>
        </label>

        <details class="filters">
          <summary>상세 필터</summary>
          <div class="filter-grid">
            <label>포함 키워드 <input id="f-include" type="text" placeholder="쉼표/공백 구분"></label>
            <label>제외 키워드 <input id="f-exclude" type="text"></label>
            <label>시작일 <input id="f-date-from" type="date"></label>
            <label>종료일 <input id="f-date-to" type="date"></label>
            <label>최소 길이(초) <input id="f-duration-min" type="number" min="0"></label>
            <label>최대 길이(초) <input id="f-duration-max" type="number" min="0"></label>
            <label>최소 좋아요 <input id="f-min-likes" type="number" min="0"></label>
            <label>최소 댓글 <input id="f-min-comments" type="number" min="0"></label>
            <label>최소 즐겨찾기 <input id="f-min-favorites" type="number" min="0"></label>
            <label>최소 조회수 <input id="f-min-views" type="number" min="0"></label>
            <label>플랫폼당 한도 <input id="f-per-platform-limit" type="number" min="1" value="15"></label>
            <label class="check"><input id="f-video-only" type="checkbox" checked disabled> 영상만(M1 고정)</label>
          </div>
        </details>

        <button type="submit" id="search-btn">검색</button>
      </form>

      <p id="status"></p>
      <ul id="side-status" class="side-status"></ul>
      <div id="results" class="grid"></div>
      <button id="more-btn" type="button" class="hidden">더 보기</button>
    </section>

    <!-- 영상 모달: 재생 + 원본 보기(재생 실패 대체) -->
    <div id="video-modal" class="modal hidden">
      <div class="modal-body">
        <button id="modal-close" class="modal-close" aria-label="닫기">×</button>
        <video id="video-player" controls playsinline></video>
        <div class="modal-actions">
          <button id="copy-url-btn" type="button">URL 복사</button>
          <a id="original-link" class="original-link" href="#" target="_blank" rel="noopener noreferrer">원본 보기</a>
        </div>
      </div>
    </div>
  </main>
  <script type="module" src="/static/app.js"></script>
</body>
</html>
```

### 13-C: app.js 재작성 (검색 전용 + 원본 보기/재생실패 대체)

- [ ] **Step C1: 재작성** — `web/app.js`:

```js
// web/app.js — DOM wiring(검색 전용). 순수 로직은 lib.js(import). 로그인은 settings.js.
import {
  PLATFORMS, initialState, togglePlatform, toggleAllPlatforms, platformsFromState,
  pendingPlatforms, visibleItems, selectLoadMorePlatforms,
  resetForNewSearch, ingestItems, parseFilters,
} from "./lib.js";

// ====== DOM 헬퍼 ======
const $ = (id) => document.getElementById(id); // bare id ('#' 없음)
const show = (el) => el && el.classList.remove("hidden");
const hide = (el) => el && el.classList.add("hidden");
function escapeHtml(s) {
  return String(s == null ? "" : s).replace(/[&<>"']/g, (c) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[c]));
}
function setStatus(msg, spinner) {
  $("status").innerHTML = (spinner ? '<span class="spinner"></span>' : "") + (msg || "");
}

// ====== 상태 ======
let state = initialState();
let capabilities = null; // GET /api/v1/search/capabilities 결과
let currentItem = null;  // 모달에 띄운 아이템(post_url 포함; 재생실패 대체용)
let currentVideoURL = ""; // detail 로 해결한 URL 포함, modal/copy 의 단일 기준
let lastSearchData = null; // 토글 시 side/pending 상태 재렌더용(탭 메모리만)

// ====== API 클라이언트 (같은 출처) ======
const api = {
  async capabilities() { const r = await fetch("/api/v1/search/capabilities"); return r.json(); },
  async search(body) {
    const r = await fetch("/api/v1/search", {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body),
    });
    return r.json();
  },
  async feedDetail(feedId, xsecToken) {
    const r = await fetch("/api/v1/feeds/detail", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ feed_id: feedId, xsec_token: xsecToken, load_all_comments: false }),
    });
    return r.json();
  },
};

// ====== 필터 값 수집 → filters ======
function collectFilters() {
  return parseFilters({
    include: $("f-include").value,
    exclude: $("f-exclude").value,
    date_from: $("f-date-from").value,
    date_to: $("f-date-to").value,
    duration_min: $("f-duration-min").value,
    duration_max: $("f-duration-max").value,
    min_likes: $("f-min-likes").value,
    min_comments: $("f-min-comments").value,
    min_favorites: $("f-min-favorites").value,
    min_views: $("f-min-views").value,
    per_platform_limit: $("f-per-platform-limit").value,
    video_only: $("f-video-only").checked,
  });
}

// ====== 플랫폼 칩 → state 반영 + capability 기반 필터 비활성화 ======
function syncChipsFromState() {
  document.querySelectorAll("#platform-chips input[data-platform]").forEach((cb) => {
    cb.checked = state.selected.has(cb.dataset.platform);
  });
  const all = $("platform-all");
  if (all) all.setAttribute("aria-pressed", String(PLATFORMS.every((p) => state.selected.has(p))));
}
function applyCapabilityDisabling() {
  if (!(capabilities && capabilities.platforms)) return;
  const sel = platformsFromState(state);
  const hasMetric = (p, key) => (capabilities.platforms[p]?.metrics || []).includes(key);
  const hasFilter = (p, key) => {
    const c = capabilities.platforms[p] || {};
    return [...(c.native_filters || []), ...(c.post_filters || [])].includes(key);
  };
  const toggle = (id, disable) => { const el = $(id); if (el) el.disabled = disable; };
  toggle("f-min-views", sel.every((p) => !hasMetric(p, "views")));
  toggle("f-duration-min", sel.every((p) => !hasFilter(p, "duration_min")));
  toggle("f-duration-max", sel.every((p) => !hasFilter(p, "duration_max")));
  toggle("f-date-to", sel.every((p) => !hasFilter(p, "date_to")));
  toggle("f-date-from", sel.every((p) => !hasFilter(p, "date_from")));
}

// ====== 사이드 상태 패널(status/cursor 용 sides 만 사용) ======
function renderSideStatus(data) {
  const ul = $("side-status");
  const sides = (data && data.sides) || {};
  const labels = { xiaohongshu: "XHS", douyin: "Douyin", tiktok: "TikTok" };
  const rows = PLATFORMS.map((p) => {
    const s = sides[p];
    if (!s) return "";
    const label = labels[p];
    if (!s.available || !s.available.available) {
      const reason = (s.available && s.available.reason) || s.error || "준비 중";
      return `<li class="side-unavailable"><b>${label}</b>: ${escapeHtml(reason)}</li>`;
    }
    return `<li class="side-ok"><b>${label}</b>: (더 보기 ${s.has_more ? "가능" : "불가"})</li>`;
  }).filter(Boolean);
  const pending = pendingPlatforms(state).map(
    (p) => `<li class="side-pending"><b>${labels[p]}</b>: 검색 실행 필요</li>`
  );
  ul.innerHTML = [...rows, ...pending].join("");
}

// ====== 카드 렌더 (data-idx 로 조회, escape 불필요) ======
function metric(v, prefix) { return v == null ? "" : `${prefix} ${Number(v).toLocaleString()}`; }
function cardHTML(it, idx) {
  const meta = [
    metric(it.likes, "❤"), metric(it.comments, "💬"), metric(it.favorites, "🔖"),
    metric(it.views, "👁"), metric(it.shares, "↗"),
  ].filter(Boolean).join(" · ");
  const cover = it.thumbnail_url
    ? `<img class="card-cover" src="${escapeHtml(it.thumbnail_url)}" alt="" loading="lazy">`
    : `<div class="card-cover"></div>`;
  const badge = it.platform === "xiaohongshu" ? "XHS" : it.platform === "douyin" ? "Douyin" : "TikTok";
  return `
    <article class="card" data-idx="${idx}">
      <div style="position:relative">
        ${cover}
        <span class="badge-platform">${badge}</span>
      </div>
      <div class="card-body">
        <p class="card-title">${escapeHtml(it.title)}</p>
        <div class="card-meta"><span>${escapeHtml(it.author)}</span><span>${meta}</span></div>
      </div>
    </article>`;
}

function renderResults() {
  const grid = $("results");
  const items = visibleItems(state);
  if (!items.length) {
    grid.innerHTML = "";
    setStatus("검색 결과가 없습니다.");
    hide($("more-btn"));
    return;
  }
  setStatus(`영상 결과 ${items.length}개`);
  grid.innerHTML = items.map((it) => cardHTML(it, state.items.indexOf(it))).join("");
  if (selectLoadMorePlatforms(state).length > 0) show($("more-btn"));
  else hide($("more-btn"));
}

// ====== 검색 실행 ======
async function runSearch(isMore) {
  const platforms = isMore ? selectLoadMorePlatforms(state) : platformsFromState(state);
  if (platforms.length === 0) {
    setStatus("최소 한 개의 플랫폼을 선택하세요.");
    return;
  }
  const pageCursors = {};
  for (const p of platforms) pageCursors[p] = state.pageCursors[p] || "";
  const body = {
    keyword: state.keyword,
    platforms,
    sort: state.sort,
    page_cursors: pageCursors,
    filters: collectFilters(),
  };
  state.lastReqPlatforms = platforms;
  setStatus(isMore ? "더 불러오는 중…" : "검색 중…", true);
  try {
    const res = await api.search(body);
    if (!res.success) {
      setStatus("오류: " + (res.message || ""));
      return;
    }
    state = ingestItems(state, res.data, platforms);
    lastSearchData = res.data;
    renderSideStatus(res.data);
    renderResults();
  } catch (e) {
    setStatus("서버에 연결할 수 없습니다. 잠시 후 다시 시도해주세요.");
  }
}

// ====== 검색 폼 제출 (입력값을 먼저 읽고 state.keyword 검증) ======
function onSearchSubmit(ev) {
  ev.preventDefault();
  const kw = $("keyword").value.trim(); // 입력값을 먼저 읽는다
  if (!kw) {
    setStatus("검색어를 입력해 주세요.");
    return;
  }
  state = resetForNewSearch(state, kw, $("sort").value); // 커서/hasMore/아이템 리셋
  runSearch(false);
}

// ====== 영상 재생 + 원본 보기(재생 실패 대체) ======
async function playItem(idx) {
  const it = state.items[idx];
  if (!it) return;
  if (it.video_url) { openVideoModal(it, it.video_url); return; } // Douyin/TikTok
  if (!it.needs_detail) {
    if (it.post_url) window.open(it.post_url, "_blank", "noopener");
    else setStatus("재생하거나 열 수 있는 URL 이 없습니다.");
    return;
  }
  setStatus("영상 불러오는 중…", true);
  try {
    const res = await api.feedDetail(it.post_id, it.detail_token);
    if (!res.success || !(res.data && res.data.video_url)) {
      if (it.post_url) window.open(it.post_url, "_blank", "noopener");
      else setStatus("영상 URL 을 가져오지 못했습니다.");
      return;
    }
    openVideoModal(it, res.data.video_url);
    setStatus("");
  } catch (e) {
    if (it.post_url) window.open(it.post_url, "_blank", "noopener");
    else setStatus("영상을 불러오는 중 오류가 발생했습니다.");
  }
}
function openVideoModal(it, url) {
  currentItem = it;
  currentVideoURL = url;
  const v = $("video-player");
  v.src = url;
  // 원본 링크 항상 세팅(post_url 없으면 숨김). 재생 실패 시 대체 경로.
  const link = $("original-link");
  if (it && it.post_url) { link.href = it.post_url; show(link); }
  else { link.removeAttribute("href"); hide(link); }
  show($("video-modal"));
  // 재생 실패(error 이벤트) → 원본 페이지로 대체 이동.
  v.onended = null;
  v.onerror = () => { if (it && it.post_url) { window.open(it.post_url, "_blank", "noopener"); closeVideoModal(); } };
  v.play().catch(() => { if (it && it.post_url) window.open(it.post_url, "_blank", "noopener"); });
}
function closeVideoModal() {
  const v = $("video-player");
  v.pause(); v.removeAttribute("src"); v.onerror = null; v.load();
  currentVideoURL = "";
  currentItem = null;
  hide($("video-modal"));
}
async function copyVideoUrl() {
  const url = currentVideoURL || (currentItem && currentItem.post_url) || "";
  if (!url) return;
  try {
    await navigator.clipboard.writeText(url);
    $("copy-url-btn").textContent = "복사됨 ✓";
    setTimeout(() => ($("copy-url-btn").textContent = "URL 복사"), 1500);
  } catch (e) {
    prompt("이 URL 을 복사하세요:", url);
  }
}

// ====== 초기화 (로그인 없음 — 검색 UI 만) ======
async function init() {
  try {
    const cap = await api.capabilities();
    if (cap && cap.success) capabilities = cap.data;
  } catch (e) { /* 사이드카 미가동 시 무방 */ }

  syncChipsFromState();
  applyCapabilityDisabling();

  $("search-form").addEventListener("submit", onSearchSubmit);
  $("more-btn").addEventListener("click", () => runSearch(true));
  $("platform-chips").addEventListener("change", (ev) => {
    const cb = ev.target.closest("input[data-platform]");
    if (!cb) return;
    state = togglePlatform(state, cb.dataset.platform);
    syncChipsFromState();
    applyCapabilityDisabling();
    renderResults();                 // off 즉시 로컬 hide
    renderSideStatus(lastSearchData); // 새로 on 된 플랫폼은 '검색 실행 필요'
  });
  $("platform-all").addEventListener("click", () => {
    state = toggleAllPlatforms(state);
    syncChipsFromState();
    applyCapabilityDisabling();
    renderResults();
    renderSideStatus(lastSearchData);
  });
  $("results").addEventListener("click", (ev) => {
    const card = ev.target.closest(".card");
    if (!card) return;
    playItem(Number(card.getAttribute("data-idx"))); // data-idx 로 조회(escape 불필요)
  });
  $("modal-close").addEventListener("click", closeVideoModal);
  $("copy-url-btn").addEventListener("click", copyVideoUrl);
  $("video-modal").addEventListener("click", (ev) => {
    if (ev.target === $("video-modal")) closeVideoModal();
  });
  document.addEventListener("keydown", (ev) => { if (ev.key === "Escape") closeVideoModal(); });
}

document.addEventListener("DOMContentLoaded", init);
```

> **수정된 버그 목록(사용자 지적 + 감사 대응)**:
> 1. **입력값 먼저 읽기**: `onSearchSubmit` 이 `$("keyword").value.trim()` 을 먼저 읽고 빈값 검증.
> 2. **`$()` bare id**: 헬퍼가 `id` 만 받고 모든 호출이 `$("f-include")` 식(`#` 없음).
> 3. **새 검색 커서 리셋**: `resetForNewSearch` 가 `pageCursors`/`hasMore`/`items` 초기화.
> 4. **빈 next_cursor 클리어**: `ingestItems` 가 `pageCursors[p] = side.next_cursor || ""`.
> 5. **더보기 플랫폼 제어**: `selectLoadMorePlatforms` 가 `has_more && lastReqPlatforms` 모두 만족 플랫폼만; 없으면 more-btn 숨김.
> 6. **인증 프롬프트 분리(감사 #8)**: 검색 화면에서 QR/로그인 UI 전부 제거 → `/settings` 로 이동. `app.js` 는 검색 전용.
> 7. **data-idx escape 회피**: 카드 조회를 `data-idx`(숫자 인덱스)로 해 key escape 불필요.
> 8. **상세 필터(감사 #7)**: `per_platform_limit`·`video_only` 입력 추가 + `parseFilters` 반영.
> 9. **원본 보기 + 재생실패 대체(M1)**: 모달에 항상 `원본 보기`(post_url) 버튼; `<video>` error/play-reject 시 post_url 새 창으로 대체 이동.
> 10. **ingestItems 는 data.items append**: 서버 머지 결과(탑레벨 `items`)를 그대로 append, `sides` 는 status/cursor 용도만.

### 13-D: 설정 영역 분리 (QR 로그인) + /settings 라우트

- [ ] **Step D1: 재작성** — `web/settings.html`(QR 로그인 전용, 검색과 분리):

```html
<!doctype html>
<html lang="ko">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>설정 — 플랫폼 로그인</title>
  <link rel="stylesheet" href="/static/style.css">
</head>
<body>
  <header class="header">
    <h1>설정</h1>
    <p class="subtitle">플랫폼 계정은 로컬에서만 사용됩니다. <a href="/">← 검색으로</a></p>
  </header>
  <main id="settings-app">
    <section class="settings-card">
      <h2>샤오홍슈(XHS) 로그인</h2>
      <p class="muted">검색 화면에는 인증 프롬프트가 표시되지 않습니다. XHS 영상 강화를 원할 때만 이곳에서 로그인하세요.</p>
      <button id="login-btn" type="button">QR 코드로 로그인</button>
      <img id="qrcode-img" alt="로그인 QR" hidden>
      <p id="login-status"></p>
    </section>
  </main>
  <script type="module" src="/static/settings.js"></script>
</body>
</html>
```

- [ ] **Step D2: 신규** — `web/settings.js`(기존 app.js 의 QR 로그인 로직 이전):

```js
// web/settings.js — 플랫폼 로그인(로컬 설정 영역). 검색 화면과 분리.
const $ = (id) => document.getElementById(id);

const api = {
  async loginStatus() { const r = await fetch("/api/v1/login/status"); return r.json(); },
  async loginQrcode() { const r = await fetch("/api/v1/login/qrcode"); return r.json(); },
};

function setStatus(msg) { $("login-status").textContent = msg; }

async function startLogin() {
  setStatus("QR 코드를 가져오는 중…");
  try {
    const res = await api.loginQrcode();
    if (!res.success) { setStatus("오류: " + (res.message || "")); return; }
    const data = res.data || {};
    if (data.is_logged_in) { setStatus("이미 로그인되어 있습니다."); return; }
    let src = data.img || "";
    if (src && !src.startsWith("data:")) src = "data:image/png;base64," + src;
    $("qrcode-img").src = src;
    $("qrcode-img").hidden = false;
    setStatus("샤오홍슈 앱으로 QR 을 스캔하세요.");
    pollLoginStatus();
  } catch (e) {
    setStatus("서버에 연결할 수 없습니다. 잠시 후 다시 시도해주세요.");
  }
}

let pollTimer = null;
async function pollLoginStatus() {
  if (pollTimer) clearTimeout(pollTimer);
  try {
    const res = await api.loginStatus();
    if (res.data && res.data.is_logged_in) {
      setStatus(res.data.username ? `로그인됨: ${res.data.username}` : "로그인되었습니다.");
      $("qrcode-img").hidden = true;
      return;
    }
  } catch (e) { /* 재시도 */ }
  pollTimer = setTimeout(pollLoginStatus, 2000);
}

async function init() {
  try {
    const res = await api.loginStatus();
    if (res.data && res.data.is_logged_in) {
      setStatus((res.data.username ? `로그인됨: ${res.data.username}` : "이미 로그인됨.") + " (검색에 자동 적용)");
    }
  } catch (e) { /* 무시 */ }
  $("login-btn").addEventListener("click", startLogin);
}

document.addEventListener("DOMContentLoaded", init);
```

- [ ] **Step D3: 라우트 추가** — `routes.go` 의 정적 서빙 근처에 1줄 추가(기존 `router.GET("/", ...)` 옆):

```go
// 로컬 설정 페이지(플랫폼 로그인). 검색 화면과 분리.
router.GET("/settings", func(c *gin.Context) {
    c.File("./web/settings.html")
})
```

> 기존 `router.Static("/static","./web")` 가 `settings.js` 도 서빙. `/settings` 만 별도 라우트.

- [ ] **Step C2: 문법/테스트 확인** — `node --check web/app.js && node --check web/settings.js && node --test web/lib.test.mjs` → PASS.
- [ ] **Step C3: 정적 라우트 테스트** — `go test ./ -run TestStaticRoutes -v` → PASS(`app.js`/`lib.js`/`settings.js` 정적 서빙 + `/settings` 라우트).
- [ ] **Step C4: 커밋**

```bash
git add web/lib.js web/lib.test.mjs web/package.json web/index.html web/app.js web/settings.html web/settings.js routes.go
git commit -m "feat(web): unified search UI (no auth prompt), split QR login to /settings, add per_platform_limit/video_only and original-link fallback"
```

---

## Task 14: 스타일 + 최종 회귀 + 통합 체크리스트

**Files:** Modify `web/style.css`(신규 컴포넌트 스타일 추가). 전체 회귀.

- [ ] **Step 1: 스타일 추가** — `web/style.css` 에 기존 클래스(.header/.grid/.card/.modal/.spinner/.hidden)는 유지하고 아래 추가:

```css
/* === 통합 검색 추가 스타일 === */
.settings-link { display:inline-block; margin-top:6px; font-size:13px; color:#4a6cf7; text-decoration:none; }
.settings-card { border:1px solid var(--border,#e5e5e5); border-radius:10px; padding:18px 20px; max-width:560px; }
.settings-card h2 { margin:0 0 6px; font-size:16px; }
.muted { color:#777; font-size:13px; margin:0 0 12px; }
.modal-actions { display:flex; gap:10px; align-items:center; margin-top:10px; }
.original-link { font-size:14px; color:#4a6cf7; text-decoration:none; }
.search-section form { display:flex; flex-direction:column; gap:12px; }
.chips { border:1px solid var(--border,#e5e5e5); border-radius:8px; padding:10px 12px; }
.chips legend { font-size:13px; color:#666; padding:0 4px; }
.chips label { margin-right:16px; cursor:pointer; }
.chips button[aria-pressed="true"] { background:#4a6cf7; color:#fff; }
.side-pending { color:#9a6700; }
.sort { display:flex; align-items:center; gap:8px; font-size:14px; }
.sort select { padding:6px 8px; border-radius:6px; border:1px solid var(--border,#ccc); }
.filters summary { cursor:pointer; font-size:14px; color:#444; padding:6px 0; }
.filter-grid { display:grid; grid-template-columns:repeat(auto-fit,minmax(180px,1fr)); gap:10px; margin-top:10px; }
.filter-grid label { display:flex; flex-direction:column; font-size:12px; gap:4px; color:#555; }
.filter-grid input { padding:6px 8px; border:1px solid var(--border,#ccc); border-radius:6px; }
.filter-grid input:disabled { background:#f3f3f3; color:#aaa; }
.filter-grid label.check { flex-direction:row; align-items:center; gap:6px; }
#search-btn { align-self:flex-start; padding:10px 24px; font-size:15px; }
.side-status { list-style:none; padding:0; margin:8px 0; display:flex; gap:16px; flex-wrap:wrap; font-size:13px; }
.side-ok b { color:#1a7f37; }
.side-unavailable b { color:#b35900; }
.badge-platform { position:absolute; top:8px; left:8px; background:rgba(0,0,0,.65); color:#fff; font-size:11px; padding:2px 6px; border-radius:4px; }
#more-btn { display:block; margin:16px auto; padding:8px 28px; }
```

- [ ] **Step 2: 전체 회귀(Go)**

```bash
gofmt -w $(find . -name '*.go' -not -path './vendor/*')
go vet ./...
go build ./...
go test ./...
```
Expected: 모두 PASS.

- [ ] **Step 3: 사이드카 회귀(Python)**

```bash
cd tiktok-sidecar
pytest -v
cd ..
```
Expected: healthz/douyin/tiktok 테스트 PASS.

- [ ] **Step 4: 프론트 회귀(node)**

```bash
node --check web/app.js
node --check web/settings.js
node --check web/lib.js
node --test web/lib.test.mjs
```
Expected: PASS.

- [ ] **Step 5: production TODO 0건 검증**

```bash
# 실제 production 코드에 TODO/빈 성공 응답/placeholder 가 없는지(테스트/문서 제외).
grep -rn "TODO\|FIXME\|return \[\]\|return None" \
  tiktok-sidecar/douyin.py tiktok-sidecar/tiktok.py tiktok-sidecar/sign.py tiktok-sidecar/app.py \
  adapter_xhs.go adapter_douyin.go adapter_tiktok.go sidecar_client.go \
  handlers_search.go capabilities.go search/*.go \
  web/lib.js web/app.js web/settings.js 2>/dev/null || echo "production TODO 0건"
```
Expected: 빈 결과(또는 의미 있는 `return None` 헬퍼만 — 매핑 함수의 `_to_int` 빈값 처리는 정상). 구현자는 각 hit 이 실제 로직인지 확인.

- [ ] **Step 6: 통합 체크리스트(수동)** — Go + 사이드카 기동 후:
  - [ ] `curl -s http://127.0.0.1:18060/api/v1/search/capabilities | jq` → 3 플랫폼 메타.
  - [ ] 사이드카 미기동 시: XHS(로그인 시) 정상, Douyin/TikTok 사이드 `unavailable` 격리(전체 UI 유지).
  - [ ] `DOUYIN_COOKIE` 설정 + 사이드카 기동: Douyin 결과 + 커서(더보기) 동작.
  - [ ] `TT_MSTOKEN` 설정: TikTok 단일 페이지 결과(더보기 숨김).
  - [ ] **페이지네이션 통일**: Douyin 만 opaque cursor(더보기); XHS/TikTok 은 단일 페이지(더보기 숨김).
  - [ ] 검색 화면(`/`)에 인증/QR 프롬프트 없음; `/settings` 에서만 QR 로그인 노출.
  - [ ] 재생 실패/URL 없음 시 `원본 보기`(post_url) 새 창으로 대체; 모달에 항상 원본 링크.
  - [ ] XHS 결과 카드 클릭 → detail 호출 → 영상 재생.
  - [ ] 응답 JSON 에 cookie/ms_token/query 평문 미포함 확인.
  - [ ] 부분 실패: 한 플랫폼 차단 시 다른 플랫폼 결과는 살아남.

- [ ] **Step 7: 최종 커밋**

```bash
git add web/style.css
git commit -m "style(web): add unified search component styles"
```

---

## M1 수용 기준 (Acceptance)

1. **production TODO 0건**: Task 14 Step 5 grep 이 빈 결과(실제 로직만).
2. **실제 Go 시그니처 일치**: `SearchFeeds(...) (*FeedsListResponse, error)`와 `CheckLoginStatus(...) (*LoginStatusResponse,error)`를 `XhsService`/fake/실서비스가 동일하게 구현하고, 어댑터가 `resp.Feeds`를 읽는다(Task 9).
3. **프론트엔드 흐름 정확**: 입력값 선읽기·bare-id `$()`·커서 리셋/클리어·더보기 플랫폼 게이팅·기본 XHS+Douyin·전체 토글·off 즉시 숨김·on pending 표시·data-idx 조회·플랫폼별 capability 비활성화(node:test 로 자동 검증).
4. **의존성 버전**: `TikTokApi==7.3.3`(6.5.0 아님), `abogus.py` vendored, FastAPI/httpx/playwright 고정.
5. **sanitize/가용성**: `SideResult.Error` 는 고정 안전 메시지(`SideErrorMessage`); `Available()` 는 `/healthz` bool(Go 가 cookie/ms_token env 미참조); 비밀 평문 응답/로그 미노출.
6. **부분 실패 격리**: 한 플랫폼 지연/실패 시 다른 플랫폼 결과 유지(aggregator goroutine + 타임아웃).
7. **회귀 green**: `go test ./...` · `pytest` · `node --test` · `node --check` · `go build` 전부 PASS.
8. **페이지네이션 통일(M1)**: XHS/TikTok 단일 페이지(`has_more=false`, `next_cursor=""`), Douyin 만 opaque cursor. 더보기는 `has_more && lastReqPlatforms` 게이팅(node:test `selectLoadMorePlatforms` 검증).
9. **앱 사용자 인증 없음 + QR 분리**: 검색 UI 인증 프롬프트 없음; QR 로그인은 `/settings` 로컬 영역만. `web/package.json`(`{"type":"module"}`) 선언으로 `node --test` 가 `./lib.js` ESM import 해석.
10. **원본 보기(M1)**: 모달에 항상 `원본 보기`(post_url); 재생 실패·URL 없음 시 post_url 새 창 대체. detail 로 해결한 URL은 `currentVideoURL` 하나를 모달·복사에서 공유. 모든 플랫폼 PostURL 채움(XHS explore URL 포함).

---

## Self-Review

**1. Spec coverage** — 스펙 요구사항별 매핑:
- 통합 키워드 검색(XHS+Douyin+TikTok): Task 5/9/7/8/10.
- 정렬(relevance/popularity/latest): Task 4(rankMerge) + Task 9/12 native 매핑 + Task 13 UI.
- 상세필터(포함/제외/날짜/duration/지표): Task 3(post filter) + Task 13 UI + capability 비활성화.
- 미리보기/원본: Task 13 카드 썸네일 + 모달(XHS detail/Douyin·TikTok direct).
- 부분 실패 격리: Task 5(aggregator goroutine + SideResult).
- 비밀 비노출: Task 5(sentinel/sanitize) + Task 6(/healthz) + Task 11(sidecar).
- capability 메타데이터: Task 10 + Task 13.

**2. Placeholder scan** — module path 는 실제 go.mod 값(`github.com/xpzouying/xiaohongshu-mcp`)으로 고정. 외부 API 는 모두 사전 검증 완료: TikTokApi 7.3.3 `create_sessions(ms_tokens=[...], num_sessions=1)` + `close_sessions()`(stop_playwright 금지); Evil0ctal abogus(commit 42784ff verbatim) `ABogus().get_value(params)` — `Signer.sign` 가 이 확정 진입점을 그대로 사용(impl 시 조정 항목 없음, 본 로직은 stub 테스트로 검증). 그 외 TODO/FIXME/빈 성공 응답/placeholder 없음(Task 14 Step 5 grep 로 검증).

**3. Type consistency** —
- `VideoAdapter`/`AdapterSearchPage`/`SideResult`/`AggregatedResult`: Task 1 정의, Task 5/7/8/9/10 일관 사용.
- sentinel `ErrUnavailable/ErrBadGateway/ErrUnreachable`: Task 5(search) 정의, Task 6(sidecar) 매핑, Task 7/8/9 전파.
- `sidecarVideo`/`sidecarSearchResp`/`sidecarHealth`: Task 6 정의, Task 7/8 사용.
- `XhsService`: `SearchFeeds (...) (*FeedsListResponse, error)` + `CheckLoginStatus (...) (*LoginStatusResponse,error)`를 Task 9 인터페이스/fake/실서비스가 일치시킨다.
- Python `SearchRequest.filters` ↔ Go `sidecarReqFilters` ↔ `search.SearchFilters`: Task 6/11/12 필드명 일치(date_from/date_to/duration_min/.../min_views).
- 프론트 `lib.js` 함수명 ↔ `app.js` import ↔ `lib.test.mjs`: Task 13 일치.

---

## Execution Handoff — 승인됨

**실행 방식: 1번 Subagent-Driven.** Task 0부터 resolver 순서대로 진행한다. 각 태스크는 fresh implementer → spec reviewer → code-quality reviewer → 필요한 수정 → 해당 태스크 검증 green 순으로 닫고 다음 태스크로 이동한다. 계획 재작성 루프를 다시 열지 말고, 구현 중 발견한 국소 문제는 해당 태스크 범위에서 테스트와 함께 해결한다. 제품 범위나 계약을 바꿔야 하는 blocker 만 사용자에게 보고한다.

> 사용자 승인 후 옵션 1(Subagent-Driven) 진행 시 `superpowers:subagent-driven-development` 스킬 사용.
