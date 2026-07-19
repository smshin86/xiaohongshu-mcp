import os
from fastapi import FastAPI
from fastapi.responses import JSONResponse, StreamingResponse
from starlette.background import BackgroundTask
from download import (
    DownloadBadRequest,
    DownloadForbidden,
    DownloadTimedOut,
    DownloadUpstreamFailed,
    open_download,
)
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

if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="127.0.0.1", port=18061, access_log=False)
