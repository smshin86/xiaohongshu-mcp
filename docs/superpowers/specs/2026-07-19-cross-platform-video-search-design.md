# 참고영상 탐색·수집 도구 (XHS / Douyin / TikTok 통합) 설계

> **For agentic workers:** 본 설계는 `superpowers:writing-plans` → `superpowers:subagent-driven-development` 경로로, 마일스톤(M1→M2→M3) 순으로 구현한다. 최종 목표/인터페이스는 전체를 본 문서에 정의하고 각 마일스톤의 acceptance criteria 를 준수한다. 모든 계약(필드명·경로·포트·상태코드·타임아웃·env)은 그대로 사용한다.

> **Design reference:** 본 도구의 제품 비전(참고영상 기반 상품 키워드 발굴/수집)의 영감 출처 — https://www.youtube.com/watch?v=92P3_d6yXlM (참고 장면 6:39–7:07, 8:04–8:14).

## Goal (목표)

개인 로컬 **참고영상 탐색·수집 도구**. 상품 키워드 발굴(직접 입력 또는 참고 영상 URL 분석) → 중국어 검색어화 → **XHS/Douyin 중심 + 선택 TikTok** 통합 검색 → 성과 좋은 영상 선별 → 미리보기·원본 열기·(스트리밍) 다운로드 지원. 서버 영구 저장 없이 요청 시에만 결과를 메모리·화면에서 일시 사용.

> 플랫폼 3종(XHS·Douyin·TikTok)을 통합하고, 결과는 **반응형 통합 카드 그리드**로 제공하며, 진입은 **직접 키워드 + 참고 URL 분석** 두 가지입니다.

## Scope (범위)

**포함:**
- 진입 2종: (1) 키워드 직접 입력(한국어/중국어; 한국어면 중국어 후보 칩 생성·선택/수정), (2) 참고 영상 URL 분석(YouTube/TikTok/Instagram, 최대 3개).
- XHS + Douyin 기본 검색, TikTok 은 플랫폼 토글로 추가.
- 반응형 **통합 카드 그리드** + 상단 플랫폼 필터(전체/XHS/Douyin/TikTok; 기본 활성 XHS+Douyin).
- 상세 검색 조건(정렬 관련도/인기/최신, 포함/제외 키워드, 게시기간, 영상길이, 최소 지표, 플랫폼별 결과 수, 영상만).
- 카드 동작: 미리재생, 원본 보기, URL 복사, 참고 영상 선택(세션 내), 다운로드(스트리밍, 비영구).
- 플랫폼 미지원 지표는 항상 null(키 존재·값 null — **필드 생략 아님**, JSON 스키마 균일). 통합 인기순은 raw 조회수 단순 비교 금지(rank 기반 머지).

**명시적 제외(금지):**
- 검색 결과·영상 파일의 **서버/DB/디스크 영구 저장 금지**. 요청 중 임시 버퍼/스트림만 허용.
- 영구 수집/히스토리/북마크 DB, **다중 사용자·결제·상업 플랫폼 기능** 없음.
- 참고 영상 **프레임 비전 분석**(이미지 프레임 AI 인식)은 **후속 범위**로 명시 분리(MVP 는 텍스트 메타데이터 기반).
- Yinziai 등 제3자 paste/analyze UI 에 대한 **자동 제출/스크래핑 금지**. 수동 fallback(copy+새 탭 열기)만 허용.
- 공개 데모 API 의존 금지(쿠키/서명/위험제어 필요 플랫폼은 로컬 사이드카).
- 에러 로그에 검색어/결과 평문 기록 금지(인증용 쿠키·ms_token 설정 파일은 사용자 로컬 보관 예외).

**개인용 안내:** 다운로드/참고는 사용자가 권한을 가진 콘텐츠의 개인 참고 용도이며, 저작물 재사용 권리를 부여하지 않음을 UI/응답에 명시.

**별도 작업(본 설계 밖):** Docker/배포 자동화. XHS 는 기존 QR 로그인·`cookies.json` 복원·`CheckLoginStatus` 를 M1 에서 그대로 연결하며, 로그인 상태 확인 실패를 가용으로 추정하지 않는다.

## Architecture (아키텍처)

파이프라인: **Discovery → Search(선택 adapter 동시 fan-out, 최대 3) → Normalize/Filter/Dedupe → Preview/Acquire**.

2-프로세스 토폴로지. 언어 경계 = HTTP.

```
브라우저 (단일 진입바: 키워드 | URL분석  + 플랫폼 필터 + 정렬/상세필터 + 통합 카드 그리드)
        │ same-origin HTTP
        ▼
Go 서버(xiaohongshu-mcp 확장) 127.0.0.1:18060
  / , /static/*                        (통합 웹 UI)
  POST /api/v1/search                  (통합 검색: fan-out·normalize·filter·dedupe·merge)
  GET  /api/v1/search/capabilities     (플랫폼별 필터/지표/정렬 capability 메타데이터)
  POST /api/v1/keywords/extract        (URL→키워드 후보; 사이드카 위임)
  POST /api/v1/keywords/translate      (한국어→중국어 후보; 사이드카 위임)
  GET  /api/v1/download                (스트리밍 다운로드 프록시)
  (기존) /api/v1/feeds/detail, /api/v1/login/*
  AggregatorService
    ├─ XhsAdapter     (Go, go-rod, cookies.json)
    ├─ DouyinAdapter  (HTTP→사이드카)
    └─ TikTokAdapter  (HTTP→사이드카) ──┐
                                       ▼
Python FastAPI 사이드카 127.0.0.1:18061  (Douyin + TikTok 동일 사이드카; 외부 공개 금지, Go 전용)
  POST /search {platform,q,count,sort,filters}  (Douyin/TikTok 검색; 중첩 filters 포함)
  POST /keywords/extract   (YouTube/TikTok/IG 메타데이터 기반 키워드 후보)
  POST /keywords/translate (LLM/번역 provider, 환경변수 설정)
  GET  /download?platform=&url=   (Douyin/TikTok 스트리밍 다운로드 프록시)
  GET  /healthz            {douyin:bool, tiktok:bool(ms_token), llm:bool}
```

**핵심 특성:**
- **선택 adapter 동시 fan-out(최대 3) + 플랫폼별 격리**: 상단 플랫폼 칩이 곧 검색 요청의 `platforms` — **선택된 어댑터만 동시 fan-out**. 한쪽 실패/미가용은 `SideResult` 단위로 격리, 다른 플랫폼 결과는 정상 반환(전체 실패 금지).
- **Douyin/TikTok → 동일 Python 사이드카**: 둘 다 Python 네이티브(TikTok-Api, Douyin/Evil0ctal 참고구현체)라 사이드카 1개로 통일. playwright·쿠키·서명(X-Bogus/A-Bogus)·다운로드 프록시를 로컬에서 통합 관리.
- **XHS → Go(go-rod)**: 기존 QR 로그인과 `XiaohongshuService.CheckLoginStatus` 를 재사용한다. 로그인 완료 시 검색 가능, 미로그인·probe 오류 시 해당 side 만 `available:false`.
- **Discovery(키워드 추출/번역)**: 사이드카가 위임 처리(메타데이터 수집·LLM 호출). Go 는 API 게이트웨이 역할.
- **다운로드**: 백엔드 스트리밍 응답/리다이렉트. 디스크 미기록.

## Tech Stack

- Go 서버: gin, go-rod(XHS), `golang.org/x/sync/errgroup` 변형(에러 흡수).
- Python 사이드카: FastAPI, `davidteather/TikTok-Api`(TikTok 검색), Douyin 웹 검색(로컬 쿠키+서명; `Evil0ctal/Douyin_TikTok_Download_API` 는 다운로드/파싱 참고구현체로 self-host 반영), playwright(`python -m playwright install`), 메타데이터 수집(yt-dlp 계열), LLM/번역 provider(설정형).
- 프론트엔드: vanilla HTML/CSS/JS(빌드 없음, Go embed same-origin 서빙).

## Global Constraints (전역 제약)

- 포트/bind: Go `127.0.0.1:18060`(개인 로컬 도구, LAN 미공개); Python 사이드카 **`127.0.0.1:18061`(외부 공개 금지, Go 전용 호출)**.
- 환경변수:
  - `TT_MSTOKEN`(TikTok ms_token), `DOUYIN_COOKIE`(Douyin 쿠키), `SIDECAR_URL`(기본 `http://127.0.0.1:18061` — Go 만 호출).
  - `LLM_API_KEY`/`LLM_BASE`(키워드 생성·번역 provider, M3). `XHS` 는 기존 `cookies.json`.
- 정렬 키: `"relevance"` | `"popularity"` | `"latest"`(그 외 → 400, 폴백 없음).
- M1 은 영상 검색 전용이므로 `filters.video_only` 를 서버에서 항상 `true` 로 정규화하고 UI 는 체크 고정으로 표시한다.
- XHS 검색 필터: `note_type="视频"`(영상만의 기본 동작에 활용).
- 타임아웃: 어댑터별 XHS 60s / Douyin 45s / TikTok 45s; AggregatorService 상위 75s; 다운로드 스트림 300s(클라이언트 취소 지원). 한쪽 타임아웃 시 다른 쪽 반환.
- UI 언어: 한국어 chrome. 결과 제목·작성자·문구는 **원문(번역 X)**.
- 비영속: 검색 응답·다운로드는 메모리/스트림에서만 처리. Go/Python 어디도 결과·영상 파일을 디스크/DB에 쓰지 않음.
- **앱 사용자 인증 없음**: 이 로컬 앱 자체의 회원가입/로그인은 없고, 검색 시 앱 인증을 요구하지 않는다. 단, 플랫폼 세션까지 완전 무인증이거나 모든 콘텐츠 접근이 보장되는 것은 아니다. 로컬 운영자가 **최초 1회** XHS cookies/QR · Douyin cookie · TikTok ms_token 을 준비하면 백엔드가 재사용하고, **만료 시에만 설정 영역**에서 갱신한다. 검색 UI 는 인증 화면에 막히지 않고 준비된 플랫폼은 즉시 검색, 미준비 플랫폼은 상태만 표시한다. 자격증명은 **로컬 전용**(로그·응답·repo commit 비노출).
- **플랫폼 세션 경계**: M1 은 익명 visitor session 자동 bootstrap 을 약속하지 않는다. 로컬 운영자가 XHS QR/cookies, Douyin cookie, TikTok `ms_token` 을 준비·갱신한다. 앱 사용자는 별도 계정이나 검색별 인증 없이 준비된 플랫폼을 사용하고, CAPTCHA/login wall 우회는 시도하지 않는다.

## Components & Interfaces

### 통합 DTO `VideoItem` (Go + 프론트 JSON 계약)

지표는 pointer(`*int64`) + `omitempty` **미사용** → 미지원 시 **항상 `null` 키로 응답(필드 생략 아님)**. JSON 스키마를 플랫폼 무관하게 균일하게 유지(문자열·정수 필드는 빈 값일 때만 생략 — 별개 의미). raw 강제 채움 금지.

```go
type VideoItem struct {
    Platform      string  `json:"platform"`                  // "xiaohongshu"|"douyin"|"tiktok"
    PostID        string  `json:"post_id"`
    PostURL       string  `json:"post_url"`
    VideoURL      string  `json:"video_url,omitempty"`       // 있으면 미리재생, 비어있으면 원본 보기/다운로드 경로로 해결
    ThumbnailURL  string  `json:"thumbnail_url"`
    Title         string  `json:"title"`                     // 원문
    Description   string  `json:"description,omitempty"`     // 원문
    Author        string  `json:"author"`
    PublishedAt   string  `json:"published_at,omitempty"`    // RFC3339 (가능 시)
    Duration      int     `json:"duration,omitempty"`        // 초 단위, 0=미상
    Likes         *int64  `json:"likes"`                     // 항상 키 존재; null=미지원
    Comments      *int64  `json:"comments"`                  // "
    Favorites     *int64  `json:"favorites"`                 // saves/collected; "
    Views         *int64  `json:"views"`                     // XHS=null(미지원)
    Shares        *int64  `json:"shares"`                    // "
    SourceKeyword string  `json:"source_keyword"`            // 이 결과를 낸 최종 중국어 검색어
    NeedsDetail   bool    `json:"needs_detail"`              // true=XHS(클릭 시 detail), false=Douyin/TikTok(즉시)
    DetailToken   string  `json:"detail_token,omitempty"`    // XHS xsec_token
    DedupeKey     string  `json:"-"`                         // platform:post_url 우선, 없으면 platform:post_id
    RankScore     float64 `json:"-"`                         // 머지용 1/(rank+k)
}
```

### Go 어댑터 인터페이스

```go
type VideoAdapter interface {
    Name() string                                       // "xiaohongshu"|"douyin"|"tiktok"
    Search(ctx context.Context, q SearchQuery) (AdapterSearchPage, error)
    Available(ctx context.Context) Availability         // 준비 상태 + 미가용 사유 메시지
}
// AdapterSearchPage — 어댑터가 반환하는 단일 페이지 결과 + 페이지 메타데이터.
// 미지원/마지막 페이지는 NextCursor="" + HasMore=false.
type AdapterSearchPage struct {
    Items      []VideoItem
    NextCursor string // opaque(플랫폼별); 빈 값=다음 페이지 없음
    HasMore    bool
}
type SearchQuery struct {
    Keyword    string            // 최종 중국어 검색어
    Sort       string            // relevance|popularity|latest
    Filters    SearchFilters
    Limit      int               // per-platform(= per_platform_limit)
    PageCursor string            // opaque(플랫폼별); 빈 값=첫 페이지. 외부 Go 계약은 항상 opaque cursor(플랫폼 내부 offset/page 와 무관)
}
```

- **`XhsAdapter`**(Go): 기존 `XiaohongshuService` 래핑. `Views=nil`, `NeedsDetail=true`. video_url/post_url 은 `/api/v1/feeds/detail` 로 해결. `Search` 는 `AdapterSearchPage` 반환(XHS 검색의 cursor/has_more 매핑).
- **`DouyinAdapter`**(HTTP→사이드카): Douyin 웹 검색 결과 매핑. `Views`/`Shares` 가능 시 채움. 사이드카 응답의 `next_cursor`/`has_more` → `AdapterSearchPage.NextCursor`/`HasMore` 매핑.
- **`TikTokAdapter`**(HTTP→사이드카): TikTok-Api 검색 결과 매핑. `playCount`→`Views`, `diggCount`→`Likes`. `VideoURL`/`PostURL` 시도 채움, 미획득 시 빈 값(재생 실패→원본 보기/다운로드 흐름이 자동 대체). 동일하게 사이드카 `next_cursor`/`has_more` → `AdapterSearchPage` 매핑.

### Python 사이드카 HTTP 계약 (`tiktok-sidecar/`)

- `GET /healthz` → `{"status":"ok","douyin":bool,"tiktok":bool,"llm":bool}`
- `POST /search` body (native 필터 전달을 위해 GET 쿼리가 아닌 중첩 JSON; capability 표와 일치):
  ```json
  {"platform":"douyin|tiktok","q":"便携风扇","count":20,
   "sort":"relevance|popularity|latest","cursor":"",
   "filters":{"include_keywords":[],"exclude_keywords":[],
     "date_from":"","date_to":"","duration_min":0,"duration_max":0,
     "min_likes":0,"min_comments":0,"min_favorites":0,"min_views":0,
     "video_only":true}}
  ```
  → `{"success":true,"data":{"videos":[ {VideoItem 호환 JSON} ],"next_cursor":"","has_more":false}}`.
  `cursor`/`next_cursor` 는 **opaque**(빈 값=첫 페이지/다음 없음); 플랫폼 내부 offset/page 를 감춘다. native 적용 가능 필터만 사용; post 필터는 Go Aggregator 가 응답 후 처리(capability 표 참조). 에러: 쿠키/ms_token 없음·만료 → `503 {"success":false,"message":"..."}`; 검색 실패/안티봇 → `502`.
- `POST /keywords/extract` body `{"urls":["youtube...","tiktok...","instagram..."]}`(≤3) →
  ```json
  {"success":true,"data":{"candidates":[
    {"keyword":"","source_url":"","basis":"title|hashtag|description|metadata","confidence":0.0}
  ],"note":"텍스트 메타데이터 기반(프레임 비전 분석 제외)"}}
  ```
  접근 실패 시 `{"success":false,...}` → 프론트가 직접 키워드 입력으로 복구.
- `POST /keywords/translate` body `{"text":"손 선풍기","source_lang":"ko"}` → `{"success":true,"data":{"candidates":[{"zh":"便携风扇"}]}}`.
- `GET /download?platform=douyin|tiktok&url=<enc>` → 스트리밍(`video/mp4`, `Content-Disposition: attachment; filename=...`). 403/502/504 명시.

### Go `AggregatorService`

```go
type Availability struct {
    Available bool   `json:"available"`
    Reason    string `json:"reason,omitempty"` // 미가용 사유(로그인 필요/ms_token 만료 등)
}
type SideResult struct {
    Items      []VideoItem  `json:"items"`
    Error      string       `json:"error,omitempty"`
    Available  Availability `json:"available"`
    NextCursor string       `json:"next_cursor,omitempty"` // opaque(플랫폼별); 빈 값=다음 페이지 없음
    HasMore    bool         `json:"has_more"`               // 항상 키 존재
}
type AggregatedResult struct {
    KeywordUsed string                `json:"keyword_used"`  // 최종 적용된 중국어 검색어
    Sort        string                `json:"sort"`
    Items       []VideoItem           `json:"items"`         // 머지·중복제거·정렬된 통합 리스트
    Sides       map[string]SideResult `json:"sides"`         // xiaohongshu/douyin/tiktok별 상태
}
```

- 어댑터들 주입(`[]VideoAdapter`). 요청의 `platforms` 에 해당하는 어댑터만 **동시 fan-out**(각 어댑터에 `page_cursors[platform]` → `SearchQuery.PageCursor` 전달) → 어댑터가 **`AdapterSearchPage`(`Items`+`NextCursor`+`HasMore`) 반환** → 각 `Items` 정규화 → 필터 적용 → **중복 제거(DedupeKey)** → **rank 머지 정렬** → `Items` + `Sides`(`AdapterSearchPage.NextCursor`/`HasMore` → `SideResult.NextCursor`/`HasMore` 매핑) 구성. 에러/미가용은 `Sides` 로 흡수.

### HTTP 엔드포인트 (Go)

- `GET /api/v1/search/capabilities` → `{platforms:{xiaohongshu:{...},douyin:{...},tiktok:{...}},merge_rule:"per_platform_rank"}`. 각 플랫폼 값은 `{sorts,metrics,native_filters,post_filters,pagination,available,unsupported_note}`이며 pagination 은 XHS/TikTok=`"single_page"`, Douyin=`"opaque_cursor"`.
- `POST /api/v1/search` body (`keyword` 는 항상 **사용자가 최종 확정한 중국어 검색어**; 한국어 입력은 클라이언트가 먼저 `/keywords/translate` 로 후보 칩을 받아 사용자가 선택/수정한 뒤 이 엔드포인트로 전달 — 서버 자동번역 없음):
  ```json
  {"keyword":"便携风扇",
   "platforms":["xiaohongshu","douyin"],
   "sort":"popularity",
   "page_cursors":{"xiaohongshu":"","douyin":"","tiktok":""},
   "filters":{"include_keywords":[],"exclude_keywords":[],
     "date_from":"","date_to":"","duration_min":0,"duration_max":0,
     "min_likes":0,"min_comments":0,"min_favorites":0,"min_views":0,
     "video_only":true,"per_platform_limit":20}}
  ```
  → `{"success":true,"data":AggregatedResult}`(각 `SideResult` 에 `next_cursor`/`has_more` 포함). 검증: `keyword` 공백→400; `sort`/`platforms` 불가→400. `page_cursors` 값은 **opaque**(빈 문자열=첫 페이지); 플랫폼 내부가 offset/page 를 쓰더라도 **외부 Go 계약은 항상 opaque cursor**. (한국어→중국어 후보 선택 흐름은 `/keywords/translate` 가 담당; `/search` 는 중국어 최종어만 받음.)
- `POST /api/v1/keywords/extract`, `POST /api/v1/keywords/translate`, `GET /api/v1/download` (상기 계약).
- 기존 `/api/v1/feeds/search`(XHS 전용), `/api/v1/feeds/detail`, `/api/v1/login/*` 유지.

### 프론트엔드 (`web/` 재설계)

- **진입바**: 키워드 입력(한국어/중국어) OR "URL 분석" 모드(≤3 URL) → 키워드 후보 칩(원문/중문) → 선택/수정 → 검색.
- **플랫폼 칩 = 다음 검색 요청의 `platforms`**: 전체/샤오홍슈/Douyin/TikTok. **"전체" = 3개 플랫폼 모두 선택**(개별 칩 3개 on 과 동등). **초기 상태 = XHS+Douyin 개별 칩 활성, TikTok off**. 검색 실행 시 **선택된 어댑터만 fan-out**. 결과 표시 중 동작: 칩 **off** → 현재 결과에서 해당 플랫폼 **로컬 숨김**(재검색 없음); 마지막 요청에 없던 플랫폼을 **on** → 해당 칩에 **"검색 실행 필요"** 상태 표시 → **검색 버튼으로 재실행**(자동 재검색 아님).
- **정렬 + 상세필터 패널**: 포함/제외, 게시기간, 영상길이, 최소 지표, 플랫폼별 수, 영상만. capability 인식 → 미지원 필터는 비활성+제한 안내.
- **반응형 통합 카드 그리드**: 카드 필드·동작(미리재생/원본 보기/URL 복사/참고 영상 선택/다운로드). 플랫폼 배지 + 같은 플랫폼 raw 지표 노출. 미지원 지표는 숨김.
- **더보기(다음 페이지)**: 플랫폼별 `has_more` 시 "더보기" → 직전 응답의 `next_cursor` 로 `page_cursors` 를 채워 `/search` 재호출 → **결과를 브라우저 탭 메모리에 append**(서버 저장 없음) → append 마다 같은 플랫폼 안에서 `platform:post_id` 또는 `platform:post_url` dedupe.
- **세션 내 "참고 영상" 트레이**(비영속, 요청/탭 세션만).
- 플랫폼별 상태(건수/에러/준비) 표시.

## Data Flow

1. **키워드 검색**: 진입바 → (한국어면 `/keywords/translate` 로 중국어 후보 칩 → 사용자 선택 → `keyword`) → `POST /api/v1/search`(body 의 `platforms` = 상단 칩 선택 상태) → Aggregator 가 **선택된 어댑터(최대 3)만 동시 fan-out** → 각 `[]VideoItem` 정규화 → 필터 → 중복제거 → rank 머지 정렬 → `Items`+`Sides` → 프론트 통합 그리드. (결과 표시 후 칩 on/off 동작은 §프론트엔드 참조.)
2. **URL 분석(Discovery)**: URL ≤3 → `/keywords/extract` → 키워드 후보 칩 → 선택 → (1) 흐름으로 합류. 추출 실패 시 직접 키워드 입력으로 복구.
3. **미리재생**: 카드 클릭 → `NeedsDetail`?(XHS: `/api/v1/feeds/detail` 로 video_url/post_url 해결 / Douyin·TikTok: video_url 즉시) → 모달 `<video>`. **재생 실패 감지 시 "원본 게시물에서 보기"(post_url 새 탭)** + 보조 버튼 상시 노출.
4. **다운로드**: 카드 "다운로드" → `GET /api/v1/download?platform=&post_id=&detail_token=&url=&filename=` → 백엔드 스트리밍. **식별은 platform+post_id(+detail_token) 우선**(SSRF 방지): XHS 는 서버가 `/feeds/detail` 로 video_url 자체 해결(임의 url 미수용); Douyin/TikTok 은 항목 식별 후 사이드카 `/download` 위임. 외부 url 이 불가피한 경우 https + 플랫폼 호스트 allowlist + DNS/redirect 재검증 + private/loopback 차단(§Security). 디스크 미기록.
5. **중복제거**: 요청 내 `DedupeKey`(`platform:post_url` 우선, 없으면 `platform:post_id`) 기준 — **플랫폼 내부 중복제거만 보장**. 서로 다른 플랫폼의 동일 영상은 URL 이 같더라도 자동 제거하지 않는다 → **크로스플랫폼 dedupe 는 후속 범위**(수동 "참고 영상 선택" 트레이로 사용자가 보조).
6. **동시성/타임아웃**: 어댑터별 ctx 타임아웃 + 상위 75s. 한쪽 타임아웃/에러 시 다른 쪽 반환.
7. **다음 페이지(더보기)**: 플랫폼별 `has_more` 시 직전 `next_cursor` 로 `page_cursors` 를 채워 `/search` 재호출 → 결과를 **브라우저 탭 메모리에 append**(서버 저장 없음) → 매 append 마다 같은 플랫폼의 `platform:post_id`/`platform:post_url` dedupe. 외부 Go 계약은 opaque cursor(플랫폼 내부 offset/page 무관).

## Search Conditions & Filtering (native vs post-processing)

**필터를 (a) 어댑터 네이티브 요청 필터 vs (b) aggregator 후처리 필터로 구분.** capability 엔드포인트가 이를 노출.

| 조건 | XHS(native/post) | Douyin | TikTok |
|---|---|---|---|
| keyword 포함/제외 | native / post | native / post | native / post |
| 정렬 relevance | native(sort_type=general) | native | native(반환 순서 그대로) |
| 정렬 popularity | native(likes desc) | native | **post**(반환 집합 내 diggCount desc 재정렬) |
| 정렬 latest | native(time_descending) | native | **post**(createTime desc 재정렬) |
| 게시기간 | native | native | post |
| 영상길이 범위 | post | native | post |
| 최소 likes | **post** | post | post |
| 최소 comments | **post** | post | post |
| 최소 favorites | post | post | post |
| 최소 views | **제외(미지원)** → UI 제한 안내 | post | post |
| 영상만(video_only) | native(note_type=视频) | native | post |
| 결과 수/다음 페이지 | native(limit + opaque next_cursor) | native(limit + cursor) | native(limit + cursor) |

> **M1 페이지네이션 범위(현실적 축소)**: 외부 Go 계약은 항상 opaque cursor 이나, **M1 구현 범위는 Douyin 만 opaque cursor 더보기를 지원**하고 XHS·TikTok 은 **단일 페이지**(`has_more=false`, `next_cursor=""`)로 출범한다. XHS 는 기존 검색 커서 매핑의 안정성이, TikTok 은 `TikTokApi 7.3.3 search.search_type` async generator 의 cursor/has_more 노출이 신뢰성 부족해 단일 페이지로 한정한다. 더보기 UI 는 `has_more && 직전 요청 플랫폼` 게이팅으로 단일 페이지 플랫폼은 자동 숨김. (이행 계약: 어댑터 `AdapterSearchPage.HasMore/NextCursor` 와 `SideResult` 매핑은 그대로 — XHS/TikTok 어댑터가 `false`/`""` 만 반환.)

- **XHS 최소 likes/comments**: 현재 구현은 응답 후처리(임계값 미만 제거)이므로 **post**. 정렬·게시기간·영상만 은 XHS 검색 API 네이티브 파라미터(sort_type·필터)로 지원.
- **TikTok 정렬**: `davidteather/TikTok-Api` 의 `Search.search_type()` 는 sort 파라미터를 노출하지 않음 → relevance(반환 순서)만 native, popularity/latest 는 반환 집합 내 통계 기반 **post** 재정렬. 크로스셋 인기 비교는 rank 머지가 보정.
- **XHS 조회수 미지원**: `min_views` 필터·`views` 정렬은 XHS 에서 제외, capability 로 UI 에 제한 명시.
- 미지원 정렬·필터는 `unsupported_note` 로 명시. **post 필터는 native 결과를 초과 fetch 하지 않으므로** 최종 건수가 줄어들 수 있음(UI 안내).

## 통합 정렬(Merge) 규칙 — 결정

**raw 조회수/좋아요 단순 비교 금지.** 각 플랫폼 어댑터가 반환한 순서를 그대로 보존하고 within-platform rank `r`(0부터) 부여 → score `= 1/(r+1)` → 동일 rank 끼리는 XHS→Douyin→TikTok 라운드로빈. 머지 단계에서 likes/time/PostID 로 다시 정렬하지 않는다. 카드는 자기 플랫폼 raw 지표 + 배지 표시. **진정한 크로스플랫폼 백분위 정규화는 후속 범위.**

## Security

- **SSRF 방지(/download)**: 임의 url 파라미터를 그대로 fetch 금지. **platform + post_id(+detail_token) 로 서버가 항목을 식별**해 video_url 을 자체 해결하는 방식 우선. 외부 url 이 불가피한 경우에만: (1) `https` 만 허용, (2) 플랫폼 호스트 allowlist(xiaohongshu.com 계열 · douyin/douyinpic/byteimg 계열 · tiktok/tiktokcdn 계열), (3) fetch 전 DNS 해석 후 private/loopback/link-local IP 차단(IP 리바인딩 방지), (4) HTTP redirect 추종 시 each hop 마다 호스트 allowlist + IP 차단 재검증(redirect-to-localhost 공격 방지). 사이드카는 **`127.0.0.1:18061` 에만 bind(외부 공개 금지, Go 전용 호출)** — 내부 신뢰 경계(위 보호는 사용자 대면 Go 경계에서 적용).
- **SSRF 방지(/keywords/extract)**: 사용자 입력 URL 은 YouTube/TikTok/Instagram 호스트 allowlist 로 제한, 동일 DNS/redirect/IP 보호 적용. 외부 메타데이터 fetch 실패 시 `success:false`(직접 키워드 입력 복구).
- **자격증명 비노출**: `TT_MSTOKEN`/`DOUYIN_COOKIE`/XHS `cookies.json` 은 서버 메모리·설정 파일에만 존재. **query string·응답 JSON·로그에 평문 노출 금지**(사이드카·Go 로그 모두). 에러 메시지에 토큰/쿠키 단편 미포함.
- **개인용 안내**: 다운로드/참고는 사용자 권한 콘텐츠의 개인 용도, 재사용 권리 미부여(§Download & Fallbacks 와 중복 명시).

## Error Handling

| 상황 | 백엔드 | 프론트 |
|---|---|---|
| XHS 미로그인/probe 실패 | `sides.xiaohongshu.available:false` | "설정에서 QR 로그인을 완료해 주세요" |
| Douyin 쿠키/서명 미설정 | `available:false`/503 | "Douyin: 쿠키/서명 미설정" |
| TikTok ms_token 만료/미설정 | 503/`available:false` | "TikTok: ms_token 갱신 필요(tiktok.com 쿠키)" |
| 사이드카 다운 | Douyin/TikTok `SideResult.Error` | 해당 플랫폼 "서비스에 연결할 수 없습니다" |
| 한 플랫폼 타임아웃 | `SideResult.Error` | 해당 "응답 시간 초과, 다시 시도" |
| 빈 결과 | `items:[]` | "검색 결과가 없습니다" |
| keyword 공백/잘못된 sort·platforms | 400 | "검색어/조건을 확인해 주세요" |
| URL 추출 실패 | `/keywords/extract` success:false | "분석 실패 — 키워드 직접 입력" |
| 안티봇/속도제한 | 502 | 해당 "일시적으로 차단 — 잠시 후 재시도" |
| 서버 전체 다운 | fetch 실패 | "서버에 연결할 수 없습니다" |

**원칙**: 한 플랫폼 실패는 전체 실패로 번지지 않음. 항상 `SideResult` 격리.

## Download & Fallbacks

- **스트리밍/리다이렉트, 비영구**: `GET /api/v1/download` → 백엔드가 원본 스트림을 그대로 전달(`Content-Type`, `Content-Disposition: attachment; filename=...`). 디스크 임시파일 미생성. 식별은 **platform+post_id/detail_token 우선**(SSRF 방지); 외부 url 은 https + 호스트 allowlist + DNS/redirect 재검증 + private/loopback 차단(§Security).
- **에러 처리**: 원본 만료→`502`, 접근 거부→`403`, 타임아웃→`504`, 클라이언트 취소 감지·정리.
- **XHS**: 현재 상세 조회의 video URL 우선 사용. 실패 시 **post URL 복사 + 원본 열기**(다운로드 미보장 시 안내).
- **Yinziai**(`yinziai.com/ko/tools/download-video-xhslink`): 공개 연동 API 미확인 제3자 paste/analyze UI → **핵심 자동화 의존 금지**. 선택적 수동 fallback 으로 **"XHS 링크 복사 + Yinziai 새 탭 열기"** 만 허용. **자동 제출/스크래핑은 범위 밖.**
- **개인용 안내**: 다운로드는 사용자 권한 콘텐츠의 개인 참고 용도, 재사용 권리 미부여 안내를 응답/UI 에 포함.

## Testing Strategy

- **Go 단위(TDD)**: 가짜 어댑터로 `AggregatorService` 검증(**`platforms` 게이팅 = 선택 어댑터만 fan-out**, 부분 실패 격리, `Available()` 게이팅, 타임아웃 격리); 정규화(pointer null 매핑); 필터(native/post 구분); **opaque cursor 라운드트립**(`page_cursors`→`SearchQuery.PageCursor`→어댑터 `AdapterSearchPage.NextCursor`/`HasMore`→`SideResult.NextCursor`/`HasMore`, 내부 offset/page 감춤; 미지원/마지막 페이지는 `NextCursor=""`+`HasMore=false`); **중복제거(DedupeKey)**; **rank 머지 정렬**(동점 라운드로빈); `XhsAdapter`/`DouyinAdapter`/`TikTokAdapter` httptest 매핑+에러(503/502); `/api/v1/search`·`/capabilities`·`/download` 핸들러 httptest(400 검증 포함); **SSRF 방어**(/download·/keywords/extract 의 호스트 allowlist·private/loopback 차단·redirect hop 재검증) 단위 테스트.
- **Python 사이드카**: `/search`(platform별 + **opaque cursor 페이징**) 매핑·쿠키/ms_token 부재→503·`/keywords/extract`·`/keywords/translate`·`/download` 스트리밍(외부 API 는 스텁).
- **프론트**: `node --check web/app.js` + 수동 브라우저 체크리스트(진입 2종·**칩=platforms(초기 XHS+Douyin/TikTok off/'전체'=3개, on=검색 실행 필요)**·상세필터·통합 그리드·**더보기(append+dedupe)**·카드 동작·미지원 지표 숨김·중복제거·다운로드·fallback).
- **실데이터/통합**: 수동 연기(실 쿠키/ms_token 필요). 단위 테스트는 외부 API 없이 통과.
- **회귀**: `go vet ./...`·`go build ./...`·기존 `TestStaticRoutes`·`TestVideo*`.

## Phased Delivery (마일스톤)

> 최종 목표·인터페이스는 본 문서 전체. 각 마일스톤 acceptance criteria 준수.

**M1 — 통합 키워드 검색 + 상세필터 + 미리보기/원본**
- Acceptance: 상단 플랫폼 칩 = 검색 `platforms`(초기 XHS+Douyin, TikTok off, '전체'=3개 선택) → **선택 어댑터만 fan-out**; 중국어 직접 키워드 통합 검색; 반응형 통합 그리드; 정렬(relevance/popularity/latest); 상세필터(native/post 구분, capability 인식, 미지원은 제외+안내); **opaque cursor 더보기 = M1 은 Douyin 만 지원(XHS·TikTok 은 단일 페이지; 더보기 UI 는 `has_more && 직전 요청 플랫폼` 게이팅으로 숨김) → 탭 메모리 append + dedupe, 서버 저장 없음**; 미리재생 + 원본 보기(재생 실패 대체); 미지원 지표 null/숨김; 세션 내 중복제거; 플랫폼별 부분실패 격리.
- 제외: 다운로드(M2), 한국어 후보/URL 분석 Discovery(M3).

**M2 — 카드별 스트리밍 다운로드 + Yinziai 수동 fallback**
- Acceptance: `/api/v1/download` 스트리밍(Content-Disposition, 403/만료/타임아웃/취소 처리); **식별은 platform+post_id/detail_token 우선 + 외부 url 시 SSRF 보호(https·allowlist·loopback 차단·redirect 재검증)**; XHS detail URL 우선 + 실패 시 post URL 복사/원본 열기; Yinziai 수동 fallback(링크 복사+새 탭, 자동 제출 금지); 개인용 안내 표시.

**M3 — 참고 URL → AI 키워드 추출 → 한/중 후보 선택 → 검색**
- Acceptance: URL ≤3(YouTube/TikTok/Instagram) → `/keywords/extract`(텍스트 메타데이터 기반, **프레임 비전 분석 제외**) → 키워드 후보 칩; 한국어 입력 → `/keywords/translate` 중국어 후보 칩 → 선택/수정 → 검색; 추출 실패 시 직접 입력 복구.

## Out of Scope (별도/후속)

- 참고 영상 **프레임 비전 분석**(이미지 프레임 AI 인식).
- 영구 수집/히스토리/북마크 DB; 다중 사용자·결제·상업 기능.
- Yinziai 자동 제출/스크래핑; 공개 데모 API 의존.
- 진정한 크로스플랫폼 백분위 정규화(MVP 는 rank 머지).
- **크로스플랫폼 동일 영상 자동 중복제거**(URL 상이 → 자동 판별 불가). MVP 는 플랫폼 내 dedupe + 수동 "참고 영상 선택" 트레이로 보조.
- Docker/배포 자동화.
