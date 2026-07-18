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
