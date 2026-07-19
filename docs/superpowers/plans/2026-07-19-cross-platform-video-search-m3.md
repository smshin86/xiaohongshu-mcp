# Cross-Platform Video Search — M3 (참고 URL → 키워드 추출/번역 → 통합 검색) Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development 로 Task 단위 구현. 각 Task 는 TDD(실패 테스트→구현→검증→커밋). 본 계획은 파일·계약·실패 테스트·검증 명령만 포함(긴 구현 코드는 의도적 생략 — 계약과 테스트가 명세).

**Goal:** M1/M2 를 **additive** 로 확장해, 참고 영상 URL(YouTube/TikTok/Instagram, ≤3) → 텍스트 메타데이터 기반 키워드 후보 + 한국어→중국어 후보 칩 → 사용자 선택/수정 → 기존 통합 검색 합류, 추출 실패 시 직접 입력 복구를 추가. 비전/프레임 분석 제외.

**Architecture:** Discovery(메타데이터 fetch + LLM)는 Python 사이드카 위임; Go 는 same-origin `/api/v1/keywords/*` 게이트웨이. 프론트는 진입바 "URL 분석" 모드 + 기존 검색 submit 의 한국어 감지→번역 칩으로 M1 `/api/v1/search` 흐름을 재사용.

## Global Constraints

- **Base / 브랜치:** base = `feat/m2-download @ ba94ae3`. 실행 시 `feat/m3-keyword-discovery` 신규 분기. **승인 전까지 구현·커밋·브랜치 생성·push/PR/merge 금지**(본 계획은 수정 전용).
- **기존 동작 보존(additive):** M1/M2 파일은 M3 진입점만 추가하고, M3와 무관한 기존 함수·라우트·동작은 보존한다. 한국어 submit 분기 추가는 M3 acceptance에 따른 의도된 확장이고, 중국어/영어 검색·M1 카드·M2 다운로드 동작은 변경하지 않는다. import 재사용 허용(`download.GuardedNetworkBackend`/`_resolve_public`/`_host_matches`/`_REDIRECT_STATUSES`/`_MAX_REDIRECTS`, `SidecarClient`, `AppServer`, `search.*`, `web/lib.js` 기존 함수).
- **포트/env:** Go `127.0.0.1:18060`; sidecar `127.0.0.1:18061`(외부 공개 금지). `SIDECAR_URL`(기본 `http://127.0.0.1:18061`); `LLM_API_KEY`/`LLM_BASE`/`LLM_MODEL`(sidecar 프로세스만).
- **Discovery 위임:** 메타데이터 수집·LLM 호출은 sidecar; Go 는 게이트웨이(자체 LLM 호출 금지).
- **SSRF(/keywords/extract):** 사용자 URL → YouTube/TikTok/Instagram 호스트 allowlist, https-only, userinfo 거부, hostname ToLower + **정확한 suffix match**(suffix attack 차단), DNS 공인-전용, **dial 시 재검증 공인 IP pin**(M2 `ba94ae3` 패턴, `download.GuardedNetworkBackend` 재사용), redirect **each hop 재검증**(≤5). `download.DownloadBadRequest`(dial/resolve 시) → `MetadataFetchFailed` 안전 매핑. **스트리밍 응답만(`stream=True`); `resp.content` 무제한 버퍼 금지**; Content-Type HTML allowlist(`text/html`); Content-Length 및 **디코딩 본문 2MiB cap**; oversize/non-HTML/빈본문 → 거부(`MetadataFetchFailed`).
- **자격증명·LLM secret 비노출:** `LLM_API_KEY`/`LLM_BASE`/`LLM_MODEL`·`TT_MSTOKEN`/`DOUYIN_COOKIE`/cookies 는 응답 JSON·에러 메시지·로그(Go gin path-only formatter 유지, sidecar `access_log=False` 유지) 어디에도 평문 금지.
- **테스트(외부 API 금지):** 모든 외부 호출(메타데이터 fetch, LLM)은 주입/스텁. Python 테스트 경로 `tiktok-sidecar/tests/test_*.py`; 명령 `cd tiktok-sidecar && uv run --with-requirements requirements.txt python -m pytest tests/... -q`(`/tmp` venv 사용 금지). Task 1 은 resolver/opener seam 으로 DNS/redirect/size 를 단위 검증(네트워크 없음); Task 3 route 테스트는 metadata service 를 주입(DNS 미접촉).
- **타임아웃/동시성:** sidecar metadata phase 20s(max 3 concurrent, 개별 HTTP connect 10s/read 15s) + LLM 25s, discovery 전체 50s. Go: 전용 `keywordsHTTP *http.Client{Timeout:60s}` + 핸들러 `ctx 60s` + cancel propagation(요청에 caller ctx 전달).
- **에러 정책(Go 게이트웨이):** recoverable(sidecar `success:false`: 메타데이터 0건·LLM 불가) → `200 {success:false}`(직접 입력 복구); sidecar 도달 불가/불량 응답 → `502 {success:false, message}`; deadline(60s) → `504 {success:false, message}`; client canceled → 응답 미작성(중단); unexpected → `500`. 입력 오류(빈/`>3` URL·빈 또는 200자 초과 text·`source_lang!="ko"`) → `400 {success:false, message}`.
- **비전/프레임 제외:** 메타데이터 텍스트(title/description/hashtag)만.
- **Go 변경 후 `gofmt -w`**; 테스트 산출물 즉시 제거.

---

### Task 1: 사이드카 — SSRF-안전 스트리밍 메타데이터 fetch (`metadata.py`)

**Files:** Create `tiktok-sidecar/metadata.py`; Test `tiktok-sidecar/tests/test_metadata.py`

**Contract (재사용 + 신규):**
- `download` 에서 import(수정 금지): `GuardedNetworkBackend`, `_resolve_public`, `_host_matches`, `_REDIRECT_STATUSES`, `_MAX_REDIRECTS`.
- `METADATA_ALLOWED_HOSTS = {"youtube.com","youtu.be","tiktok.com","instagram.com"}`; `_host_matches`(host==suffix or endswith "."+suffix).
- `class MetadataFetchFailed(Exception)`; `@dataclass Metadata(title: str = "", description: str = "", hashtags: list[str] = field(default_factory=list))`(아래 테스트/호출의 생략 인자와 일치).
- `validate_metadata_url(raw_url, resolver=socket.getaddrinfo) -> str`: raw_url 길이 ≤2048; https-only; userinfo 거부; hostname ToLower + suffix allowlist; `_resolve_public`(공인만); `download.DownloadBadRequest`/`_resolve_public` 실패 → `MetadataFetchFailed`.
- `fetch_metadata(raw_url, *, opener=None, resolver=socket.getaddrinfo) -> Metadata`:
  - redirect 루프가 **each hop** 마다 `validate_metadata_url(current, resolver)` 호출(최초 + redirect Location 도 urljoin 후 검증); hops ≤ `_MAX_REDIRECTS` 초과 → fail.
  - `opener(request_url, headers) -> Resp`(`Resp.status_code:int`, `Resp.headers:mapping`, `Resp.iter_bytes()->iter[bytes]`, `Resp.close()`). 기본 opener = guarded httpx client(`GuardedNetworkBackend` pin, `follow_redirects=False`, `trust_env=False`, connect 10/read 15)로 **`client.send(req, stream=True)`**; 반환 wrapper의 `close()`가 response와 client를 모두 닫고 redirect/오류/성공 파싱 모두 `finally`에서 닫는다. backend/resolve 의 `DownloadBadRequest` → `MetadataFetchFailed`.
  - 본문 읽기 전: status≥400 → fail; `Content-Type` 이 `text/html` 로 시작(allowlist) 아니면 fail; `Content-Length` > 2MiB → fail.
  - 본문: `iter_bytes()` 누적 ≤ **2MiB**; 초과 시 `Resp.close()` 후 fail. `MetadataHTMLParser`(`<title>`, og:title/og:description/twitter:description/meta description, 본문 `#hashtag` 추출). title/desc/hashtag 모두 빈 → fail.
- `opener` seam 덕분에 stub 이 redirect 를 **따르게** 하여 redirect 재검증을 실제로 시험(이전 draft 의 “stub 이 모든 redirect 차단” 결함 수정).

**Failing tests** (`tests/test_metadata.py`) — fixtures: `public_resolver`/`private_resolver`/stateful `rebinding_resolver`; `FakeResp(status, headers, body_bytes_or_chunks)`(`iter_bytes`/`close`). 테스트:
- `test_validate_suffix_attack`: `evilyoutube.com`, `youtube.com.evil.com`, `notyoutube.com` → fail; `www.youtube.com`/`youtu.be`/`www.tiktok.com`/`www.instagram.com` → ok.
- `test_validate_rejects_http_userinfo_private_longurl`: http/userinfo/`192.168.x`/길이>2048 → fail.
- `test_validate_empty_dns_raises_metadatafailed`: `resolver → []` → `MetadataFetchFailed`.
- `test_fetch_parses_title_desc_hashtags`: opener 200 `text/html` + canned HTML → `Metadata(title, hashtags contains portablefan/fan/cooling)`.
- `test_fetch_allowed_redirect_validates_target`: opener hop1 302→`https://www.youtube.com/watch?v=1&x=1`, hop2 200 → ok(redirect target 공인 검증 통과).
- `test_fetch_private_redirect_blocked`: opener 302→`https://127.0.0.1/secret` → `MetadataFetchFailed`.
- `test_fetch_disallowed_redirect_blocked`: opener 302→`https://evil.com/x` → fail.
- `test_fetch_public_to_private_rebinding_blocked`: opener hop1 302→동일 `www.youtube.com` 경로; `rebinding_resolver`(call1 공인, call2 사설) → hop2 검증에서 fail.
- `test_fetch_hop_limit`: opener 매호 302→신규 allowed url 6회 → fail.
- `test_fetch_rejects_non_html_content_type`: `Content-Type: application/json` → fail.
- `test_fetch_rejects_oversize_content_length`: `Content-Length: 3000000` → fail(미읽기).
- `test_fetch_rejects_oversize_streamed_body`: `iter_bytes` 3MiB yield → 2MiB 초과 감지 → fail.

대표 본문(rebinding + oversize):
```python
def test_fetch_public_to_private_rebinding_blocked():
    calls = {"n": 0}
    def resolver(host, port, type=socket.SOCK_STREAM):
        calls["n"] += 1
        ip = "142.250.0.1" if calls["n"] == 1 else "127.0.0.1"
        return [(socket.AF_INET, socket.SOCK_STREAM, 6, "", (ip, port))]
    def opener(url, headers):
        if url.endswith("?next=1"):
            return FakeResp(200, {"content-type": "text/html"}, HTML)
        return FakeResp(302, {"location": "https://www.youtube.com/watch?v=1?next=1"}, b"")
    with pytest.raises(MetadataFetchFailed):
        fetch_metadata("https://www.youtube.com/watch?v=1", opener=opener, resolver=resolver)

def test_fetch_rejects_oversize_streamed_body():
    def opener(url, headers):
        return FakeResp(200, {"content-type": "text/html"}, [b"<html>" + b"x" * (2 * 1024 * 1024 + 1)])
    with pytest.raises(MetadataFetchFailed):
        fetch_metadata("https://www.tiktok.com/@u/video/1", opener=opener, resolver=public_resolver)
```

**Validation:** `cd tiktok-sidecar && uv run --with-requirements requirements.txt python -m pytest tests/test_metadata.py -q` → `12 passed`.

**Commit:** `feat(sidecar): add SSRF-safe streamed metadata fetch with size/html caps`

---

### Task 2: 사이드카 — LLM 클라이언트 + 키워드 추출/번역 (`llm.py`, `keywords.py`)

**Files:** Create `tiktok-sidecar/llm.py`, `tiktok-sidecar/keywords.py`; Test `tiktok-sidecar/tests/test_keywords.py`

**Contract:**
- `llm.LLMUnavailable(Exception)`; `llm.LLMClient(base=None, key=None, model=None, transport=None)`:
  - `None` 인자 → env(`LLM_BASE`/`LLM_API_KEY`/`LLM_MODEL`) 읽기; **명시적 `""` → disable**.
  - `available()` = `transport is not None` (주입 모드: key/base 무관 available) **또는** (`key and base` truthy). 주입 transport 사용 시 env/key 불필요(테스트 일관성).
  - `complete(system, user) -> str`: available 아니면 `LLMUnavailable`; UTF-8 prompt가 8KiB를 넘으면 안전하게 truncate. transport 있으면 transport(url,headers,body)→parsed, 없으면 httpx `_post`(`trust_env=False`, connect 10s/overall 25s, streaming **본문 ≤16KiB read cap**). 모든 예외/로그에 key 평문 금지 → `LLMUnavailable("llm call failed")` 로 래핑.
- `keywords` 상수: `EXTRACT_NOTE="텍스트 메타데이터 기반(프레임 비전 분석 제외)"`, `MAX_EXTRACT=8`, `MAX_TRANSLATE=5`, `MAX_URL=2048`, `MAX_TRANSLATE_TEXT=200`, `META_TITLE_CAP=1000`, `META_DESC_CAP=2000`, `MAX_PROMPT_BYTES=8192`, `MAX_LLM_RESPONSE_BYTES=16384`, `CAND_TEXT_CAP=64`.
- `extract_keywords(metas: list[tuple[str, Metadata]], llm) -> list[dict]`:
  - 빈 metas → `[]`. 메타데이터 blob 은 index(`[0]..[n]`)로 프롬프트에 포함(title/desc `META_*_CAP` truncate).
  - LLM 은 `{keyword, source_index:int, basis, confidence}` 반환(★ source_url 반환 금지 → URL hallucination 방지). 서버가 `source_url = metas[source_index][0]` 매핑; index 범위 밖 → 해당 후보 drop.
  - basis ∈ {title,hashtag,description,metadata}; keyword ≤`CAND_TEXT_CAP` truncate; confidence clamp 0..1; keyword 중복제거(lower); `MAX_EXTRACT` cap.
  - `LLMUnavailable`/파싱 실패 → **휴리스틱 폴백**: hashtag(`basis=hashtag`), title 토큰 길이≥2(`basis=title`), 각 `source_url`=해당 url, confidence 0.5/0.3.
- `translate_keywords(text, source_lang, llm) -> list[dict]`:
  - `source_lang != "ko"` 또는 trim한 text가 빈 값/200자 초과 → `ValueError`; llm 미가용 → `LLMUnavailable`. prompt/response cap 적용. LLM `{zh}` 후보, zh ≤`CAND_TEXT_CAP`, 중복제거, `MAX_TRANSLATE` cap. 결과 0건 → `LLMUnavailable`.

**Failing tests** (`tests/test_keywords.py`) — `stub_transport(payload)` 로 `LLMClient(transport=...)` 생성(available):
- `test_extract_maps_source_index_to_url`: stub 이 indexed candidates 반환 → `source_url` 가 정확한 입력 URL; 범위 밖 index drop.
- `test_extract_clamps_dedupes_caps`: confidence 1.5 clamp, 중복 keyword 제거, 50개 → ≤`MAX_EXTRACT`.
- `test_extract_falls_back_to_heuristic`: `LLMClient(key="")`(disable) → hashtag+title 폴백, `source_url` 세팅, basis ∈ {hashtag,title}.
- `test_extract_empty_returns_empty`: `extract_keywords([], llm) == []`.
- `test_translate_requires_ko_and_llm`: `source_lang="en"` → `ValueError`; `LLMClient(key="")` → `LLMUnavailable`.
- `test_translate_parses_zh`: stub → `{zh}` 후보.
- `test_constructor_none_reads_env_but_explicit_empty_disables`: env가 있어도 `key=""`는 unavailable; `None`만 env 사용.
- `test_prompt_response_and_translate_text_caps`: 200자 초과 번역 입력 거부, 16KiB 초과 응답은 `LLMUnavailable`.
- `test_secret_not_in_transport_failure`: dummy base/key와 `httpx.ConnectError`를 던지는 injected transport(실 네트워크 없음) → `LLMUnavailable`; `"SECRET-KEY" not in str(exc)`.

대표 본문(source_index 매핑):
```python
def test_extract_maps_source_index_to_url():
    payload = {"choices": [{"message": {"content": json.dumps([
        {"keyword": "便携风扇", "source_index": 1, "basis": "title", "confidence": 0.9},
        {"keyword": "风扇", "source_index": 9, "basis": "hashtag", "confidence": 0.4},
    ])}}]}
    llm = LLMClient(transport=stub_transport(payload))
    metas = [("https://www.youtube.com/watch?v=1", Metadata(title="a")),
             ("https://www.tiktok.com/@u/video/2", Metadata(title="便携风扇"))]
    out = extract_keywords(metas, llm)
    assert len(out) == 1  # source_index 9 범위밖 → drop
    assert out[0]["source_url"] == "https://www.tiktok.com/@u/video/2"
```

**Validation:** `cd tiktok-sidecar && uv run --with-requirements requirements.txt python -m pytest tests/test_keywords.py -q` → `9 passed`.

**Commit:** `feat(sidecar): add LLM client and keyword extract/translate with source-index mapping`

---

### Task 3: 사이드카 — `/keywords` 라우트 + DI + bounded 동시성 + timeout

**Files:** Modify `tiktok-sidecar/models.py`(요청 모델 2개 additive), `tiktok-sidecar/app.py`(2 라우트 + DI defaults, additive); Test `tiktok-sidecar/tests/test_app_keywords.py`

**Contract:**
- `models.ExtractRequest{urls: list[str]}`; `models.TranslateRequest{text: str; source_lang: str = "ko"}`.
- app.py DI defaults: `default_metadata_provider() -> Callable[[str], Metadata]`(기본 `fetch_metadata`); `default_llm() -> LLMClient`. **route 경계는 metadata service 를 주입**(raw fetcher 아님 → route 테스트가 DNS 미접촉). `app.dependency_overrides` 는 pytest **fixture 에서 전후 clear**(`yield` 후 `app.dependency_overrides.clear()`).
- `POST /keywords/extract`: urls trim/dedupe; 빈 또는 `>3` → `_fail(400)`; 각 url 길이 ≤`MAX_URL`(초과 skip). **bounded concurrency**(`ThreadPoolExecutor(max_workers=3)`)로 metadata_provider 호출, metadata phase 20s·discovery 전체 50s deadline 적용; 완료 순서와 무관하게 원 입력 순서로 metas를 재조립(source_index 안정성), 실패 URL skip. timeout 시 `shutdown(wait=False, cancel_futures=True)`로 요청 thread가 무기한 worker를 기다리지 않게 한다. 수집 0건 → `200 {success:false,data:{}}`. `extract_keywords`; 후보 0건 → `200 success:false`; else `200 {success:true,data:{candidates,note:EXTRACT_NOTE}}`. discovery timeout 초과 → `200 success:false`.
- `POST /keywords/translate`: text trim; 빈/200자 초과 → `_fail(400)`; `source_lang!="ko"` → `_fail(400)`; 전체 50s 안에서 `translate_keywords` 실행(LLM 자체 25s); `LLMUnavailable`/timeout → `200 success:false`; else `200 {success:true,data:{candidates}}`.

**Failing tests** (`tests/test_app_keywords.py`, TestClient + DI fixture, **DNS 없음**):
- `test_extract_success_returns_note_and_candidates`: provider stub → `Metadata`; llm stub transport → candidates+note.
- `test_extract_all_provider_failures_success_false`: provider 항상 `MetadataFetchFailed` → `200 success:false`.
- `test_extract_input_validation`: 빈 urls / 4 urls → `400`; `test_extract_preserves_input_order_under_concurrency`: 완료 순서가 뒤바뀌어도 source_index URL 매핑은 입력 순서 유지.
- `test_translate_success` / `test_translate_empty_or_too_long_400` / `test_translate_lang_not_ko_400` / `test_translate_llm_unavailable_success_false`.
- `test_no_secret_in_response`: `LLM_API_KEY` env 세팅 후 response body 에 key 미포함.
- `test_dependency_overrides_cleared`: 한 테스트 종류 후 `app.dependency_overrides` 빈 확인(fixture 동작).

대표(fixture + extract):
```python
@pytest.fixture
def client_factory():
    created = []
    def make(provider, llm):
        app.app.dependency_overrides[app.default_metadata_provider] = lambda: provider
        app.app.dependency_overrides[app.default_llm] = lambda: llm
        c = TestClient(app.app); created.append(c); return c
    yield make
    app.app.dependency_overrides.clear()  # 전후 clear

def test_extract_success_returns_note_and_candidates(client_factory):
    def provider(u): return Metadata(title="Portable Fan", hashtags=["portablefan"])
    llm = LLMClient(transport=stub_transport({"choices": [{"message": {"content":
        json.dumps([{"keyword":"便携风扇","source_index":0,"basis":"title","confidence":0.9}])}}]}))
    c = client_factory(provider, llm)
    r = c.post("/keywords/extract", json={"urls": ["https://www.youtube.com/watch?v=1"]})
    assert r.status_code == 200 and r.json()["data"]["note"].startswith("텍스트 메타데이터")
```

**Validation:** `cd tiktok-sidecar && uv run --with-requirements requirements.txt python -m pytest tests/test_app_keywords.py -q && uv run --with-requirements requirements.txt python -m pytest tests/ -q` → 신규 + 전체 GREEN.

**Commit:** `feat(sidecar): add /keywords routes with DI and bounded metadata concurrency`

---

### Task 4: Go — `/api/v1/keywords/*` 게이트웨이(클라이언트 + 핸들러 + seam + timeout + 에러 정책)

**Files:** Modify `sidecar_client.go`(전용 `keywordsHTTP` + 2 메서드, **inline 구현 하나로 확정 — generic 변형 금지**), `search/errors.go`(`ErrKeywordsFailed`), `app_server.go`(`sidecarKeywords` seam + `keywordsHTTP` 초기화), `routes.go`(2 라우트); Create `handlers_keywords.go`, `sidecar_client_keywords_test.go`, `handlers_keywords_test.go`

**Contract:**
- `SidecarClient` 에 `keywordsHTTP *http.Client{Timeout:60*time.Second}` 추가(`NewSidecarClient` 초기화; search 25s·download Timeout 없음 과 분리). 메서드는 caller ctx 로 요청 → **cancel propagation**.
- `search.ErrKeywordsFailed`(신규 sentinel).
- `ExtractKeywords(ctx, urls []string) (SidecarKeywordResult, error)` / `TranslateKeywords(ctx, text, sourceLang string) (SidecarTranslateResult, error)`: POST `/keywords/extract|translate` via `keywordsHTTP`; 매핑 — 200+success:true→data; 200+success:false→`ErrKeywordsFailed`; ≥400→`ErrBadGateway`; 네트워크→`ErrUnreachable`; `ctx.DeadlineExceeded`/`Canceled`→그대로 상위.
- 타입: `SidecarKeywordCandidate{Keyword,SourceURL,Basis string; Confidence float64}`, `SidecarKeywordResult{Candidates []...; Note string}`, `SidecarTranslateCandidate{ZH string}`, `SidecarTranslateResult{Candidates []...}`(+ internal envelope `{Success, Data}`).
- `sidecarKeywords` interface(`AppServer` seam); `(*AppServer).extractKeywordsHandler`/`translateKeywordsHandler`. 핸들러 `ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)`.
- **에러 정책(구현 의사결정표):**
  | 사태 | HTTP | body |
  |---|---|---|
  | 입력 오류(빈/`>3` URL·빈/200자 초과 text·`source_lang!="ko"`) | 400 | `{success:false,message:"..."}`(한국어) |
  | `ErrKeywordsFailed`(sidecar success:false) | 200 | `{success:false}`(직접 입력 복구) |
  | `search.ErrUnreachable`(sidecar 다운) | 502 | `{success:false,message:"서비스에 연결할 수 없습니다."}` |
  | `search.ErrBadGateway`(sidecar 불량 상태/응답) | 502 | `{success:false,message:"키워드 서비스 응답이 올바르지 않습니다."}` |
  | `DeadlineExceeded`(60s) | 504 | `{success:false,message:"응답 시간 초과입니다."}` |
  | `context.Canceled` | — | 응답 미작성(return) |
  | 기타 | 500 | `{success:false,message:"오류가 발생했습니다."}` |
  | 성공 | 200 | `{success:true,data:...}` |
- routes: `api.POST("/keywords/extract", appServer.extractKeywordsHandler)`, `api.POST("/keywords/translate", appServer.translateKeywordsHandler)`.

**Failing tests:**
- `sidecar_client_keywords_test.go`(httptest): `TestSidecarExtractParsesCandidates`/`SuccessFalse→ErrKeywordsFailed`/`502→ErrBadGateway`/`network→ErrUnreachable`/`ctx cancel propagates`(`keywordsHTTP` 사용).
- `handlers_keywords_test.go`(gin test context, **`c.Request.Header.Set("Content-Type","application/json")")`**):
  - `TestExtractHandlerSuccessAndRecoverable`: 성공→200 success:true; `ErrKeywordsFailed`→200 success:false.
  - `TestExtractHandlerInputValidation`: 빈/4 urls→400.
  - `TestExtractHandlerUnreachable502`/`DeadlineExceeded504`.
  - `TestTranslateHandlerEmptyAndLangValidation`: 빈/200자 초과 text→400; `source_lang!="ko"`→400; `ErrUnreachable`/`ErrBadGateway`→502.
  - `TestHandlerCanceledWritesNothing`: canceled ctx → body 길이 0·응답 header 미설정(Recorder 기본 code 값으로 판정하지 않음).
  - `TestKeywordRoutesRegistered`: `setupRoutes(app).Routes()`에 두 POST path가 정확히 등록됐는지 검사(실 sidecar 호출 없음).

대표(핸들러 recoverable + 502):
```go
func TestExtractHandlerRecoverable200False_Unreachable502(t *testing.T) {
	s := newKeywordsServer(fakeKeywords{extractErr: search.ErrKeywordsFailed})
	w := httptest.NewRecorder(); c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte(`{"urls":["https://www.youtube.com/a"]}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	s.extractKeywordsHandler(c)
	require.Equal(t, 200, w.Code); require.Contains(t, w.Body.String(), `"success":false`)

	s2 := newKeywordsServer(fakeKeywords{extractErr: search.ErrUnreachable})
	w2 := httptest.NewRecorder(); c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte(`{"urls":["https://www.youtube.com/a"]}`)))
	c2.Request.Header.Set("Content-Type", "application/json")
	s2.extractKeywordsHandler(c2)
	require.Equal(t, 502, w2.Code)
}
```

**Validation:** `gofmt -l . && go vet . ./search && go build ./... && go test ./... -run 'TestSidecarExtract|TestSidecarTranslate|TestExtractHandler|TestTranslateHandler|TestKeywordRoutesRegistered' -count=1` → clean + PASS.

**Commit:** `feat: proxy keyword extract/translate via /api/v1/keywords with 60s timeout`

---

### Task 5: 웹 — "URL 분석" 진입 + 한국어→중국어 칩(기존 검색 submit 에 통합)

**Files:** Modify `web/lib.js`(순수 헬퍼), `web/lib.test.mjs`(테스트), `web/app.js`(`onSearchSubmit` 통합), `web/index.html`(URL 섹션 + `#candidate-chips`), `web/style.css`(`.candidate-chips`/`.cand-chip`), `static_routes_test.go`(DOM/스크립트 hook 회귀)

**Contract(기존 식별자 준수):** 키워드 입력 id = **`keyword`**(`$("keyword")` 헬퍼 사용; `keyword-input` 금지); form=`search-form`, status=`status`, sort=`sort`. **`.chips` 는 플랫폼 fieldset 전용** → 후보 칩은 전용 `#candidate-chips` 컨테이너 + `.cand-chip` 아이템 사용(`.chips` 재사용 금지).
- `lib.js`(순수, DOM 무의존):
  - `parseUrlList(text) -> string[]`: ws/줄바꿈/쉼표 분할·trim·중복제거·빈 제거. **캡 없음**(모두 반환; `>3` 검증은 UI).
  - `isKorean(text) -> bool`: hangul(`가-힣`) 포함 여부.
  - `normalizeExtractResponse(json) -> {failed:boolean, candidates, note}`: `!success`/`data` 결측/`candidates` 비배열 → failed; 빈 keyword는 제거하고 64자 초과는 절단하며 최대 8개. 정규화 후 빈 배열도 failed.
  - `normalizeTranslateResponse(json) -> {failed, candidates:[{zh}]}`: 동일 규칙으로 빈 zh 제거·64자 절단·중복제거·최대 5개.
  - `basisLabel(basis)`: title→제목/hashtag→해시태그/description→설명/metadata→메타/그외→메타.
- `app.js`(별도 translate 버튼 **없음** — 기존 submit 에 통합):
  - `api.extract(urls)`/`api.translate(text)`를 same-origin POST JSON으로 추가하고 각각 `/api/v1/keywords/extract`, `/api/v1/keywords/translate` 호출.
  - `#entry-mode-toggle` click은 `show/hide(#url-entry)`로 `.hidden` class를 토글하고 `aria-expanded`를 갱신. `#entry-mode-toggle`, `#analyze-btn`, 동적 `.cand-chip`은 모두 `type="button"`이라 form submit을 유발하지 않는다.
  - `onSearchSubmit`: `kw=$("keyword").value.trim()`; 빈→`setStatus("검색어를 입력해 주세요")`+return. **`isKorean(kw)` →** `api.translate(kw)`; 네트워크 throw/`normalizeTranslateResponse(failed)` → `setStatus("번역 실패 — 중국어로 직접 입력해 주세요")` + **STOP(검색 미실행, 직접 입력 복구)**; 성공 → `#candidate-chips` 에 중국어 칩 렌더 + `setStatus("중국어 후보를 선택/수정한 뒤 다시 검색")` + **STOP**(기존 `runSearch` 호출 안 함). `!isKorean` → 기존 `resetForNewSearch`+`runSearch`.
  - analyze(`#analyze-btn`): `parseUrlList($("url-input").value)`; 길이 0 또는 `>3` → `setStatus("URL 1~3개를 입력해 주세요")`+STOP(조용히 truncate 금지). `api.extract(urls)` try/catch; throw/fail → `setStatus("분석 실패 — 키워드를 직접 입력해 주세요")`+STOP; 성공 → `.cand-chip` 렌더(클릭 시 `$("keyword").value=kw` + focus). 빈/과다 후보는 `normalize*` 로 정규화.
- `index.html`: `<button id="entry-mode-toggle" type="button" aria-controls="url-entry" aria-expanded="false">URL 분석</button>`, `<section id="url-entry" class="hidden"><textarea id="url-input">…</textarea><button id="analyze-btn" type="button">분석</button></section>`, `<div id="candidate-chips" class="candidate-chips"></div>`(검색 폼 내부). boolean `hidden` attribute는 기존 `show/hide` helper와 맞지 않으므로 사용하지 않는다.
- `style.css`: `.candidate-chips`(flex-wrap, 425px 오버플로우 없음), `.cand-chip`(pill, hover).

**Failing tests** (`web/lib.test.mjs`):
- `parseUrlList`: 분할/중복제거/빈 제거; **4 URL → 4 반환(캡 없음)**.
- `isKorean`: "손 선풍기"→true, "便携风扇"→false, "fan"→false.
- `normalizeExtractResponse`: success→candidates+note; `!success`/결측/빈 candidates→`failed:true`.
- `normalizeTranslateResponse`: success→`[{zh}]`; `!success`→failed.
- `basisLabel` 매핑(+default 메타).
- `static_routes_test.go`: `/` HTML에 `entry-mode-toggle/url-entry/url-input/analyze-btn/candidate-chips`와 두 비-submit 버튼이 존재하고, `/static/app.js`에 두 keyword API path와 이벤트 hook이 존재함을 확인.

**Validation:** `cd web && node --check app.js && node --test lib.test.mjs` → node --check OK + JS 전체 PASS(기존 + 신규 5).

**Commit:** `feat(web): add URL analysis and Korean-to-Chinese candidate chips into search flow`

---

### Task 6: 전체 회귀 + cmux 브라우저 수용 + 최종 리뷰

**Files:** 없음(검증 전용). 결함 시에만 해당 파일 수정 + 추가 커밋.

**Validation:**
1. 자동화(워크트리 루트):
   - `gofmt -l .`(출력 없음); `go vet . ./search`; `go build ./...`.
   - Go 타깃: `go test ./... -run 'TestSidecarExtract|TestSidecarTranslate|TestExtractHandler|TestTranslateHandler|TestStaticRoutes|TestDownload' -count=1`.
   - **전체 `go test ./...`** 는 별도 timeout(`-timeout 180s`)으로 시도; 기존 live XHS 외부 blocker(`xiaohongshu/TestSearchWithFilters`, go-rod UI 120s)는 **EXTERNAL BLOCKED 로 분리 기록**(회귀 아님).
   - JS: `cd web && node --check app.js && node --test lib.test.mjs`.
   - Python: `cd tiktok-sidecar && uv run --with-requirements requirements.txt python -m pytest tests/ -q`(신규 + 기존).
2. cmux 브라우저 수용(로컬 Go 서버 `127.0.0.1:18060`, 실 LLM/외부 메타데이터는 자격증명·외부 차단 → 단위 테스트가 대체; UI surface + 실패 복구 + 회귀를 직접 확인):
   - **성공 경로(synthetic 고정)**: cmux 내장 브라우저의 init script/eval로 `window.fetch`를 keyword/search path별 합성 응답으로 주입한 뒤 reload. extract → `#candidate-chips` `.cand-chip` 렌더(425px flex-wrap 오버플로우 없음) → 칩 클릭 → `$("keyword")` 채움 → 기존 검색 wiring 정상. 실제 외부 URL/LLM 성공에 의존하지 않는다.
   - **한국어→중국어 흐름**: 한국어 키워드 submit → 중국어 칩 표시 + **검색 중단** → 칩 선택 후 재검색 시에만 `/api/v1/search`.
   - **실패 복구**: 불가 호스트(`https://evil.com/x`) → "분석 실패 — 키워드를 직접 입력해 주세요"; LLM 미설정 환경 번역 → "번역 실패 — 중국어로 직접 입력해 주세요"; 빈/4 URL → "URL 1~3개를 입력해 주세요".
   - **secret 비노출**: 브라우저 응답 본문·콘솔·Go 서버 로그(path-only)에 `LLM_API_KEY`/query/토큰 미포함.
   - **회귀**: M1 키워드 검색(칩=platforms·통합 그리드·더보기) + M2 다운로드 아이콘/모달 정상.
3. 최종 리뷰(`git log --oneline ba94ae3..HEAD` + `git diff ba94ae3..HEAD`): 계약·Global Constraints 대조(additive 보존·SSRF allowlist·공인 IP pin·redirect 재검증·size/html cap·source-index 매핑·secret 비노출·에러 정책표·외부 API 없는 테스트). 실제 결함만 수정(추가 커밋).

**Commit:** 결함 수정 시만(`fix(...)` 커밋); 없으면 최종 상태 보고. push/PR/merge 금지(승인 후 별도).

---

## Self-Review

**1. Spec coverage(M3, spec L320–321 / L167–174 / L272):** URL ≤3(allowlist+입력검증, Task1·4·5) ✅; `/keywords/extract` 텍스트 메타데이터(비전 제외, Task1·2 + EXTRACT_NOTE) ✅; 키워드 후보 칩(Task3 응답+Task5 렌더) ✅; 한국어→`/keywords/translate` 중국어 칩→선택/수정→검색(Task3·4·5, submit 통합) ✅; 추출 실패→직접 입력(Task3 success:false+Task4 정책+Task5 메시지) ✅. SSRF(allowlist·공인IP pin·redirect 재검증·size/html cap) ✅; secret 비노출(Task2·3·4) ✅; 외부 API 없는 테스트(Task1 seam·Task2 stub·Task3 DI) ✅.

**2. 블로커 반영:** ① base=`feat/m2-download@ba94ae3`, additive 보존(“import only” 제거), Python `tests/`+`uv run --with-requirements` ✅; ② stream+2MiB/HTML cap, DownloadBadRequest→MetadataFetchFailed, redirect 재검증 실제 시험(opener seam) + suffix/rebinding/oversize 테스트 ✅; ③ None sentinel+명시적 empty disable+주입 transport 일관 available, transport 실패 secret 테스트(dummy base/key), source_index 매핑(URL hallucination 방지), 글자수 cap, source_lang=ko 검증 ✅; ④ route 은 metadata service 주입(DNS 미접촉), Task1 resolver/client 별도 검증, fixture DI clear, bounded concurrency, sidecar 50s/Go 60s timeout+cancel propagation, gin 테스트 Content-Type, 에러 정책표(recoverable만 200/deadline 504/canceled 중단), Go inline 단일 구현 ✅; ⑤ `#candidate-chips`/`.cand-chip` 전용(`.chips`=fieldset 분리), 입력 id `keyword`, 별도 translate 버튼 제거(submit 한국어 감지→칩→검색 중단), 4 URL silent truncate 금지(1~3 검증), network catch+한국어 메시지+빈/과다 정규화 ✅; ⑥ 성공(synthetic chip→search)+실패 복구 cmux 수용, M1/M2 회귀, 전체 go test 별도 timeout+XHS 외부 blocker 분리 ✅.

**3. Type consistency:** `SidecarKeywordCandidate/Result`·`SidecarTranslateCandidate/Result`(Task4) ↔ Python `{keyword,source_url,basis,confidence}`/`{zh}`(Task2·3) ↔ JS `normalizeExtractResponse/normalizeTranslateResponse`(Task5) 필드명 일치. `ErrKeywordsFailed`(Task4)가 errors.go·핸들러·테스트 동일 사용. `EXTRACT_NOTE`(Task2)↔Task3 응답↔Task5 note. basis enum `title|hashtag|description|metadata` 가 Task2(검증)·Task5(`basisLabel`) 일치. `source_index` → `source_url` 매핑이 Task2(LLM 반환)·Task3(응답 그대로)·Task4(전달) 일치.

**4. 범위:** Task 수 = 6. additive(M1/M2 동작 보존). 계획 수정 전용(구현·커밋·브랜치·push/PR/merge 금지). 긴 구현 코드 제거(파일·계약·실패 테스트·검증만).
