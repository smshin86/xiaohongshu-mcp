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
