import os
import time
import concurrent.futures
from concurrent.futures import ThreadPoolExecutor, as_completed
from typing import Callable

from fastapi import Depends, FastAPI
from fastapi.responses import JSONResponse, StreamingResponse
from starlette.background import BackgroundTask
from download import (
    DownloadBadRequest,
    DownloadForbidden,
    DownloadTimedOut,
    DownloadUpstreamFailed,
    open_download,
)
from keywords import (
    EXTRACT_NOTE,
    MAX_TRANSLATE_TEXT,
    MAX_URL,
    extract_keywords,
    translate_keywords,
)
from llm import LLMClient, LLMUnavailable
from metadata import Metadata, fetch_metadata
from models import ExtractRequest, SearchRequest, TranslateRequest

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

@app.get("/download")
def download_video(platform: str, url: str):
    try:
        stream = open_download(platform, url)
    except DownloadBadRequest:
        return _fail(400)
    except DownloadForbidden:
        return _fail(403)
    except DownloadTimedOut:
        return _fail(504)
    except DownloadUpstreamFailed:
        return _fail(502)

    headers = {"Content-Disposition": f'attachment; filename="{platform}-video.mp4"'}
    return StreamingResponse(
        stream.iter_bytes(),
        media_type=stream.content_type,
        headers=headers,
        background=BackgroundTask(stream.close),
    )


# M3 — 키워드 추출/번역. DI 로 route 테스트가 DNS/네트워크에 닿지 않게 분리.
def default_metadata_provider() -> Callable[[str], Metadata]:
    """기본 provider = fetch_metadata(URL → Metadata)."""
    return fetch_metadata


def default_llm() -> LLMClient:
    """기본 LLMClient(None → env 읽기)."""
    return LLMClient()


# metadata phase 20s, discovery 전체 50s — LLM 자체 25s timeout 과 함께 안전 여유.
_METADATA_DEADLINE = 20.0
_DISCOVERY_DEADLINE = 50.0
_CONCURRENCY = 3


@app.post("/keywords/extract")
def extract_keywords_route(
    payload: ExtractRequest,
    provider: Callable[[str], Metadata] = Depends(default_metadata_provider),
    llm: LLMClient = Depends(default_llm),
):
    """참고 URL 들에서 메타데이터를 수집해 키워드 후보를 추출.

    - urls trim/dedupe(첫 등장 순서 보존); 빈 또는 >3 → 400
    - url 길이 > MAX_URL 은 skip(400 아님)
    - bounded concurrency(max 3)로 provider 호출; metadata phase 20s deadline
    - 완료 순서와 무관하게 원 입력 순서로 metas 재조립; 실패 URL skip
    - timeout 시 executor.shutdown(wait=False, cancel_futures=True) 로 worker 차단 방지
    - discovery 전체 50s 초과 or 수집/후보 0건 → 200 success:false
    """
    seen: set[str] = set()
    urls: list[str] = []
    for raw in payload.urls:
        if not isinstance(raw, str):
            continue
        u = raw.strip()
        if not u or u in seen:
            continue
        seen.add(u)
        urls.append(u)
    if not urls or len(urls) > 3:
        return _fail(400)

    start = time.monotonic()
    executor = ThreadPoolExecutor(max_workers=_CONCURRENCY)
    future_to_idx: dict = {}
    completed: dict = {}  # idx -> (url, Metadata)
    try:
        for idx, url in enumerate(urls):
            if len(url) > MAX_URL:
                continue  # oversize skip
            future_to_idx[executor.submit(provider, url)] = idx
        try:
            for future in as_completed(future_to_idx.keys(), timeout=_METADATA_DEADLINE):
                idx = future_to_idx[future]
                try:
                    md = future.result()
                except Exception:
                    # MetadataFetchFailed/예기치 못한 오류 — 해당 URL 은 결과에서 skip
                    continue
                completed[idx] = (urls[idx], md)
        except concurrent.futures.TimeoutError:
            pass
    finally:
        # 요청 thread 가 미완료 worker 를 무기한 대기하지 않도록 즉시 해제.
        executor.shutdown(wait=False, cancel_futures=True)

    if time.monotonic() - start > _DISCOVERY_DEADLINE:
        return _ok_empty()

    # 원 입력 순서로 metas 재조립(완료 순서 무관).
    metas: list[tuple[str, Metadata]] = []
    for idx, url in enumerate(urls):
        if len(url) > MAX_URL:
            continue
        if idx in completed:
            metas.append(completed[idx])
    if not metas:
        return _ok_empty()

    try:
        candidates = extract_keywords(metas, llm)
    except Exception:
        return _ok_empty()
    if not candidates:
        return _ok_empty()

    if time.monotonic() - start > _DISCOVERY_DEADLINE:
        return _ok_empty()

    return _ok({"candidates": candidates, "note": EXTRACT_NOTE})


@app.post("/keywords/translate")
def translate_keywords_route(
    payload: TranslateRequest,
    llm: LLMClient = Depends(default_llm),
):
    """한국어 텍스트 → 중국어 검색 키워드 후보.

    - text trim; 빈/200자 초과 → 400
    - source_lang != "ko" → 400
    - LLMUnavailable/timeout → 200 success:false; else 200 success:true
    """
    text = (payload.text or "").strip()
    if not text or len(text) > MAX_TRANSLATE_TEXT:
        return _fail(400)
    if payload.source_lang != "ko":
        return _fail(400)

    try:
        candidates = translate_keywords(text, "ko", llm)
    except ValueError:
        # 라우트에서 사전 검증 → 정상 경로에선 도달하지 않음(안전망).
        return _fail(400)
    except LLMUnavailable:
        return _ok_empty()
    except Exception:
        return _ok_empty()

    return _ok({"candidates": candidates})


def _ok_empty() -> JSONResponse:
    """success:false 빈 데이터 — Go 게이트웨이가 직접 입력 복구로 처리."""
    return JSONResponse({"success": False, "data": {}}, status_code=200)


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="127.0.0.1", port=18061, access_log=False)
