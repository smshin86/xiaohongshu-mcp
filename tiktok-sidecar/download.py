"""Douyin/TikTok 영상 다운로드 스트림.

Go 서버가 사용자 대면 SSRF 경계를 담당하지만, sidecar가 redirect를 직접 따라가므로
각 hop의 URL과 DNS도 여기서 다시 검증한다. 응답 body는 파일이나 메모리에 모으지 않는다.
"""

from __future__ import annotations

import ipaddress
import os
import socket
from dataclasses import dataclass
from threading import Lock
from typing import Callable, Iterable
from urllib.parse import urljoin, urlsplit

import httpx


class DownloadBadRequest(Exception):
    """요청 URL/platform이 안전 정책을 위반함."""


class DownloadForbidden(Exception):
    """원본이 접근을 거부함."""


class DownloadUpstreamFailed(Exception):
    """원본 네트워크/상태 오류."""


class DownloadTimedOut(Exception):
    """원본 응답 시간 초과."""


_ALLOWED_HOSTS = {
    "douyin": ("douyin.com", "douyinvod.com", "douyinpic.com", "byteimg.com", "bytecdn.cn"),
    "tiktok": ("tiktok.com", "tiktokcdn.com", "tiktokv.com", "byteoversea.com", "ibytedtos.com"),
}
_REDIRECT_STATUSES = {301, 302, 303, 307, 308}
_MAX_REDIRECTS = 5


def _host_matches(host: str, suffixes: Iterable[str]) -> bool:
    host = host.lower().rstrip(".")
    return any(host == suffix or host.endswith("." + suffix) for suffix in suffixes)


def _resolve_public(host: str, port: int, resolver: Callable = socket.getaddrinfo) -> None:
    try:
        answers = resolver(host, port, type=socket.SOCK_STREAM)
    except (OSError, socket.gaierror) as exc:
        raise DownloadBadRequest("download host resolution failed") from exc
    if not answers:
        raise DownloadBadRequest("download host resolution failed")
    for answer in answers:
        try:
            ip = ipaddress.ip_address(answer[4][0])
        except (IndexError, ValueError) as exc:
            raise DownloadBadRequest("download host resolution failed") from exc
        if not ip.is_global:
            raise DownloadBadRequest("download host is not public")


def validate_download_url(platform: str, raw_url: str, resolver: Callable = socket.getaddrinfo) -> str:
    """https/platform host/public DNS만 허용하고 원문 URL을 반환한다."""

    suffixes = _ALLOWED_HOSTS.get(platform)
    if suffixes is None:
        raise DownloadBadRequest("unsupported download platform")
    try:
        parsed = urlsplit(raw_url)
        port = parsed.port or 443
    except ValueError as exc:
        raise DownloadBadRequest("invalid download url") from exc
    if parsed.scheme.lower() != "https" or not parsed.hostname:
        raise DownloadBadRequest("invalid download url")
    if parsed.username is not None or parsed.password is not None:
        raise DownloadBadRequest("invalid download url")
    host = parsed.hostname.lower()
    if not _host_matches(host, suffixes):
        raise DownloadBadRequest("download host is not allowed")
    _resolve_public(host, port, resolver)
    return raw_url


@dataclass
class DownloadStream:
    """열린 upstream과 client의 lifetime을 StreamingResponse까지 유지한다."""

    client: object
    response: object

    def __post_init__(self) -> None:
        self._closed = False
        self._close_lock = Lock()

    @property
    def content_type(self) -> str:
        value = str(getattr(self.response, "headers", {}).get("content-type", ""))
        return value if value.lower().startswith("video/") else "video/mp4"

    def iter_bytes(self):
        yield from self.response.iter_bytes()

    def close(self) -> None:
        with self._close_lock:
            if self._closed:
                return
            self._closed = True
        try:
            self.response.close()
        finally:
            self.client.close()


def _request_headers(platform: str) -> dict[str, str]:
    headers = {
        "User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/127 Safari/537.36",
        "Referer": "https://www.douyin.com/" if platform == "douyin" else "https://www.tiktok.com/",
    }
    if platform == "douyin":
        cookie = os.getenv("DOUYIN_COOKIE", "")
        if cookie:
            headers["Cookie"] = cookie
    return headers


def _close_quietly(value: object) -> None:
    try:
        value.close()
    except Exception:
        pass


def open_download(
    platform: str,
    raw_url: str,
    client_factory: Callable = httpx.Client,
    resolver: Callable = socket.getaddrinfo,
) -> DownloadStream:
    """upstream header를 먼저 열고 검증한 뒤 열린 chunk stream을 반환한다."""

    current = validate_download_url(platform, raw_url, resolver)
    try:
        client = client_factory(
            follow_redirects=False,
            timeout=httpx.Timeout(300.0, connect=20.0),
            trust_env=False,
        )
    except Exception as exc:
        raise DownloadUpstreamFailed("download upstream unavailable") from exc

    redirects = 0
    try:
        while True:
            try:
                request = client.build_request("GET", current, headers=_request_headers(platform))
                response = client.send(request, stream=True)
            except httpx.TimeoutException as exc:
                raise DownloadTimedOut("download upstream timed out") from exc
            except httpx.HTTPError as exc:
                raise DownloadUpstreamFailed("download upstream unavailable") from exc

            if response.status_code in _REDIRECT_STATUSES:
                location = response.headers.get("location", "")
                _close_quietly(response)
                if not location or redirects >= _MAX_REDIRECTS:
                    raise DownloadUpstreamFailed("download redirect failed")
                redirects += 1
                current = validate_download_url(platform, urljoin(current, location), resolver)
                continue
            if response.status_code == 403:
                _close_quietly(response)
                raise DownloadForbidden("download upstream forbidden")
            if response.status_code >= 400:
                _close_quietly(response)
                raise DownloadUpstreamFailed("download upstream failed")
            return DownloadStream(client=client, response=response)
    except Exception:
        _close_quietly(client)
        raise
