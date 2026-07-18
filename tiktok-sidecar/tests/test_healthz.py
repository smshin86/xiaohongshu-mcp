import importlib

def _reload_app(monkeypatch, env):
    for k in ("DOUYIN_COOKIE", "TT_MSTOKEN", "LLM_API_KEY"):
        monkeypatch.delenv(k, raising=False)
    for k, v in env.items():
        monkeypatch.setenv(k, v)
    import app
    importlib.reload(app)
    return app

def test_healthz_shape_and_all_false(monkeypatch):
    app = _reload_app(monkeypatch, {})
    assert app.health_state() == {"status": "ok", "douyin": False, "tiktok": False, "llm": False}

def test_healthz_reflects_env(monkeypatch):
    app = _reload_app(monkeypatch, {"DOUYIN_COOKIE": "x=1", "TT_MSTOKEN": "tok"})
    # health_state 는 항상 status:"ok" 포함(Go sidecarHealth.Status 계약) — 누락 금지.
    assert app.health_state() == {"status": "ok", "douyin": True, "tiktok": True, "llm": False}

def test_healthz_endpoint_ok_status(client, monkeypatch):
    _reload_app(monkeypatch, {"DOUYIN_COOKIE": "x=1"})
    r = client.get("/healthz")
    assert r.status_code == 200
    body = r.json()
    assert body["status"] == "ok"          # Go sidecarHealth.Status 계약
    assert body["douyin"] is True
    assert "x=1" not in r.text             # 평문 비밀 미노출
