import httpx
import pytest

import download


def public_resolver(host, port, type=None):
    return [(2, type or 1, 6, "", ("93.184.216.34", port))]


def private_resolver(host, port, type=None):
    return [(2, type or 1, 6, "", ("127.0.0.1", port))]


class FakeResponse:
    def __init__(self, status=200, headers=None, chunks=None):
        self.status_code = status
        self.headers = headers or {}
        self._chunks = chunks or [b"a", b"b"]
        self.close_count = 0

    def iter_bytes(self):
        yield from self._chunks

    def close(self):
        self.close_count += 1


class FakeClient:
    def __init__(self, outcomes):
        self.outcomes = list(outcomes)
        self.requests = []
        self.close_count = 0

    def build_request(self, method, url, headers=None):
        return {"method": method, "url": url, "headers": headers or {}}

    def send(self, request, stream=False):
        self.requests.append((request, stream))
        outcome = self.outcomes.pop(0)
        if isinstance(outcome, Exception):
            raise outcome
        return outcome

    def close(self):
        self.close_count += 1


def factory_for(fake):
    def factory(**kwargs):
        assert kwargs["follow_redirects"] is False
        assert kwargs["trust_env"] is False
        return fake
    return factory


@pytest.mark.parametrize(
    "platform,url",
    [
        ("douyin", "https://v.douyinvod.com/video.mp4"),
        ("tiktok", "https://V16.TIKTOKCDN.COM/video.mp4"),
    ],
)
def test_validate_download_url_accepts_platform_cdn(platform, url):
    assert download.validate_download_url(platform, url, public_resolver) == url


@pytest.mark.parametrize(
    "platform,url,resolver",
    [
        ("youtube", "https://www.tiktok.com/v", public_resolver),
        ("tiktok", "http://www.tiktok.com/v", public_resolver),
        ("tiktok", "https://tiktok.com.evil.test/v", public_resolver),
        ("douyin", "https://evildouyin.com/v", public_resolver),
        ("douyin", "https://www.douyin.com/v", private_resolver),
        ("tiktok", "https://user:pass@www.tiktok.com/v", public_resolver),
        ("tiktok", "https://www.tiktok.com/v", lambda *args, **kwargs: []),
    ],
)
def test_validate_download_url_rejects_unsafe_input(platform, url, resolver):
    with pytest.raises(download.DownloadBadRequest) as exc:
        download.validate_download_url(platform, url, resolver)
    assert "token=secret" not in str(exc.value)


def test_guarded_backend_rechecks_dns_at_dial_and_rejects_rebinding():
    url = "https://v.tiktokcdn.com/video"
    assert download.validate_download_url("tiktok", url, public_resolver) == url

    backend = download.GuardedNetworkBackend(private_resolver)
    with pytest.raises(download.DownloadBadRequest):
        backend.connect_tcp("v.tiktokcdn.com", 443)


def test_guarded_backend_dials_the_validated_public_ip():
    calls = []

    class FakeBackend:
        def connect_tcp(self, host, port, **kwargs):
            calls.append((host, port, kwargs))
            return object()

        def sleep(self, seconds):
            pass

    backend = download.GuardedNetworkBackend(public_resolver)
    backend._backend = FakeBackend()
    stream = backend.connect_tcp("v.tiktokcdn.com", 443, timeout=2.0)

    assert stream is not None
    assert calls[0][0:2] == ("93.184.216.34", 443)
    assert calls[0][2]["timeout"] == 2.0


def test_default_client_uses_guarded_network_backend():
    client = download._new_guarded_client(public_resolver)
    try:
        assert isinstance(
            client._transport._pool._network_backend,
            download.GuardedNetworkBackend,
        )
    finally:
        client.close()


def test_open_download_streams_chunks_and_closes_once(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    response = FakeResponse(headers={"content-type": "video/webm"}, chunks=[b"one", b"two"])
    client = FakeClient([response])

    stream = download.open_download(
        "tiktok",
        "https://v.tiktokcdn.com/video",
        client_factory=factory_for(client),
        resolver=public_resolver,
    )

    assert list(stream.iter_bytes()) == [b"one", b"two"]
    assert stream.content_type == "video/webm"
    assert list(tmp_path.iterdir()) == []
    stream.close()
    stream.close()
    assert response.close_count == 1
    assert client.close_count == 1


def test_open_download_follows_allowed_redirect_and_revalidates():
    redirect = FakeResponse(302, {"location": "https://v2.tiktokcdn.com/final"})
    final = FakeResponse(200, {"content-type": "application/octet-stream"}, [b"video"])
    client = FakeClient([redirect, final])

    stream = download.open_download(
        "tiktok",
        "https://v1.tiktokcdn.com/start",
        client_factory=factory_for(client),
        resolver=public_resolver,
    )

    assert len(client.requests) == 2
    assert redirect.close_count == 1
    assert stream.content_type == "video/mp4"
    stream.close()


def test_open_download_rejects_private_redirect():
    redirect = FakeResponse(302, {"location": "https://private.tiktokcdn.com/final"})
    client = FakeClient([redirect])

    def resolver(host, port, type=None):
        if host == "private.tiktokcdn.com":
            return private_resolver(host, port, type)
        return public_resolver(host, port, type)

    with pytest.raises(download.DownloadBadRequest):
        download.open_download(
            "tiktok",
            "https://v1.tiktokcdn.com/start",
            client_factory=factory_for(client),
            resolver=resolver,
        )
    assert redirect.close_count == 1
    assert client.close_count == 1


def test_open_download_rejects_sixth_redirect():
    redirects = [
        FakeResponse(302, {"location": f"https://v{i}.tiktokcdn.com/next"})
        for i in range(1, 7)
    ]
    client = FakeClient(redirects)

    with pytest.raises(download.DownloadUpstreamFailed):
        download.open_download(
            "tiktok",
            "https://v0.tiktokcdn.com/start",
            client_factory=factory_for(client),
            resolver=public_resolver,
        )

    assert len(client.requests) == 6
    assert client.close_count == 1


@pytest.mark.parametrize(
    "outcome,error_type",
    [
        (FakeResponse(403), download.DownloadForbidden),
        (FakeResponse(500), download.DownloadUpstreamFailed),
        (httpx.ReadTimeout("slow"), download.DownloadTimedOut),
        (httpx.ConnectError("down"), download.DownloadUpstreamFailed),
    ],
)
def test_open_download_maps_upstream_errors(outcome, error_type):
    client = FakeClient([outcome])
    with pytest.raises(error_type) as exc:
        download.open_download(
            "douyin",
            "https://v.douyinvod.com/video",
            client_factory=factory_for(client),
            resolver=public_resolver,
        )
    assert "https://" not in str(exc.value)
    assert client.close_count == 1


class RouteStream:
    content_type = "video/mp4"

    def __init__(self):
        self.close_count = 0

    def iter_bytes(self):
        yield b"left"
        yield b"right"

    def close(self):
        self.close_count += 1


def test_download_route_streams_and_runs_background_close(client, monkeypatch):
    import app as app_module

    stream = RouteStream()
    monkeypatch.setattr(app_module, "open_download", lambda platform, url: stream)
    response = client.get(
        "/download",
        params={"platform": "douyin", "url": "https://v.douyinvod.com/video"},
    )

    assert response.status_code == 200
    assert response.content == b"leftright"
    assert response.headers["content-type"].startswith("video/mp4")
    assert "attachment" in response.headers["content-disposition"]
    assert stream.close_count == 1


@pytest.mark.parametrize(
    "error,status",
    [
        (download.DownloadBadRequest(), 400),
        (download.DownloadForbidden(), 403),
        (download.DownloadUpstreamFailed(), 502),
        (download.DownloadTimedOut(), 504),
    ],
)
def test_download_route_maps_errors_without_details(client, monkeypatch, error, status):
    import app as app_module

    def fail(platform, url):
        raise error

    monkeypatch.setattr(app_module, "open_download", fail)
    response = client.get(
        "/download",
        params={"platform": "tiktok", "url": "https://v.tiktokcdn.com/v?token=secret"},
    )
    assert response.status_code == status
    assert response.json() == {"success": False, "data": {}}
    assert "secret" not in response.text
