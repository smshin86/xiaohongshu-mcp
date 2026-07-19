"""LLM 기반 키워드 추출/번역.

source_index → source_url 서버 매핑으로 URL hallucination 을 방지한다.
LLM 이 source_url 자체를 반환하는 일은 없으며, 서버가 metas[source_index][0]
를 대입한다. LLM 불가/파싱 실패 시 휴리스틱 폴백으로 직접 입력 복구 지원.
"""

from __future__ import annotations

import json
import re
from typing import Any

from llm import LLMClient, LLMUnavailable, MAX_LLM_RESPONSE_BYTES, MAX_PROMPT_BYTES
from metadata import Metadata


# 공개 상수 — app.py / 테스트가 사용.
EXTRACT_NOTE = "텍스트 메타데이터 기반(프레임 비전 분석 제외)"
MAX_EXTRACT = 8
MAX_TRANSLATE = 5
MAX_URL = 2048
MAX_TRANSLATE_TEXT = 200
META_TITLE_CAP = 1000
META_DESC_CAP = 2000
CAND_TEXT_CAP = 64

__all__ = [
    "EXTRACT_NOTE",
    "MAX_EXTRACT",
    "MAX_TRANSLATE",
    "MAX_URL",
    "MAX_TRANSLATE_TEXT",
    "META_TITLE_CAP",
    "META_DESC_CAP",
    "MAX_PROMPT_BYTES",
    "MAX_LLM_RESPONSE_BYTES",
    "CAND_TEXT_CAP",
    "extract_keywords",
    "translate_keywords",
]

_BASIS_VALID = {"title", "hashtag", "description", "metadata"}


def _truncate(text: str, cap: int) -> str:
    if len(text) <= cap:
        return text
    return text[:cap]


def _parse_json_content(content: str) -> Any:
    """LLM content 에서 JSON 을 관대하게 파싱(코드펜스/잡음 허용)."""
    s = (content or "").strip()
    if not s:
        raise ValueError("empty content")
    # ```json ... ``` 코드펜스 제거
    if s.startswith("```"):
        s = re.sub(r"^```[a-zA-Z]*\n?", "", s)
        s = re.sub(r"\n?```$", "", s).strip()
    # 앞뒤 잡음 잘라내기 — 첫 [ 또는 { 부터 마지막 ] 또는 } 까지
    if not s.startswith(("[", "{")):
        start = max(s.find("["), s.find("{"))
        if start < 0:
            raise ValueError("no JSON")
        end = max(s.rfind("]"), s.rfind("}"))
        if end < 0:
            raise ValueError("no JSON")
        s = s[start:end + 1]
    return json.loads(s)


def _build_extract_prompt(metas: list[tuple[str, Metadata]]) -> tuple[str, str]:
    """메타데이터 blob 을 인덱스로 프롬프트에 포함(URL 미포함 → hallucination 방지)."""
    blobs = []
    for i, (_url, m) in enumerate(metas):
        title = _truncate((m.title or "").strip(), META_TITLE_CAP)
        desc = _truncate((m.description or "").strip(), META_DESC_CAP)
        hashtags = " ".join(m.hashtags) if m.hashtags else ""
        blobs.append(
            f"[{i}]\n"
            f"title: {title}\n"
            f"description: {desc}\n"
            f"hashtags: {hashtags}"
        )
    body = "\n---\n".join(blobs)
    system = (
        "你是关键词提取助手。基于用户提供的元数据(标题/描述/标签),"
        f"提取最多 {MAX_EXTRACT} 个关键词候选。"
        f"注意: {EXTRACT_NOTE}。"
        "仅基于文本元数据,不要生成URL。"
        "返回 JSON 数组,每项形如 "
        '{"keyword": str, "source_index": int, "basis": str, "confidence": float}。'
        "basis 必须是 title/hashtag/description/metadata 之一。"
        "source_index 必须是输入元数据的索引(从0开始)。"
    )
    user = f"参考元数据:\n{body}\n\n请返回 JSON 数组。"
    return system, user


def extract_keywords(
    metas: list[tuple[str, Metadata]], llm: LLMClient
) -> list[dict]:
    """메타데이터 → 키워드 후보 목록. LLM 불가/파싱 실패 시 휴리스틱 폴백."""
    if not metas:
        return []

    try:
        system, user = _build_extract_prompt(metas)
        content = llm.complete(system, user)
    except LLMUnavailable:
        return _heuristic_fallback(metas)

    try:
        cands = _parse_json_content(content)
    except Exception:
        return _heuristic_fallback(metas)
    if not isinstance(cands, list):
        return _heuristic_fallback(metas)

    out: list[dict] = []
    seen: set[str] = set()
    for c in cands:
        if not isinstance(c, dict):
            continue
        idx = c.get("source_index")
        # 범위 밖 / non-int / bool → URL 매핑 불가 → drop (metas index 만 신뢰)
        if not isinstance(idx, int) or isinstance(idx, bool) or idx < 0 or idx >= len(metas):
            continue
        kw = c.get("keyword")
        if not isinstance(kw, str):
            continue
        kw = _truncate(kw.strip(), CAND_TEXT_CAP)
        if not kw:
            continue
        kl = kw.lower()
        if kl in seen:
            continue
        basis = c.get("basis")
        if basis not in _BASIS_VALID:
            basis = "metadata"
        try:
            conf = float(c.get("confidence", 0.0))
        except (TypeError, ValueError):
            conf = 0.0
        conf = max(0.0, min(1.0, conf))
        out.append({
            "keyword": kw,
            "source_index": idx,
            "source_url": metas[idx][0],
            "basis": basis,
            "confidence": conf,
        })
        seen.add(kl)
        if len(out) >= MAX_EXTRACT:
            break
    return out


def _heuristic_fallback(metas: list[tuple[str, Metadata]]) -> list[dict]:
    """LLM 없이 메타데이터에서 후보 생성.

    hashtag(basis=hashtag, conf 0.5) 와 title 토큰 길이≥2(basis=title, conf 0.3).
    각 후보의 source_url 은 해당 메타데이터 URL.
    """
    out: list[dict] = []
    seen: set[str] = set()
    for i, (url, m) in enumerate(metas):
        for tag in m.hashtags or []:
            t = _truncate(tag.strip(), CAND_TEXT_CAP)
            if not t:
                continue
            tl = t.lower()
            if tl in seen:
                continue
            seen.add(tl)
            out.append({
                "keyword": t,
                "source_index": i,
                "source_url": url,
                "basis": "hashtag",
                "confidence": 0.5,
            })
            if len(out) >= MAX_EXTRACT:
                return out
        title = (m.title or "").strip()
        if title:
            for tok in re.split(r"\s+", title):
                tok = tok.strip()
                if len(tok) < 2:
                    continue
                t = _truncate(tok, CAND_TEXT_CAP)
                tl = t.lower()
                if tl in seen:
                    continue
                seen.add(tl)
                out.append({
                    "keyword": t,
                    "source_index": i,
                    "source_url": url,
                    "basis": "title",
                    "confidence": 0.3,
                })
                if len(out) >= MAX_EXTRACT:
                    return out
    return out


def _build_translate_prompt(text: str) -> tuple[str, str]:
    system = (
        "你是韩中翻译助手。将用户提供的韩文文本翻译成"
        "适合中国短视频平台搜索的关键词候选。"
        f"最多 {MAX_TRANSLATE} 个候选,每个候选不超过 {CAND_TEXT_CAP} 字。"
        '返回 JSON 对象 {"zh": ["候选1", "候选2", ...]}。'
    )
    user = f"韩文文本: {text}\n\n请返回 JSON。"
    return system, user


def translate_keywords(
    text: str, source_lang: str, llm: LLMClient
) -> list[dict]:
    """한국어 텍스트 → 중국어 검색 키워드 후보.

    source_lang != "ko" → ValueError; 빈/200자 초과 → ValueError;
    llm 불가/파싱 실패/결과 0건 → LLMUnavailable.
    """
    if source_lang != "ko":
        raise ValueError("source_lang must be ko")
    if not isinstance(text, str):
        raise ValueError("text must be string")
    trimmed = text.strip()
    if not trimmed or len(trimmed) > MAX_TRANSLATE_TEXT:
        raise ValueError("text empty or too long")
    if not llm.available():
        raise LLMUnavailable("llm call failed")

    system, user = _build_translate_prompt(trimmed)
    try:
        content = llm.complete(system, user)
    except LLMUnavailable:
        raise

    try:
        parsed = _parse_json_content(content)
    except Exception:
        raise LLMUnavailable("llm call failed") from None

    # {zh: [...]} 또는 [{zh: ...}] / [str, ...] 모두 처리
    zh_list: list[str] = []
    if isinstance(parsed, dict):
        zh = parsed.get("zh")
        if isinstance(zh, list):
            zh_list = [z for z in zh if isinstance(z, str)]
        elif isinstance(zh, str):
            zh_list = [zh]
    elif isinstance(parsed, list):
        for item in parsed:
            if isinstance(item, dict):
                v = item.get("zh")
                if isinstance(v, str):
                    zh_list.append(v)
            elif isinstance(item, str):
                zh_list.append(item)

    out: list[dict] = []
    seen: set[str] = set()
    for z in zh_list:
        z = _truncate(z.strip(), CAND_TEXT_CAP)
        if not z:
            continue
        zl = z.lower()
        if zl in seen:
            continue
        seen.add(zl)
        out.append({"zh": z})
        if len(out) >= MAX_TRANSLATE:
            break
    if not out:
        raise LLMUnavailable("llm call failed")
    return out
