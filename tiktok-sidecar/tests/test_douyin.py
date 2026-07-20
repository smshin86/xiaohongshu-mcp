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
