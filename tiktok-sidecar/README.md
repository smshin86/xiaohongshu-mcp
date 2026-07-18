# video-search-sidecar

Go 메인 서버(127.0.0.1:18060) 전용 Douyin/TikTok 검색 사이드카(127.0.0.1:18061, 외부 미공개).

## 실행
```bash
python -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
playwright install chromium   # TikTokApi 7.x 세션 생성에 필요
export DOUYIN_COOKIE='ttwid=...; sessionid=...'   # Douyin(선택)
export TT_MSTOKEN='...'        # TikTok(선택, 갱신 필요)
uvicorn app:app --host 127.0.0.1 --port 18061
```

## 엔드포인트
- GET /healthz → {status:"ok", douyin, tiktok, llm} bool(평문 비밀 미포함)
- POST /search (body: {platform,q,count,sort,cursor,filters}) → {success:true,data:{videos,next_cursor,has_more}}

## 비밀
DOUYIN_COOKIE/TT_MSTOKEN 은 본 프로세스 env 에만 존재. Go 는 /healthz bool 만 읽음.

## 포함된 서드파티 코드 / 라이선스
- `tiktok-sidecar/abogus.py`: Douyin Web API 의 `a_bogus` 파라미터 생성 코드. 원저작자 **JoeanAmier/TikTokDownloader** 의 **GPLv3** 코드를 **Evil0ctal** 이 수정한 버전(`Evil0ctal/Douyin_TikTok_Download_API`).
  - upstream 경로: `crawlers/douyin/web/abogus.py`, commit `42784ffc83a72a516bfe952153ad7e2a3998d16c`.
  - 원문 그대로(verbatim) 보존. 파일 상단 헤더에 GPLv3 라이선스 및 원저작자 귀속 표기 유지.
