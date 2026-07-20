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
    # 0 综合(relevance) / 1 最新(latest) / 2 최다 좋아요(popularity)
    return {"latest": "1", "popularity": "2"}.get(sort, "0")


def _douyin_publish_time(date_from: str, date_to: str) -> str:
    # 안전 버킷(무조건 강제 금지): 0 무제한 1 1일 이내 7 1주 이내 182 반년 이내
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
    # 근사 매핑: 0 무제한 1 1분 이하 2 1~5분 3 5분 이상
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
