"""keywords.py / llm.py 단위 테스트 — stub transport 로 네트워크 없이 검증.

계약 요지:
- llm.LLMClient 는 None 인자 → env / 명시적 "" → disable / transport 주입 → always available.
- complete() 는 LLMUnavailable 로 모든 실패를 래핑(메시지/예외 어디에도 key 평문 금지).
- extract_keywords 는 source_index → source_url 매핑(URL hallucination 방지), 실패 시 휴리스틱 폴백.
- translate_keywords 는 source_lang != "ko" → ValueError, 빈/200자 초과 → ValueError, llm 불가 → LLMUnavailable.
"""

import json

import httpx
import pytest

from keywords import (
    CAND_TEXT_CAP,
    EXTRACT_NOTE,
    MAX_EXTRACT,
    MAX_LLM_RESPONSE_BYTES,
    MAX_TRANSLATE,
    MAX_TRANSLATE_TEXT,
    META_DESC_CAP,
    META_TITLE_CAP,
    extract_keywords,
    translate_keywords,
)
from llm import LLMClient, LLMUnavailable
from metadata import Metadata


def stub_transport(payload):
    """payload 를 그대로 반환하는 transport 주입 — available 일관성(네트워크 없음)."""

    def transport(url, headers, body):
        return payload

    return transport


def test_extract_maps_source_index_to_url():
    """LLM 이 source_index 를 반환하면 서버가 URL 매핑. 범위 밖 index 후보 drop."""
    payload = {"choices": [{"message": {"content": json.dumps([
        {"keyword": "便携风扇", "source_index": 1, "basis": "title", "confidence": 0.9},
        {"keyword": "风扇", "source_index": 9, "basis": "hashtag", "confidence": 0.4},
    ])}}]}
    llm = LLMClient(transport=stub_transport(payload))
    metas = [("https://www.youtube.com/watch?v=1", Metadata(title="a")),
             ("https://www.tiktok.com/@u/video/2", Metadata(title="便携风扇"))]
    out = extract_keywords(metas, llm)
    assert len(out) == 1  # source_index 9 범위밖 → drop
    assert out[0]["source_url"] == "https://www.tiktok.com/@u/video/2"
    assert out[0]["keyword"] == "便携风扇"
    assert out[0]["source_index"] == 1
    assert out[0]["basis"] == "title"
    assert out[0]["confidence"] == pytest.approx(0.9)


def test_extract_clamps_dedupes_caps():
    """confidence clamp 0..1, keyword lower 중복제거, MAX_EXTRACT cap."""
    cands = [{"keyword": "Fan", "source_index": 0, "basis": "title", "confidence": 1.5},
             {"keyword": "fan", "source_index": 0, "basis": "title", "confidence": -0.2}]
    for i in range(50):
        cands.append({"keyword": f"tag{i}", "source_index": 0,
                      "basis": "hashtag", "confidence": 0.5})
    payload = {"choices": [{"message": {"content": json.dumps(cands)}}]}
    llm = LLMClient(transport=stub_transport(payload))
    metas = [("https://www.youtube.com/watch?v=1", Metadata(title="Fan review"))]
    out = extract_keywords(metas, llm)
    assert len(out) <= MAX_EXTRACT
    # confidence clamp
    for c in out:
        assert 0.0 <= c["confidence"] <= 1.0
    # keyword lower 중복제거
    lowers = [c["keyword"].lower() for c in out]
    assert len(lowers) == len(set(lowers))
    # "Fan"/"fan" lower 동일 → 하나만
    assert lowers.count("fan") == 1


def test_extract_falls_back_to_heuristic():
    """LLMClient(key="") → disable → 휴리스틱 폴백(hashtag 0.5, title 0.3)."""
    llm = LLMClient(key="")  # 명시적 "" → disable
    assert llm.available() is False
    metas = [("https://www.youtube.com/watch?v=1",
              Metadata(title="Portable Fan Review", hashtags=["portablefan", "fan"]))]
    out = extract_keywords(metas, llm)
    assert out  # 비어있지 않음
    # source_url 매핑
    assert all(c["source_url"] == "https://www.youtube.com/watch?v=1" for c in out)
    # basis ∈ {hashtag, title}
    bases = {c["basis"] for c in out}
    assert bases.issubset({"hashtag", "title"})
    # confidence 폴백 규칙
    for c in out:
        if c["basis"] == "hashtag":
            assert c["confidence"] == pytest.approx(0.5)
        elif c["basis"] == "title":
            assert c["confidence"] == pytest.approx(0.3)
    # MAX_EXTRACT cap 준수
    assert len(out) <= MAX_EXTRACT


def test_extract_empty_returns_empty():
    """빈 metas 입력 → 빈 리스트(즉시 반환, LLM 호출 없음)."""
    llm = LLMClient(transport=stub_transport({"choices": [{"message": {"content": "[]"}}]}))
    assert extract_keywords([], llm) == []


def test_translate_requires_ko_and_llm():
    """source_lang != "ko" → ValueError; llm 불가 → LLMUnavailable."""
    llm = LLMClient(transport=stub_transport({"choices": [{"message": {"content": "[]"}}]}))
    # source_lang != "ko"
    with pytest.raises(ValueError):
        translate_keywords("손 선풍기", "en", llm)
    # llm 불가
    disabled = LLMClient(key="")
    with pytest.raises(LLMUnavailable):
        translate_keywords("손 선풍기", "ko", disabled)


def test_translate_parses_zh():
    """LLM {zh} 후보 파싱 — CAND_TEXT_CAP/MAX_TRANSLATE cap."""
    payload = {"choices": [{"message": {"content": json.dumps([
        {"zh": "便携风扇"}, {"zh": "风扇"}, {"zh": "便携风扇"},  # 중복 → drop
    ])}}]}
    llm = LLMClient(transport=stub_transport(payload))
    out = translate_keywords("손 선풍기", "ko", llm)
    assert 1 <= len(out) <= MAX_TRANSLATE
    assert out[0]["zh"] == "便携风扇"
    assert all(1 <= len(c["zh"]) <= CAND_TEXT_CAP for c in out)
    # 중복 제거
    zhs = [c["zh"] for c in out]
    assert len(zhs) == len(set(zhs))


def test_constructor_none_reads_env_but_explicit_empty_disables(monkeypatch):
    """None 인자 → env 읽기 / 명시적 "" → disable."""
    monkeypatch.setenv("LLM_BASE", "https://api.example.com/v1")
    monkeypatch.setenv("LLM_API_KEY", "ENV-KEY-1234")
    monkeypatch.setenv("LLM_MODEL", "gpt-test")
    # None → env 사용 → available
    env_client = LLMClient()
    assert env_client.available() is True
    # 명시적 "" → disable (env 가 있어도)
    disabled = LLMClient(key="")
    assert disabled.available() is False
    disabled_base = LLMClient(base="")
    assert disabled_base.available() is False
    # transport 주입 → key/base 무관 available
    injected = LLMClient(key="", base="", transport=stub_transport({"choices": []}))
    assert injected.available() is True


def test_prompt_response_and_translate_text_caps():
    """번역 입력 200자 초과/빈 → ValueError; 16KiB 초과 응답 → LLMUnavailable."""
    llm = LLMClient(transport=stub_transport({"choices": [{"message": {"content": "[]"}}]}))
    # 200자 초과 번역 입력
    with pytest.raises(ValueError):
        translate_keywords("한" * (MAX_TRANSLATE_TEXT + 1), "ko", llm)
    # 빈/공백 text
    with pytest.raises(ValueError):
        translate_keywords("   ", "ko", llm)
    # 16KiB 초과 LLM 응답 → LLMUnavailable (complete 직접 검증)
    big = LLMClient(transport=stub_transport(
        {"choices": [{"message": {"content": "x" * (MAX_LLM_RESPONSE_BYTES + 1)}}]}))
    with pytest.raises(LLMUnavailable):
        big.complete("system", "user")


def _connect_error_transport(url, headers, body):
    """httpx.ConnectError 을 발생시키는 주입 transport — 네트워크 없이 실패 검증."""
    raise httpx.ConnectError("simulated connect failure")


def test_secret_not_in_transport_failure():
    """transport 실패 시 LLMUnavailable — key 평문 비노출(네트워크 없음)."""
    # 주입 transport 가 ConnectError 를 raise → LLMUnavailable 로 wrapping.
    llm = LLMClient(base="https://example.invalid/v1", key="SECRET-KEY",
                    transport=_connect_error_transport)
    assert llm.available() is True
    with pytest.raises(LLMUnavailable) as exc_info:
        llm.complete("system", "user")
    # 메시지/원인 어디에도 key 평문이 나와선 안 된다.
    assert "SECRET-KEY" not in str(exc_info.value)
    assert "SECRET-KEY" not in repr(exc_info.value)
