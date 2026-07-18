# sign.py: a_bogus 서명 래퍼. Evil0ctal vendored abogus.py 의 업스트림 변동을 격리.

class Signer:
    """프로덕션 a_bogus 서명기. vendored abogus(verbatim) 의 ABogus 호출."""

    def sign(self, params: dict, user_agent: str) -> str:
        from abogus import ABogus  # Evil0ctal vendored(verbatim)
        # ABogus(platform=None) 생성자; get_value(url_params: dict|str, method="GET") -> str
        return ABogus().get_value(params)


class FakeSigner:
    """단위 테스트용 고정 서명(외부 의존 제거)."""

    def __init__(self, value: str = "fake_a_bogus"):
        self.value = value

    def sign(self, params: dict, user_agent: str) -> str:
        return self.value
