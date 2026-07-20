"""metadata.py 단위 테스트 — resolver/opener seam 으로 네트워크 없이 검증."""

import socket

import pytest

import metadata
from metadata import Metadata, MetadataFetchFailed, fetch_metadata, validate_metadata_url


HTML = (
    b"<html><head><title>Portable Fan</title>"
    b'<meta name="description" content="Best portable fans.">'
    b'<meta property="og:description" content="Cooling fan review.">'
    b"</head><body><p>#portablefan #fan #cooling</p></body></html>"
)


def public_resolver(host, port, type=socket.SOCK_STREAM):
    return [(socket.AF_INET, socket.SOCK_STREAM, 6, "", ("142.250.0.1", port))]


def private_resolver(host, port, type=socket.SOCK_STREAM):
    return [(socket.AF_INET, socket.SOCK_STREAM, 6, "", ("127.0.0.1", port))]


class FakeResp:
    """opener seam 반환 타입 — bytes 또는 list[bytes] 본문을 지원."""

    def __init__(self, status, headers, body):
        self.status_code = status
        self.headers = headers
        self._body = body
        self.close_count = 0

    def iter_bytes(self):
        if isinstance(self._body, (bytes, bytearray)):
            yield bytes(self._body)
        else:
            for chunk in self._body:
                yield chunk

    def close(self):
        self.close_count += 1


def test_validate_suffix_attack():
    bad = [
        "https://evilyoutube.com/x",
        "https://youtube.com.evil.com/x",
        "https://notyoutube.com/x",
    ]
    for url in bad:
        with pytest.raises(MetadataFetchFailed):
            validate_metadata_url(url, public_resolver)
    good = [
        "https://www.youtube.com/watch?v=1",
        "https://youtu.be/abc",
        "https://www.tiktok.com/@u/video/1",
        "https://www.instagram.com/p/abc",
    ]
    for url in good:
        assert validate_metadata_url(url, public_resolver) == url


def test_validate_rejects_http_userinfo_private_longurl():
    with pytest.raises(MetadataFetchFailed):  # http scheme
        validate_metadata_url("http://www.youtube.com/watch?v=1", public_resolver)
    with pytest.raises(MetadataFetchFailed):  # userinfo
        validate_metadata_url("https://user:pass@www.youtube.com/watch?v=1", public_resolver)
    with pytest.raises(MetadataFetchFailed):  # private DNS
        validate_metadata_url("https://www.youtube.com/watch?v=1", private_resolver)
    with pytest.raises(MetadataFetchFailed):  # 길이 > 2048
        validate_metadata_url("https://www.youtube.com/" + "a" * 2100, public_resolver)


def test_validate_empty_dns_raises_metadatafailed():
    def empty_resolver(host, port, type=socket.SOCK_STREAM):
        return []

    with pytest.raises(MetadataFetchFailed):
        validate_metadata_url("https://www.youtube.com/watch?v=1", empty_resolver)


def test_fetch_parses_title_desc_hashtags():
    def opener(url, headers):
        return FakeResp(200, {"content-type": "text/html"}, HTML)

    meta = fetch_metadata(
        "https://www.youtube.com/watch?v=1",
        opener=opener,
        resolver=public_resolver,
    )
    assert meta.title == "Portable Fan"
    assert meta.description == "Best portable fans."
    assert "portablefan" in meta.hashtags
    assert "fan" in meta.hashtags
    assert "cooling" in meta.hashtags


def test_fetch_allowed_redirect_validates_target():
    calls = []

    def opener(url, headers):
        calls.append(url)
        if url.endswith("x=1"):
            return FakeResp(200, {"content-type": "text/html"}, HTML)
        return FakeResp(302, {"location": "https://www.youtube.com/watch?v=1&x=1"}, b"")

    meta = fetch_metadata(
        "https://www.youtube.com/watch?v=1",
        opener=opener,
        resolver=public_resolver,
    )
    assert meta.title == "Portable Fan"
    assert len(calls) == 2


def test_fetch_private_redirect_blocked():
    def opener(url, headers):
        return FakeResp(302, {"location": "https://127.0.0.1/secret"}, b"")

    with pytest.raises(MetadataFetchFailed):
        fetch_metadata(
            "https://www.youtube.com/watch?v=1",
            opener=opener,
            resolver=public_resolver,
        )


def test_fetch_disallowed_redirect_blocked():
    def opener(url, headers):
        return FakeResp(302, {"location": "https://evil.com/x"}, b"")

    with pytest.raises(MetadataFetchFailed):
        fetch_metadata(
            "https://www.youtube.com/watch?v=1",
            opener=opener,
            resolver=public_resolver,
        )


def test_fetch_public_to_private_rebinding_blocked():
    calls = {"n": 0}

    def resolver(host, port, type=socket.SOCK_STREAM):
        calls["n"] += 1
        ip = "142.250.0.1" if calls["n"] == 1 else "127.0.0.1"
        return [(socket.AF_INET, socket.SOCK_STREAM, 6, "", (ip, port))]

    def opener(url, headers):
        if url.endswith("?next=1"):
            return FakeResp(200, {"content-type": "text/html"}, HTML)
        return FakeResp(302, {"location": "https://www.youtube.com/watch?v=1?next=1"}, b"")

    with pytest.raises(MetadataFetchFailed):
        fetch_metadata("https://www.youtube.com/watch?v=1", opener=opener, resolver=resolver)


def test_fetch_hop_limit():
    state = {"n": 0}

    def opener(url, headers):
        state["n"] += 1
        return FakeResp(302, {"location": f"https://www.youtube.com/x{state['n']}"}, b"")

    with pytest.raises(MetadataFetchFailed):
        fetch_metadata(
            "https://www.youtube.com/start",
            opener=opener,
            resolver=public_resolver,
        )
    # 6번째 redirect 에서 실패(opener 호출 6회)
    assert state["n"] == 6


def test_fetch_rejects_non_html_content_type():
    def opener(url, headers):
        return FakeResp(200, {"content-type": "application/json"}, b'{"x":1}')

    with pytest.raises(MetadataFetchFailed):
        fetch_metadata(
            "https://www.tiktok.com/@u/video/1",
            opener=opener,
            resolver=public_resolver,
        )


def test_fetch_rejects_oversize_content_length():
    def opener(url, headers):
        return FakeResp(
            200,
            {"content-type": "text/html", "content-length": "3000000"},
            b"",
        )

    with pytest.raises(MetadataFetchFailed):
        fetch_metadata(
            "https://www.tiktok.com/@u/video/1",
            opener=opener,
            resolver=public_resolver,
        )


def test_fetch_rejects_oversize_streamed_body():
    def opener(url, headers):
        return FakeResp(
            200,
            {"content-type": "text/html"},
            [b"<html>" + b"x" * (2 * 1024 * 1024 + 1)],
        )

    with pytest.raises(MetadataFetchFailed):
        fetch_metadata(
            "https://www.tiktok.com/@u/video/1",
            opener=opener,
            resolver=public_resolver,
        )
