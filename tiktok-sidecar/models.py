from pydantic import BaseModel, Field

class SearchFilters(BaseModel):
    include_keywords: list[str] = Field(default_factory=list)
    exclude_keywords: list[str] = Field(default_factory=list)
    date_from: str = ""
    date_to: str = ""
    duration_min: int = 0
    duration_max: int = 0
    min_likes: int = 0
    min_comments: int = 0
    min_favorites: int = 0
    min_views: int = 0
    video_only: bool = False
    per_platform_limit: int = 0            # 0=미지정(상한 없음)

class SearchRequest(BaseModel):
    platform: str                          # douyin|tiktok (body, query 사용 금지)
    q: str
    count: int = 15
    sort: str = "relevance"
    cursor: str = ""
    filters: SearchFilters = Field(default_factory=SearchFilters)

# VideoItem 은 dict 로 직접 구성(Go sidecarVideo 와 동일 스키마). 별도 모델 불필요.

# M3: 키워드 추출/번역 요청 모델(additive).
class ExtractRequest(BaseModel):
    urls: list[str]                        # 참고 URL(≤3). route 가 trim/dedupe/길이 검사.

class TranslateRequest(BaseModel):
    text: str                              # 번역 원문. route 가 trim/길이(≤200) 검사.
    source_lang: str = "ko"                # 현재 "ko" 만 지원.
