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
