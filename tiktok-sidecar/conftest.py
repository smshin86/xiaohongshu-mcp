import importlib
import pytest
from fastapi.testclient import TestClient

@pytest.fixture
def client(monkeypatch):
    import app
    importlib.reload(app)
    # app 은 모듈, app.app 은 FastAPI 인스턴스 — TestClient 에는 인스턴스를 넘긴다.
    return TestClient(app.app)
