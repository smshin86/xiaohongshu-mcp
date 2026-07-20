// web/lib.js — 순수 로직(DOM 무의존). node:test 와 app.js 공유.
export const PLATFORMS = ["xiaohongshu", "douyin", "tiktok"];

export function initialState() {
  return {
    keyword: "",
    sort: "relevance",
    selected: new Set(["xiaohongshu", "douyin"]),
    items: [],
    pageCursors: { xiaohongshu: "", douyin: "", tiktok: "" },
    hasMore: { xiaohongshu: false, douyin: false, tiktok: false },
    lastReqPlatforms: [],
  };
}

// togglePlatform: 칩 토글. 최소 1개 유지(모두 해제 금지).
export function togglePlatform(state, name) {
  const next = new Set(state.selected);
  if (next.has(name)) {
    if (next.size === 1) return state;
    next.delete(name);
  } else {
    next.add(name);
  }
  return { ...state, selected: next };
}

export function platformsFromState(state) {
  return PLATFORMS.filter((p) => state.selected.has(p));
}

// '전체'는 3개를 모두 켠다. 이미 전체면 핵심 기본(XHS+Douyin)으로 되돌린다.
export function toggleAllPlatforms(state) {
  const allSelected = PLATFORMS.every((p) => state.selected.has(p));
  return { ...state, selected: new Set(allSelected ? ["xiaohongshu", "douyin"] : PLATFORMS) };
}

// 마지막 검색에 포함되지 않았지만 현재 켜진 플랫폼은 자동 검색하지 않고 pending 표시.
export function pendingPlatforms(state) {
  if (!state.lastReqPlatforms.length) return [];
  return platformsFromState(state).filter((p) => !state.lastReqPlatforms.includes(p));
}

// off 된 플랫폼은 서버 재검색 없이 현재 그리드에서 즉시 숨긴다.
export function visibleItems(state) {
  return state.items.filter((it) => state.selected.has(it.platform));
}

// selectLoadMorePlatforms: has_more 이고 직전 요청에 포함됐던 플랫폼만.
export function selectLoadMorePlatforms(state) {
  return PLATFORMS.filter(
    (p) => state.selected.has(p) && state.hasMore[p] && state.lastReqPlatforms.includes(p)
  );
}

// resetForNewSearch: 새 검색 시 커서/hasMore/아이템 초기화(칩 선택은 유지).
export function resetForNewSearch(state, keyword, sort) {
  return {
    ...initialState(),
    keyword,
    sort,
    selected: state.selected,
  };
}

// ingestItems: 서버가 머지+사후필터한 data.items 를 append. sides 는 status/cursor 용도만.
// 빈 next_cursor 는 커서 클리어(항상 반영). 중복은 dedupeKey 로 제거.
export function ingestItems(state, aggregated, requestedPlatforms) {
  const items = [...state.items, ...(aggregated.items || [])];
  const pageCursors = { ...state.pageCursors };
  const hasMore = { ...state.hasMore };
  for (const p of requestedPlatforms) {
    const side = aggregated.sides && aggregated.sides[p];
    if (!side) continue;
    pageCursors[p] = side.next_cursor || "";
    hasMore[p] = !!(side.has_more && side.next_cursor);
  }
  const seen = new Set();
  const deduped = [];
  for (const it of items) {
    const k = dedupeKey(it);
    if (seen.has(k)) continue;
    seen.add(k);
    deduped.push(it);
  }
  return { ...state, items: deduped, pageCursors, hasMore };
}

export function dedupeKey(it) {
  if (it.post_url) return `${it.platform}:${it.post_url}`;
  return `${it.platform}:${it.post_id}`;
}

// parseFilters: 폼 값 → filters JSON(body). video_only(bool), per_platform_limit(기본 15).
export function parseFilters(values) {
  const f = {};
  const split = (s) => (s || "").split(/[,\s]+/).filter(Boolean);
  const inc = split(values.include);
  const exc = split(values.exclude);
  if (inc.length) f.include_keywords = inc;
  if (exc.length) f.exclude_keywords = exc;
  if (values.date_from) f.date_from = values.date_from;
  if (values.date_to) f.date_to = values.date_to;
  if (values.duration_min) f.duration_min = Number(values.duration_min);
  if (values.duration_max) f.duration_max = Number(values.duration_max);
  if (values.min_likes) f.min_likes = Number(values.min_likes);
  if (values.min_comments) f.min_comments = Number(values.min_comments);
  if (values.min_favorites) f.min_favorites = Number(values.min_favorites);
  if (values.min_views) f.min_views = Number(values.min_views);
  f.video_only = true; // M1 은 영상 검색 전용
  const ppl = Number(values.per_platform_limit);
  f.per_platform_limit = ppl > 0 ? ppl : 15;
  return f;
}

// canDownload: 다운로드에 필요한 식별자가 모두 있는지. XHS 는 detail_token,
// Douyin/TikTok 은 video_url 이 추가로 필요하다. 없으면 버튼 disabled.
export function canDownload(item) {
  if (!item || !item.post_id) return false;
  if (item.platform === "xiaohongshu") return Boolean(item.detail_token);
  if (item.platform === "douyin" || item.platform === "tiktok") return Boolean(item.video_url);
  return false;
}

// buildDownloadURL: same-origin /api/v1/download anchor 용 query 문자열.
// XHS 는 platform/post_id/detail_token(원문 video_url 미포함 — 서명 노출 방지),
// Douyin/TikTok 은 platform/post_id/url=video_url 을 URLSearchParams 로 encode 한다.
// 식별자가 부족하면 빈 문자열을 반환한다. filename 은 선택.
export function buildDownloadURL(item, filename = "") {
  if (!canDownload(item)) return "";
  const params = new URLSearchParams();
  params.set("platform", item.platform);
  params.set("post_id", item.post_id);
  if (item.platform === "xiaohongshu") {
    params.set("detail_token", item.detail_token);
  } else {
    params.set("url", item.video_url);
  }
  if (filename) params.set("filename", filename);
  return "/api/v1/download?" + params.toString();
}

// hasXHSManualFallback: XHS 이고 post_url 이 있을 때만 Yinziai 수동 fallback 을 노출.
export function hasXHSManualFallback(item) {
  return Boolean(item) && item.platform === "xiaohongshu" && Boolean(item.post_url);
}

// ====== Task 5: 키워드 발견 헬퍼 (DOM 무의존) ======
// parseUrlList: ws/줄바꿈/쉼표 분할·trim·빈 제거·first-seen 중복 제거. **캡 없음**(UI 가 >3 검증).
export function parseUrlList(text) {
  const seen = new Set();
  const out = [];
  for (const raw of String(text || "").split(/[\s,]+/)) {
    const v = raw.trim();
    if (!v || seen.has(v)) continue;
    seen.add(v);
    out.push(v);
  }
  return out;
}

// isKorean: 한글 음절(가-힣)이 하나라도 포함되면 true.
export function isKorean(text) {
  return /[가-힣]/.test(String(text || ""));
}

// normalizeExtractResponse: extract API 응답 정규화. 실패 조건: !success, data 결측,
// candidates 비배열, 정규화 후 빈 배열. 성공 시 빈 keyword 제거·64자 절단·최대 8개.
export function normalizeExtractResponse(json) {
  const failed = { failed: true, candidates: [], note: "" };
  if (!json || !json.success || !json.data) return failed;
  const raw = json.data.candidates;
  if (!Array.isArray(raw)) return failed;
  const candidates = [];
  for (const c of raw) {
    if (!c) continue;
    const kw = String(c.keyword == null ? "" : c.keyword).trim();
    if (!kw) continue;
    candidates.push({
      keyword: kw.length > 64 ? kw.slice(0, 64) : kw,
      source_url: c.source_url || "",
      basis: c.basis || "",
      confidence: c.confidence,
    });
    if (candidates.length >= 8) break; // 캡 8
  }
  if (candidates.length === 0) return failed;
  return { failed: false, candidates, note: json.data.note || "" };
}

// normalizeTranslateResponse: translate API 응답 정규화. 빈 zh 제거·64자 절단·
// first-seen 중복 제거·최대 5개. 정규화 후 빈 배열은 failed.
export function normalizeTranslateResponse(json) {
  const failed = { failed: true, candidates: [] };
  if (!json || !json.success || !json.data) return failed;
  const raw = json.data.candidates;
  if (!Array.isArray(raw)) return failed;
  const seen = new Set();
  const candidates = [];
  for (const c of raw) {
    if (!c) continue;
    const zh = String(c.zh == null ? "" : c.zh).trim();
    if (!zh || seen.has(zh)) continue;
    seen.add(zh);
    candidates.push({ zh: zh.length > 64 ? zh.slice(0, 64) : zh });
    if (candidates.length >= 5) break; // 캡 5
  }
  if (candidates.length === 0) return failed;
  return { failed: false, candidates };
}

// basisLabel: extract 후보의 basis 라벨(i18n). 정의되지 않은 값/빈 값은 메타.
export function basisLabel(basis) {
  switch (basis) {
    case "title": return "제목";
    case "hashtag": return "해시태그";
    case "description": return "설명";
    case "metadata": return "메타";
    default: return "메타";
  }
}

// parseGoDurationMs: Go duration 문자열("4m0s", "90s", "1h30m") → ms.
// QR 응답의 timeout 필드(서버 loginWait) 를 폴링 상한으로 변환. 파싱 불가/빈 값 → 0.
export function parseGoDurationMs(s) {
  if (typeof s !== "string" || !s) return 0;
  const units = { ms: 1, s: 1000, m: 60000, h: 3600000 };
  const re = /(\d+)(ms|s|m|h)/g;
  let total = 0;
  let matched = false;
  let m;
  while ((m = re.exec(s)) !== null) {
    matched = true;
    total += Number(m[1]) * units[m[2]];
  }
  return matched ? total : 0;
}

// loginPollDecision: 로그인 폴링 결정(순수). 반환: "logged_in" | "expired" | "poll".
// QR timeout(deadline) 경과 시 "expired" 로 폴링을 중단해 무한 폴링을 막는다.
// deadline<=0(알 수 없음) 이면 로그인 전까지 계속 poll — 호출자가 상한을 보장해야 한다.
export function loginPollDecision({ isLoggedIn, now, deadline }) {
  if (isLoggedIn) return "logged_in";
  if (deadline > 0 && now >= deadline) return "expired";
  return "poll";
}
