"""LLM 클라이언트 — OpenAI 호환 chat completions.

주입 transport(테스트) 또는 httpx(실 호출) 경로로 completion 을 수행한다.
- None 인자 → env / 명시적 "" → disable / transport 주입 → always available.
- 모든 실패는 LLMUnavailable("llm call failed") 로 래핑 — key 평문은 예외/메시지 어디에도 비노출.
"""

from __future__ import annotations

import json
import os
from typing import Callable, Mapping

import httpx


class LLMUnavailable(Exception):
    """LLM 호출이 불가하거나 실패함."""


# 프롬프트/응답 크기 cap — keywords.py 가 동일 값으로 재사용(re-export).
MAX_PROMPT_BYTES = 8192
MAX_LLM_RESPONSE_BYTES = 16384

_DEFAULT_MODEL = "gpt-4o-mini"
_TIMEOUT = httpx.Timeout(25.0, connect=10.0)


class LLMClient:
    """OpenAI 호환 chat completions 클라이언트.

    인자 해석:
      None  → env(LLM_BASE/LLM_API_KEY/LLM_MODEL) 읽기.
      ""    → 명시적 disable(key/base 한정).
      값    → 그대로 사용.
      transport != None → 테스트 모드. key/base 무관 항상 available.
    """

    def __init__(
        self,
        base: str | None = None,
        key: str | None = None,
        model: str | None = None,
        transport: Callable[[str, Mapping[str, str], dict], dict] | None = None,
    ):
        self._transport = transport
        # 명시적 "" → disable. None → env.
        self._base = os.environ.get("LLM_BASE") if base is None else base
        self._key = os.environ.get("LLM_API_KEY") if key is None else key
        if model is None:
            model = os.environ.get("LLM_MODEL")
        self._model = model or _DEFAULT_MODEL

    def available(self) -> bool:
        """transport 주입 → 항상 가능; 아니면 key+base truthy."""
        if self._transport is not None:
            return True
        return bool(self._key) and bool(self._base)

    def complete(self, system: str, user: str) -> str:
        """system/user 프롬프트로 completion 호출 후 content 문자열 반환."""
        if not self.available():
            raise LLMUnavailable("llm call failed")
        # UTF-8 프롬프트 8KiB cap — 안전하게 truncate.
        sys_s = system.encode("utf-8")[:MAX_PROMPT_BYTES].decode("utf-8", errors="ignore")
        usr_s = user.encode("utf-8")[:MAX_PROMPT_BYTES].decode("utf-8", errors="ignore")
        body = {
            "model": self._model,
            "messages": [
                {"role": "system", "content": sys_s},
                {"role": "user", "content": usr_s},
            ],
        }
        try:
            if self._transport is not None:
                parsed = self._transport(self._endpoint(), self._headers(), body)
            else:
                parsed = self._post(self._endpoint(), self._headers(), body)
        except LLMUnavailable:
            raise
        except Exception:
            # 원 예외 chaining 금지 — key 가 traceback context 에 누출되지 않도록.
            raise LLMUnavailable("llm call failed") from None

        # 응답 cap — 직렬화된 본문이 16KiB 초과 시 거부.
        try:
            serialized = json.dumps(parsed, ensure_ascii=False)
        except Exception:
            raise LLMUnavailable("llm call failed") from None
        if len(serialized.encode("utf-8")) > MAX_LLM_RESPONSE_BYTES:
            raise LLMUnavailable("llm call failed") from None

        try:
            return parsed["choices"][0]["message"]["content"]
        except Exception:
            raise LLMUnavailable("llm call failed") from None

    def _endpoint(self) -> str:
        base = (self._base or "").rstrip("/")
        return f"{base}/chat/completions"

    def _headers(self) -> dict[str, str]:
        headers = {"Content-Type": "application/json"}
        if self._key:
            headers["Authorization"] = f"Bearer {self._key}"
        return headers

    def _post(self, url: str, headers: Mapping[str, str], body: dict) -> dict:
        """httpx 경로 — trust_env=False, 본문 16KiB read cap."""
        payload = json.dumps(body).encode("utf-8")
        # 어떤 예외라도 동일한 LLMUnavailable 로 래핑하기 위해 예외는 호출자(complete)가 처리.
        with httpx.Client(trust_env=False, timeout=_TIMEOUT) as client:
            with client.stream("POST", url, headers=dict(headers), content=payload) as resp:
                if resp.status_code >= 400:
                    raise LLMUnavailable("llm call failed")
                buf = bytearray()
                for chunk in resp.iter_bytes():
                    buf.extend(chunk)
                    if len(buf) > MAX_LLM_RESPONSE_BYTES:
                        raise LLMUnavailable("llm call failed")
                try:
                    return json.loads(bytes(buf).decode("utf-8"))
                except Exception:
                    raise LLMUnavailable("llm call failed") from None
