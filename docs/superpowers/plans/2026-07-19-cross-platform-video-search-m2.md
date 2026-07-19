# M2 실행 계획 — 스트리밍 다운로드와 XHS 수동 fallback

> 실행 방식: `superpowers:subagent-driven-development`. 각 Task마다 새 구현 에이전트가 TDD로 작업하고, 스펙 리뷰 → 품질 리뷰 → 수정 재검증을 통과한 뒤 다음 Task로 이동한다.

## 1. 목표와 고정 계약

M1 검색 결과 카드에 서버 비영구 스트리밍 다운로드를 추가한다. Go는 사용자 대면 보안 경계이고, Python sidecar는 Douyin/TikTok 원본 스트림을 중계한다. XHS는 Go가 기존 상세 조회로 영상 URL을 해결한다.

- Go: `GET /api/v1/download?platform=&post_id=&detail_token=&url=&filename=`
- Sidecar: `GET /download?platform=douyin|tiktok&url=<encoded>`
- 성공: `video/mp4` 계열 스트림 + `Content-Disposition: attachment`
- 오류: 잘못된 입력/SSRF `400`, 원본 접근 거부 `403`, 원본/상세 실패 `502`, 300초 초과 `504`
- 클라이언트 취소는 상위 context를 원본 요청까지 전달하고 즉시 body를 닫는다.
- 파일·검색 결과를 서버 디스크/DB에 쓰지 않는다. `os.Create`, 임시 파일, 전체 body 버퍼링 금지.
- 앱 사용자 인증은 추가하지 않는다. 플랫폼 자격증명은 기존 로컬 설정만 사용한다.
- Yinziai는 XHS 링크 복사 + 새 탭 열기만 제공한다. 자동 제출·스크래핑 금지.
- UI에 개인 참고용이며 재사용 권리를 부여하지 않는다는 안내를 표시한다.
- 실제 쿠키가 필요한 live smoke는 `EXTERNAL BLOCKED`로 분리하며 M2 단위/브라우저 수용을 막지 않는다.
- push/PR/merge 금지. 현재 로컬 브랜치 `feat/m2-download`에서만 커밋한다.

## 2. 실행 규칙

각 Task는 아래 순서를 지킨다.

1. 명시된 실패 테스트를 먼저 추가하고 실패 이유를 확인한다.
2. 수용조건을 만족하는 최소 구현만 작성한다.
3. 변경한 Go 파일만 `gofmt`한다.
4. Task별 테스트와 인접 회귀 테스트를 통과시킨다.
5. 스펙 리뷰와 품질 리뷰에서 승인받고 한 개의 집중된 커밋을 만든다.

보안상 다음 구현은 허용하지 않는다.

- 사용자 `url`을 검증 없이 `http.Get`/`httpx.get`에 전달
- `https://allowed.example.evil.test` 같은 suffix 오판
- 최초 URL만 검사하고 redirect 목적지는 검사하지 않는 구현
- DNS 검사 뒤 다른 해석 결과로 접속할 수 있는 unchecked dial
- URL query, 쿠키, `ms_token`을 애플리케이션 에러 로그에 출력
- 다운로드 응답을 `io.ReadAll`, `response.content`, Blob으로 전부 메모리에 적재
- XHS 클라이언트 제공 `url`을 상세 조회보다 우선 사용
- Yinziai 폼 자동 입력, 요청 전송, 결과 scraping

## Task 1 — Go 외부 URL·DNS·redirect 보안 경계

### 파일

- Create: `download_security.go`
- Create: `download_security_test.go`

### 구현 계약

`main` 패키지에 다운로드 전용 guard를 만든다.

```go
type downloadResolver interface {
    LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type downloadURLGuard struct {
    resolver downloadResolver
    dialer   *net.Dialer
}

func newDownloadURLGuard() *downloadURLGuard
func (g *downloadURLGuard) Validate(ctx context.Context, platform, rawURL string) (*url.URL, error)
func (g *downloadURLGuard) NewClient(platform string) *http.Client
```

구현 세부사항:

- platform은 `xiaohongshu|douyin|tiktok`만 허용한다.
- URL은 `https`만 허용하고 userinfo, 빈 host, 비정상 port를 거부한다.
- hostname 비교는 `host == suffix || strings.HasSuffix(host, "."+suffix)`만 사용한다.
- 최소 허용 family:
  - XHS: `xiaohongshu.com`, `xhscdn.com`, `xhslink.com`
  - Douyin: `douyin.com`, `douyinvod.com`, `douyinpic.com`, `byteimg.com`, `bytecdn.cn`
  - TikTok: `tiktok.com`, `tiktokcdn.com`, `tiktokv.com`, `byteoversea.com`, `ibytedtos.com`
- `LookupIPAddr` 결과가 하나라도 loopback/private/link-local/unspecified/multicast이면 거부한다. IPv4-mapped IPv6와 IPv6 ULA도 포함한다.
- `NewClient`의 Transport `DialContext`가 실제 접속 직전에 다시 DNS를 검사하고 검사한 public IP로 직접 dial한다. 검증과 접속 사이 DNS rebinding으로 private IP에 접속할 수 없어야 한다.
- `CheckRedirect`는 최대 5 hop이며 매 hop마다 scheme/host/DNS 정책을 다시 적용한다.
- client 자체에는 짧은 고정 timeout을 두지 않는다. Task 4의 300초 request context가 전체 스트림을 제어한다.
- guard 오류에는 원본 URL·query를 포함하지 않는다.

### 실패 테스트

table-driven 테스트로 다음을 고정한다.

- 정상 XHS/Douyin/TikTok CDN host 허용
- `http`, userinfo, 미지원 platform, 미허용 host 거부
- `xiaohongshu.com.evil.test`, `eviltiktok.com` 거부
- `127.0.0.1`, `10/8`, `172.16/12`, `192.168/16`, `169.254/16`, `::1`, `fc00::/7`, `fe80::/10` 거부
- DNS가 public → private로 바뀌는 fake resolver에서 dial 단계가 재검사해 거부
- redirect가 localhost/private 또는 다른 platform host로 향하면 거부
- redirect loop/6번째 hop 거부
- 오류 문자열에 raw query가 포함되지 않음

### 검증

```bash
gofmt -w download_security.go download_security_test.go
go test ./... -run 'TestDownloadURLGuard|TestDownloadRedirect' -count=1
go test -race . -run 'TestDownloadURLGuard|TestDownloadRedirect' -count=1
```

### 커밋

`feat(download): add SSRF-safe platform URL guard`

## Task 2 — Python sidecar 보안 스트리밍 `/download`

### 파일

- Create: `tiktok-sidecar/download.py`
- Modify: `tiktok-sidecar/app.py`
- Create: `tiktok-sidecar/tests/test_download.py`
- Modify: `tiktok-sidecar/README.md`

### 구현 계약

`download.py`에 sidecar 내부 방어와 upstream lifetime을 캡슐화한다.

```python
class DownloadBadRequest(Exception): ...
class DownloadForbidden(Exception): ...
class DownloadUpstreamFailed(Exception): ...
class DownloadTimedOut(Exception): ...

def validate_download_url(platform: str, raw_url: str, resolver=socket.getaddrinfo) -> str: ...
def open_download(platform: str, raw_url: str, client_factory=httpx.Client) -> "DownloadStream": ...
```

`DownloadStream`은 열린 `httpx.Client`와 `httpx.Response`를 보유하고 `iter_bytes()`와 idempotent `close()`를 제공한다. route가 `StreamingResponse`의 iterator로 `iter_bytes()`를 사용하고 `BackgroundTask(close)`로 양쪽을 닫는다.

- `platform`은 `douyin|tiktok`만 허용한다.
- Task 1과 같은 `https`, suffix, private/loopback/link-local, redirect 최대 5 hop 정책을 Python에서도 적용한다. sidecar가 redirect를 직접 따라가므로 hop 검증을 생략하면 안 된다.
- `follow_redirects=True` 단독 사용 금지. 3xx `Location`을 `urljoin`한 뒤 재검증하고 다음 요청을 수행한다.
- Douyin 요청에는 로컬 `DOUYIN_COOKIE`와 적절한 User-Agent/Referer를 header로만 적용한다. TikTok은 필요한 User-Agent/Referer만 사용하며 `TT_MSTOKEN`을 query/로그에 복사하지 않는다.
- upstream을 route 진입 시 열어 403/timeout/5xx를 응답 header 전 상태코드로 매핑한다.
- `403 → 403`, timeout → `504`, 그 외 upstream/network 실패 → `502`, 잘못된 입력/SSRF → `400`.
- 성공 응답은 upstream `Content-Type`이 `video/*`일 때 보존하고 아니면 `video/mp4`를 사용한다. `Content-Disposition`은 Task 4가 최종 filename을 설정하므로 sidecar는 안전한 기본값만 제공한다.
- `.content`, `read()`, 임시 파일을 사용하지 않고 chunk iterator를 그대로 전달한다.
- README 실행 예시는 `uvicorn app:app --host 127.0.0.1 --port 18061 --no-access-log`로 갱신해 signed media URL query가 access log에 남지 않게 한다. `app.py` 직접 실행도 `access_log=False`로 설정한다.

### 실패 테스트

FastAPI `TestClient`와 fake client/resolver로 검증한다.

- 미지원 platform/`http`/미허용 host/private DNS → 400
- Douyin/TikTok 정상 chunk가 순서대로 스트리밍되고 close가 정확히 한 번 호출됨
- 302 정상 CDN redirect는 성공, private/localhost redirect는 400
- 403/timeout/500 → 403/504/502
- 응답 `Content-Type`과 attachment header 존재
- 테스트 중 임시 파일이 생성되지 않음
- 오류 JSON/로그에 raw URL, cookie, `ms_token`이 없음

### 검증

```bash
cd tiktok-sidecar
uv run --with-requirements requirements.txt python -m pytest -q tests/test_download.py tests/test_search_route.py tests/test_healthz.py
```

### 커밋

`feat(sidecar): stream guarded Douyin and TikTok downloads`

## Task 3 — Go SidecarClient 스트림 전달

### 파일

- Modify: `sidecar_client.go`
- Modify: `sidecar_client_test.go`

### 구현 계약

검색용 25초 client와 다운로드용 client를 분리한다.

```go
type SidecarClient struct {
    // 기존 필드 유지
    downloadHTTP *http.Client
}

func (c *SidecarClient) Download(ctx context.Context, platform, rawURL string) (*http.Response, error)
```

- sidecar URL은 기존 `baseURL`의 loopback 신뢰 경계이고, `url.Values`로 `platform`과 `url`을 정확히 encode한다.
- 다운로드 client는 body 전체를 읽지 않고 열린 `*http.Response`을 반환한다. 호출자가 반드시 body를 닫는다.
- handler가 부여한 300초 context와 client cancellation을 그대로 전달한다.
- sidecar `403`은 `search.ErrForbidden`에 해당하는 새 sentinel 또는 명시적 typed error로, `504`는 deadline error로, 나머지 4xx/5xx는 `search.ErrBadGateway`, network는 `search.ErrUnreachable`로 매핑한다. 응답 body/error에 raw URL을 포함하지 않는다.
- 검색/health client와 TTL cache 동작은 변경하지 않는다.

### 실패 테스트

- query가 이중 encode되지 않고 sidecar가 원래 URL을 복원함
- 응답 body를 `Download` 내부에서 읽지 않고 caller가 chunk 단위로 읽을 수 있음
- caller context 취소 시 request가 종료되고 body가 닫힘
- `403/504/502/network` 매핑
- 오류 문자열에 signed query가 없음
- 기존 Search/Healthz 테스트 유지

### 검증

```bash
gofmt -w sidecar_client.go sidecar_client_test.go
go test . -run 'TestSidecar(Search|Healthz|Download)' -count=1
go test -race . -run 'TestSidecarDownload' -count=1
```

### 커밋

`feat(download): add streaming sidecar client`

## Task 4 — Go `/api/v1/download` handler와 XHS detail 해결

### 파일

- Create: `handlers_download.go`
- Create: `handlers_download_test.go`
- Modify: `app_server.go`
- Modify: `routes.go`
- Modify: `types.go` 또는 Create: `download_types.go`
- 필요 시 Modify: `search/errors.go`

### 주입 경계

테스트에서 실제 go-rod/외부 네트워크를 열지 않도록 작은 인터페이스를 둔다.

```go
type xhsDownloadResolver interface {
    GetFeedDetail(context.Context, string, string, bool) (*FeedDetailResponse, error)
}

type sidecarDownloader interface {
    Download(context.Context, string, string) (*http.Response, error)
}
```

`AppServer`는 기존 `xiaohongshuService`와 같은 인스턴스를 resolver로, `NewAppServer`에서 만든 같은 `SidecarClient`를 downloader로 보관한다. M1 adapter 조립도 이 sidecar 인스턴스를 재사용한다.

### 요청 검증과 해결 순서

`downloadHandler`는 300초 `context.WithTimeout(c.Request.Context(), 300*time.Second)`를 만든다.

- 공통: platform과 `post_id` 필수. `filename`은 선택이며 CR/LF/NUL/path separator를 제거하고 빈 값이면 `<platform>-<post_id>.mp4`.
- XHS:
  - `detail_token` 필수.
  - 클라이언트 `url`은 수용하지 않는다.
  - `GetFeedDetail(ctx, postID, detailToken, false)`의 `VideoURL`을 사용한다.
  - Task 1 guard의 XHS client로 직접 upstream을 열고 redirect/DNS를 모두 재검증한다.
- Douyin/TikTok:
  - M1 `VideoItem.video_url`이므로 `url` 필수. `post_id` 없이 URL만 전달하는 요청은 거부한다.
  - Go guard로 최초 URL의 scheme/host/DNS를 확인한 뒤 `SidecarClient.Download`에 위임한다.
  - sidecar가 실제 redirect hop을 동일 정책으로 재검증한다.
- 다른 platform, 누락 식별자, SSRF guard 실패 → 400.

### 스트림 응답

- upstream status를 header 쓰기 전에 매핑한다.
- 성공 시 `Content-Type`, 안전한 `Content-Length`가 있으면 보존하고 `Content-Disposition: attachment; filename="..."`, `X-Content-Type-Options: nosniff`, `X-Download-Notice: personal-reference-only`을 설정한다.
- `io.Copy(c.Writer, upstream.Body)`만 사용하고 `defer upstream.Body.Close()`한다.
- copy 중 client cancellation은 추가 JSON을 쓰지 않고 종료한다. timeout이면 아직 header를 쓰지 않은 경우에만 504.
- 응답/로그에 raw URL, signed query, cookie/token을 포함하지 않는다.
- `routes.go`에 `api.GET("/download", appServer.downloadHandler)`를 등록한다.
- 기존 `gin.Logger()`가 raw query를 기록하지 않도록 path-only formatter로 교체한다. formatter는 `param.Request.URL.Path`만 사용하고 RawQuery를 출력하지 않는다.

### 실패 테스트

`httptest` fake resolver/downloader/upstream으로 다음을 고정한다.

- XHS는 client URL을 무시/거부하고 detail resolver 결과만 fetch
- XHS detail_token 누락, detail 실패, 빈 VideoURL → 400/502
- Douyin/TikTok은 post_id+url 조합만 허용
- invalid platform/private URL/allowlist suffix 공격 → 400이며 sidecar 호출 0회
- 정상 chunk 스트림과 Content-Type/Disposition/notice header
- 403/502/504 상태 매핑
- client context 취소가 resolver/upstream까지 전달되고 body close
- filename CRLF/path traversal 정리
- logger output에 raw query와 signed token이 없음
- router에 `/api/v1/download`가 실제 등록됨

### 검증

```bash
gofmt -w app_server.go routes.go handlers_download.go handlers_download_test.go sidecar_client.go sidecar_client_test.go
go test . -run 'TestDownload|TestSidecarDownload|TestStaticRoutes' -count=1
go test -race . -run 'TestDownload|TestSidecarDownload' -count=1
go vet . ./search
go build ./...
```

### 커밋

`feat(download): add guarded streaming download endpoint`

## Task 5 — 카드 다운로드 UI와 Yinziai 수동 fallback

### 파일

- Modify: `web/index.html`
- Modify: `web/app.js`
- Modify: `web/lib.js`
- Modify: `web/style.css`
- Modify: `web/lib.test.mjs`
- Modify: `static_routes_test.go`

### 순수 helper 계약

`web/lib.js`에 DOM과 분리된 helper를 export한다.

```js
export function buildDownloadURL(item, filename = "")
export function canDownload(item)
export function hasXHSManualFallback(item)
```

- XHS URL에는 `platform`, `post_id`, `detail_token`, 선택 filename만 포함하고 `video_url`은 포함하지 않는다.
- Douyin/TikTok URL에는 `platform`, `post_id`, `url=video_url`, 선택 filename을 `URLSearchParams`로 encode한다.
- 필수 식별자가 없으면 빈 문자열을 반환해 다운로드 버튼을 disabled 처리한다.

### 카드 동작

- 카드 body에 다음 action을 추가한다.
  - 다운로드: same-origin `/api/v1/download` anchor. 브라우저 native streaming/download를 사용하고 fetch→Blob 버퍼링 금지.
  - 원본 보기: `post_url`이 있을 때 새 탭.
  - XHS만 `XHS 링크 복사 + Yinziai 열기`: post URL을 clipboard에 복사하고 `https://www.yinziai.com/ko/tools/download-video-xhslink`를 새 탭으로 연다.
- 모든 action은 `data-card-action`을 가지며 grid event delegation이 action click에서 `playItem`을 호출하지 않게 한다.
- Yinziai 새 탭은 사용자 click 동기 구간에서 열어 popup blocker를 피한다. clipboard 실패 시 기존 prompt 복사 fallback을 사용한다.
- Yinziai URL에 XHS 링크를 query/hash로 붙이지 않고 DOM 자동 입력/submit을 하지 않는다.
- 다운로드 실패를 앱 페이지 이탈 없이 확인할 수 있도록 anchor는 새 탭/브라우저 다운로드 문맥을 사용한다. XHS 카드에는 원본과 수동 fallback을 항상 함께 노출한다.
- 카드/모달과 가까운 위치에 다음 의미의 한국어 안내를 표시한다: “권한이 있는 콘텐츠의 개인 참고용이며 재사용 권리를 부여하지 않습니다. 다운로드가 안 되면 원본 게시물 또는 XHS 수동 도구를 이용하세요.”
- 좁은 화면에서 action이 겹치지 않도록 flex-wrap 스타일을 추가한다.

### 실패 테스트

`web/lib.test.mjs`:

- XHS query가 post_id/detail_token만 포함하고 raw video_url을 제외
- Douyin/TikTok URL이 특수문자와 signed query를 정확히 encode/decode
- 식별자/video_url 누락 시 다운로드 불가
- Yinziai fallback은 XHS+post_url일 때만 true
- 기존 11개 테스트 유지

`static_routes_test.go`:

- index에 개인 참고 안내와 필요한 action hook이 존재
- `/static/app.js`, `/static/lib.js`, `/static/style.css` 정상 제공

### 검증

```bash
node --check web/app.js
node --check web/lib.js
node --test web/lib.test.mjs
gofmt -w static_routes_test.go
go test . -run 'TestStaticRoutes|TestDownload' -count=1
```

### 커밋

`feat(web): add streaming download actions and XHS fallback`

## Task 6 — 전체 회귀, cmux 브라우저 수용, 최종 리뷰

### 자동화 회귀

worktree 루트에서 실행한다.

```bash
git status --short
git diff --check f1154ac...HEAD
gofmt -l $(git diff --name-only f1154ac...HEAD -- '*.go')
go build ./...
go vet . ./search
go test . ./search ./pkg/... -count=1
go test -race . ./search -count=1
node --check web/app.js
node --check web/settings.js
node --check web/lib.js
node --test web/lib.test.mjs
cd tiktok-sidecar && uv run --with-requirements requirements.txt python -m pytest -q
```

`go test ./...`는 별도로 한 번 시도하되 기존 live XHS browser 테스트가 cookies 없이 장시간 대기/실패하면 신규 M2 실패와 구분해 기록한다. 무한 대기시키지 않는다.

### cmux 내장 브라우저 수용

외부 브라우저/Playwright 결과만으로 승인하지 않는다. Go 서버와 stub sidecar를 loopback에서 띄운 뒤 cmux browser surface에서 확인한다.

1. `cmux new-pane --type browser ... --url http://127.0.0.1:18060/`으로 전용 surface를 만든다.
2. `cmux browser snapshot`, `console list`, `errors list`로 초기 상태를 기록한다.
3. credential 없는 실제 화면에서 M1 degraded 상태와 기존 검색 흐름이 유지되는지 확인한다.
4. cmux browser `addinitscript` 또는 로컬 stub 응답으로 XHS/Douyin/TikTok 카드 fixture를 렌더한다. 외부 플랫폼 네트워크는 호출하지 않는다.
5. 각 카드에서 다운로드/원본 action이 모달을 열지 않는지, 카드 본문 click은 기존 modal을 여는지 확인한다.
6. Douyin/TikTok 다운로드 link의 query를 decode해 platform/post_id/url이 정확한지 확인한다.
7. XHS download link에 client video_url이 없는지 확인한다.
8. Yinziai action은 새 탭만 열고 XHS URL 자동 제출/요청이 없는지 browser history/network 관점에서 확인한다.
9. 개인용 안내와 좁은 viewport action wrap을 screenshot으로 확인한다.
10. console error/page error 0건을 확인한다.

서버 handler 통합 smoke는 로컬 `httptest`/stub upstream으로 다음을 확인한다.

- chunked response가 첫 chunk부터 전달되고 전체 body를 선버퍼링하지 않음
- attachment filename과 notice header
- localhost/private/redirect SSRF 차단
- client 취소 시 upstream close

### 최종 보안·스펙 리뷰 체크리스트

- [ ] 사용자 URL보다 platform+post_id(+detail_token) 식별이 우선이다.
- [ ] XHS는 client URL을 fetch하지 않는다.
- [ ] `https`, exact suffix, DNS public IP, redirect hop, dial-time 재검증이 있다.
- [ ] Go와 sidecar 어느 쪽도 파일/DB/전체 body 버퍼를 만들지 않는다.
- [ ] 403/502/504와 client cancellation이 테스트됐다.
- [ ] raw URL query/cookie/ms_token이 로그와 오류에 없다.
- [ ] Yinziai는 copy+open만 하고 자동 제출/스크래핑하지 않는다.
- [ ] 개인 참고/권리 안내가 보인다.
- [ ] 기존 검색/미리보기/더보기 테스트가 유지된다.
- [ ] cmux 내장 브라우저에서 console/page error가 없다.
- [ ] live credential smoke만 EXTERNAL BLOCKED로 남고 M2 코드 수용과 분리된다.

### 완료 보고

- 커밋 목록과 변경 파일 수
- 자동화 테스트별 PASS/FAIL/BLOCKED
- cmux 브라우저 확인 결과와 screenshot 경로
- 남은 외부 blocker와 M3 시작 조건
- working tree clean 여부
- push/PR/merge가 없었음을 명시

최종 전체 리뷰에서 P0/P1/P2 문제가 없을 때 M2를 `ACCEPTED`로 표시한다. 문서 표현이나 live credential 부재만으로 구현을 다시 계획하지 않는다.
