"""SSRF-안전 스트리밍 메타데이터 fetch.

사용자 제공 참고 URL 에서 title/description/hashtags 만 추출한다. download.py 의
검증 프리미티브(GuardedNetworkBackend/_resolve_public/_host_matches)를 재사용하고,
redirect 각 hop 마다 URL/DNS 를 재검증한다. 본문은 2MiB 이상 읽지 않고 HTML 만 허용한다.
"""

from __future__ import annotations

import re
import socket
from dataclasses import dataclass, field
from html.parser import HTMLParser
from typing import Callable, Iterable, Mapping
from urllib.parse import urljoin, urlsplit

import httpx

from download import (
    DownloadBadRequest,
    GuardedNetworkBackend,
    _host_matches,
    _MAX_REDIRECTS,
    _REDIRECT_STATUSES,
    _resolve_public,
)


METADATA_ALLOWED_HOSTS = {"youtube.com", "youtu.be", "tiktok.com", "instagram.com"}

_MAX_URL_LENGTH = 2048
_MAX_BODY_BYTES = 2 * 1024 * 1024  # 디코딩 본문 2MiB cap


class MetadataFetchFailed(Exception):
    """메타데이터 수집 실패(안전 정책/HTML/크기/파싱)."""


@dataclass
class Metadata:
    title: str = ""
    description: str = ""
    hashtags: list[str] = field(default_factory=list)


def validate_metadata_url(raw_url: str, resolver: Callable = socket.getaddrinfo) -> str:
    """https/allowlist host/public DNS 만 허용하고 원문 URL 반환."""
    if not isinstance(raw_url, str) or len(raw_url) > _MAX_URL_LENGTH:
        raise MetadataFetchFailed("invalid metadata url")
    try:
        parsed = urlsplit(raw_url)
        port = parsed.port or 443
    except ValueError as exc:
        raise MetadataFetchFailed("invalid metadata url") from exc
    if parsed.scheme.lower() != "https" or not parsed.hostname:
        raise MetadataFetchFailed("invalid metadata url")
    if parsed.username is not None or parsed.password is not None:
        raise MetadataFetchFailed("invalid metadata url")
    host = parsed.hostname.lower()
    if not _host_matches(host, METADATA_ALLOWED_HOSTS):
        raise MetadataFetchFailed("metadata host is not allowed")
    try:
        _resolve_public(host, port, resolver)
    except DownloadBadRequest as exc:
        raise MetadataFetchFailed("metadata host is not public") from exc
    return raw_url


class _Resp:
    """기본 opener 가 반환하는 wrapper — response+client lifetime 통합 관리."""

    def __init__(self, response: httpx.Response, client: httpx.Client):
        self._response = response
        self._client = client

    @property
    def status_code(self) -> int:
        return self._response.status_code

    @property
    def headers(self) -> Mapping[str, str]:
        return self._response.headers

    def iter_bytes(self) -> Iterable[bytes]:
        return self._response.iter_bytes()

    def close(self) -> None:
        try:
            self._response.close()
        finally:
            self._client.close()


def _make_default_opener(resolver: Callable) -> Callable[[str, Mapping[str, str]], _Resp]:
    # 검증된 IP pin 을 dial 에 적용하기 위해 GuardedNetworkBackend 를 transport 에 주입.
    def opener(url: str, headers: Mapping[str, str]) -> _Resp:
        transport = httpx.HTTPTransport(trust_env=False)
        transport._pool._network_backend = GuardedNetworkBackend(resolver)
        client = httpx.Client(
            follow_redirects=False,
            timeout=httpx.Timeout(15.0, connect=10.0),
            trust_env=False,
            transport=transport,
        )
        try:
            request = client.build_request("GET", url, headers=dict(headers))
            response = client.send(request, stream=True)
        except Exception:
            client.close()
            raise
        return _Resp(response, client)

    return opener


class _MetadataHTMLParser(HTMLParser):
    """<title>/og/twitter/meta description 과 본문 #hashtag 추출."""

    _DESC_KEYS = {"description", "og:description", "twitter:description"}

    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self._title = ""
        self._og_title = ""
        self._tw_title = ""
        self.description = ""
        self.hashtags: list[str] = []
        self._in_title = False

    def handle_starttag(self, tag, attrs):
        tag = tag.lower()
        attrs_d = {k.lower(): (v or "") for k, v in attrs}
        if tag == "title":
            self._in_title = True
            return
        if tag != "meta":
            return
        name = attrs_d.get("name", "").lower()
        prop = attrs_d.get("property", "").lower()
        content = attrs_d.get("content", "").strip()
        if not content:
            return
        if prop == "og:title":
            if not self._og_title:
                self._og_title = content
        elif name == "twitter:title":
            if not self._tw_title:
                self._tw_title = content
        elif name in self._DESC_KEYS or prop in self._DESC_KEYS:
            if not self.description:
                self.description = content

    def handle_endtag(self, tag):
        if tag.lower() == "title":
            self._in_title = False

    def handle_data(self, data):
        if self._in_title:
            self._title += data
        for match in re.findall(r"#(\w[\w-]*)", data):
            cleaned = match.rstrip("-")
            if cleaned and cleaned not in self.hashtags:
                self.hashtags.append(cleaned)

    def effective_title(self) -> str:
        return self._title.strip() or self._og_title.strip() or self._tw_title.strip()


def _parse_html(body: bytes) -> Metadata:
    parser = _MetadataHTMLParser()
    try:
        parser.feed(body.decode("utf-8", errors="replace"))
    except Exception:
        # 파싱 오류는 안전하게 무시하고, 빈 메타데이터로 폴백(호출측에서 거부).
        pass
    return Metadata(
        title=parser.effective_title(),
        description=parser.description.strip(),
        hashtags=parser.hashtags,
    )


def _default_headers() -> dict[str, str]:
    return {
        "User-Agent": (
            "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) "
            "AppleWebKit/537.36 Chrome/127 Safari/537.36"
        ),
        "Accept": "text/html,application/xhtml+xml",
        "Accept-Language": "en-US,en;q=0.8",
    }


def fetch_metadata(
    raw_url: str,
    *,
    opener: Callable[[str, Mapping[str, str]], object] | None = None,
    resolver: Callable = socket.getaddrinfo,
) -> Metadata:
    """URL 을 검증하고 HTML 메타데이터를 안전하게 스트리밍 수집한다."""
    current = validate_metadata_url(raw_url, resolver)
    open_fn = opener if opener is not None else _make_default_opener(resolver)
    headers = _default_headers()
    redirects = 0
    resp: object | None = None
    try:
        while True:
            try:
                resp = open_fn(current, headers)
            except DownloadBadRequest as exc:
                raise MetadataFetchFailed("metadata fetch failed") from exc
            except Exception as exc:
                raise MetadataFetchFailed("metadata fetch failed") from exc

            status = getattr(resp, "status_code", 0)
            if status in _REDIRECT_STATUSES:
                location = resp.headers.get("location", "")
                resp.close()
                resp = None
                if not location or redirects >= _MAX_REDIRECTS:
                    raise MetadataFetchFailed("metadata redirect failed")
                redirects += 1
                current = validate_metadata_url(urljoin(current, location), resolver)
                continue

            if status >= 400:
                raise MetadataFetchFailed("metadata upstream failed")

            content_type = str(resp.headers.get("content-type", "")).lower()
            if not content_type.startswith("text/html"):
                raise MetadataFetchFailed("metadata content-type rejected")

            length = resp.headers.get("content-length", "")
            if length.isdigit() and int(length) > _MAX_BODY_BYTES:
                raise MetadataFetchFailed("metadata body too large")

            body = bytearray()
            for chunk in resp.iter_bytes():
                body.extend(chunk)
                if len(body) > _MAX_BODY_BYTES:
                    raise MetadataFetchFailed("metadata body too large")

            parsed = _parse_html(bytes(body))
            if not parsed.title and not parsed.description and not parsed.hashtags:
                raise MetadataFetchFailed("metadata body empty")
            return parsed
    finally:
        if resp is not None:
            try:
                resp.close()
            except Exception:
                pass
