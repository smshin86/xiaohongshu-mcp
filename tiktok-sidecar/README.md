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
