"""tests/test_app_keywords.py — /keywords/* 라우트 통합 (DI + DNS 없음).

계약 요지:
- POST /keywords/extract: urls trim/dedupe; 빈 또는 >3 → 400; url 길이 >MAX_URL → skip.
  ThreadPoolExecutor(max_workers=3) 로 provider 호출; metadata phase 20s · discovery 50s deadline.
  futures 완료 순서와 무관하게 원 입력 순서로 metas 재조립. 실패 URL skip.
  수집 0건 또는 후보 0건 → 200 success:false.
- POST /keywords/translate: text trim; 빈/200자 초과 → 400; source_lang!="ko" → 400.
  LLMUnavailable/timeout → 200 success:false; else 200 success:true.
- DI(default_metadata_provider/default_llm) 주입 → 라우트 테스트는 DNS/네트워크 미접촉.
- LLM_API_KEY 평문은 응답 본문 어디에도 비노출.
"""

from __future__ import annotations

import json
import time

import pytest
from fastapi.testclient import TestClient

import app
from keywords import MAX_TRANSLATE_TEXT
from llm import LLMClient
from metadata import Metadata, MetadataFetchFailed


def stub_transport(payload):
    """payload 를 그대로 반환하는 transport — available 일관성(네트워크 없음)."""

    def transport(url, headers, body):
        return payload

    return transport


@pytest.fixture
def client_factory():
    """DI override client factory — 테스트 끝나면 dependency_overrides clear."""
    created: list = []

    def make(provider, llm):
        # default_metadata_provider() → provider callable; default_llm() → llm 인스턴스.
        app.app.dependency_overrides[app.default_metadata_provider] = lambda: provider
        app.app.dependency_overrides[app.default_llm] = lambda: llm
        c = TestClient(app.app)
        created.append(c)
        return c

    yield make
    # 전후 clear — 한 테스트의 override 가 다음 테스트로 누출되지 않는다.
    app.app.dependency_overrides.clear()


def _disabled_llm():
    """LLM 호출 불가 상태(key/base 명시적 "")."""
    return LLMClient(key="", base="")


# ---------- extract ----------

def test_extract_success_returns_note_and_candidates(client_factory):
    """provider stub → Metadata; LLM stub transport → candidates + EXTRACT_NOTE."""
    def provider(u):
        return Metadata(title="Portable Fan", hashtags=["portablefan"])

    llm = LLMClient(transport=stub_transport({"choices": [{"message": {"content": json.dumps([
        {"keyword": "便携风扇", "source_index": 0, "basis": "title", "confidence": 0.9},
    ])}}]}))
    c = client_factory(provider, llm)
    r = c.post("/keywords/extract", json={"urls": ["https://www.youtube.com/watch?v=1"]})
    assert r.status_code == 200
    body = r.json()
    assert body["success"] is True
    data = body["data"]
    assert data["note"].startswith("텍스트 메타데이터")
    cands = data["candidates"]
    assert len(cands) == 1
    assert cands[0]["keyword"] == "便携风扇"
    assert cands[0]["source_url"] == "https://www.youtube.com/watch?v=1"
    assert cands[0]["source_index"] == 0


def test_extract_all_provider_failures_success_false(client_factory):
    """provider 항상 MetadataFetchFailed → 200 success:false."""
    def provider(u):
        raise MetadataFetchFailed("stub failure")

    llm = LLMClient(transport=stub_transport({"choices": [{"message": {"content": "[]"}}]}))
    c = client_factory(provider, llm)
    r = c.post("/keywords/extract", json={
        "urls": [
            "https://www.youtube.com/watch?v=1",
            "https://www.tiktok.com/@u/video/2",
        ]
    })
    assert r.status_code == 200
    body = r.json()
    assert body["success"] is False
    assert body["data"] == {}


def test_extract_input_validation(client_factory):
    """빈 urls / 4 urls → 400."""
    def provider(u):
        return Metadata(title="x")

    llm = LLMClient(transport=stub_transport({"choices": [{"message": {"content": "[]"}}]}))
    c = client_factory(provider, llm)
    # 빈 urls
    r = c.post("/keywords/extract", json={"urls": []})
    assert r.status_code == 400
    # urls 4개 → 초과
    r = c.post("/keywords/extract", json={
        "urls": [
            "https://www.youtube.com/watch?v=1",
            "https://www.youtube.com/watch?v=2",
            "https://www.youtube.com/watch?v=3",
            "https://www.youtube.com/watch?v=4",
        ]
    })
    assert r.status_code == 400


def test_extract_dedupes_urls(client_factory):
    """중복 URL 은 첫 번째만 유지 입력 순서 보존."""
    seen: list = []

    def provider(u):
        seen.append(u)
        return Metadata(title=f"title-{u}")

    llm = LLMClient(transport=stub_transport({"choices": [{"message": {"content": json.dumps([
        {"keyword": "k1", "source_index": 0, "basis": "title", "confidence": 0.5},
    ])}}]}))
    c = client_factory(provider, llm)
    r = c.post("/keywords/extract", json={
        "urls": [
            "https://www.youtube.com/watch?v=1",
            "https://www.youtube.com/watch?v=1",  # dup
            "https://www.tiktok.com/@u/video/2",
        ]
    })
    assert r.status_code == 200
    # provider 는 dedupe 이후 2번만 호출
    assert len(seen) == 2
    body = r.json()
    assert body["success"] is True


def test_extract_preserves_input_order_under_concurrency(client_factory):
    """완료 순서가 뒤바뀌어도 source_url/source_index 매핑은 원 입력 순서 유지."""
    # 의도적 delay: idx 0 이 가장 늦게 완료, idx 1 이 가장 빠름.
    # as_completed 는 idx 1 → 2 → 0 순서로 반환하겠지만, 결과는 입력 순서대로 조립.
    delays = {
        "https://www.youtube.com/watch?v=1": 0.25,
        "https://www.youtube.com/watch?v=2": 0.02,
        "https://www.youtube.com/watch?v=3": 0.10,
    }

    def provider(u):
        time.sleep(delays[u])
        return Metadata(title=f"title-{u[-1]}")

    llm = LLMClient(transport=stub_transport({"choices": [{"message": {"content": json.dumps([
        {"keyword": "k1", "source_index": 0, "basis": "title", "confidence": 0.9},
        {"keyword": "k2", "source_index": 1, "basis": "title", "confidence": 0.9},
        {"keyword": "k3", "source_index": 2, "basis": "title", "confidence": 0.9},
    ])}}]}))
    c = client_factory(provider, llm)
    r = c.post("/keywords/extract", json={
        "urls": [
            "https://www.youtube.com/watch?v=1",
            "https://www.youtube.com/watch?v=2",
            "https://www.youtube.com/watch?v=3",
        ]
    })
    assert r.status_code == 200
    body = r.json()
    assert body["success"] is True
    cands = body["data"]["candidates"]
    # source_url/source_index 모두 원 입력 순서
    assert [c["source_url"] for c in cands] == [
        "https://www.youtube.com/watch?v=1",
        "https://www.youtube.com/watch?v=2",
        "https://www.youtube.com/watch?v=3",
    ]
    assert [c["source_index"] for c in cands] == [0, 1, 2]


# ---------- translate ----------

def test_translate_success(client_factory):
    """LLM stub transport → {zh:[...]} 후보."""
    llm = LLMClient(transport=stub_transport({"choices": [{"message": {"content": json.dumps({
        "zh": ["便携风扇", "风扇"],
    })}}]}))
    c = client_factory(lambda u: Metadata(), llm)
    r = c.post("/keywords/translate", json={"text": "휴대용 선풍기", "source_lang": "ko"})
    assert r.status_code == 200
    body = r.json()
    assert body["success"] is True
    cands = body["data"]["candidates"]
    assert cands == [{"zh": "便携风扇"}, {"zh": "风扇"}]


def test_translate_empty_or_too_long_400(client_factory):
    """빈/공백/200자 초과 text → 400."""
    llm = LLMClient(transport=stub_transport({"choices": [{"message": {"content": "[]"}}]}))
    c = client_factory(lambda u: Metadata(), llm)
    # 빈
    r = c.post("/keywords/translate", json={"text": "", "source_lang": "ko"})
    assert r.status_code == 400
    # 공백만
    r = c.post("/keywords/translate", json={"text": "   ", "source_lang": "ko"})
    assert r.status_code == 400
    # 200자 초과
    r = c.post("/keywords/translate", json={
        "text": "한" * (MAX_TRANSLATE_TEXT + 1),
        "source_lang": "ko",
    })
    assert r.status_code == 400


def test_translate_lang_not_ko_400(client_factory):
    """source_lang != "ko" → 400."""
    llm = LLMClient(transport=stub_transport({"choices": [{"message": {"content": "[]"}}]}))
    c = client_factory(lambda u: Metadata(), llm)
    r = c.post("/keywords/translate", json={"text": "휴대용 선풍기", "source_lang": "en"})
    assert r.status_code == 400


def test_translate_llm_unavailable_success_false(client_factory):
    """LLM 불가 → 200 success:false (LLM 평문 비노출)."""
    llm = _disabled_llm()
    assert llm.available() is False
    c = client_factory(lambda u: Metadata(), llm)
    r = c.post("/keywords/translate", json={"text": "휴대용 선풍기", "source_lang": "ko"})
    assert r.status_code == 200
    body = r.json()
    assert body["success"] is False
    assert body["data"] == {}


# ---------- secret non-disclosure ----------

def test_no_secret_in_response(client_factory, monkeypatch):
    """LLM_API_KEY 평문이 응답 본문 어디에도 나오지 않는다."""
    secret = "DISTINCTIVE_SECRET_12345"
    monkeypatch.setenv("LLM_API_KEY", secret)
    monkeypatch.setenv("LLM_BASE", "https://api.example.com/v1")
    monkeypatch.delenv("LLM_MODEL", raising=False)
    # transport 주입 → available. env 의 key/base 는 여전히 인스턴스에 저장되지만
    # 응답/에러/로그 어디에도 평문이 노출되지 않아야 한다.
    llm = LLMClient(transport=stub_transport({"choices": [{"message": {"content": json.dumps({
        "zh": ["便携风扇"],
    })}}]}))

    def provider(u):
        return Metadata(title="x")

    c = client_factory(provider, llm)
    r = c.post("/keywords/translate", json={"text": "휴대용 선풍기", "source_lang": "ko"})
    assert r.status_code == 200
    assert secret not in r.text


# ---------- fixture teardown canary ----------

def test_dependency_overrides_cleared():
    """client_factory fixture teardown 가 dependency_overrides 를 clear 했는지 검증.

    파일 내 정의 순서대로 실행되므로, 선행 client_factory 테스트들이 남긴 override 가
    없어야 한다. teardown 가 동작하지 않으면 이 테스트가 실패한다.
    """
    assert app.app.dependency_overrides == {}
