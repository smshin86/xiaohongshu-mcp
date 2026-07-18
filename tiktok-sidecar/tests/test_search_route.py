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
