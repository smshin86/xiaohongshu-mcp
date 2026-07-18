// web/app.js — DOM wiring(검색 전용). 순수 로직은 lib.js(import). 로그인은 settings.js.
import {
  PLATFORMS, initialState, togglePlatform, toggleAllPlatforms, platformsFromState,
  pendingPlatforms, visibleItems, selectLoadMorePlatforms,
  resetForNewSearch, ingestItems, parseFilters,
} from "./lib.js";

// ====== DOM 헬퍼 ======
const $ = (id) => document.getElementById(id); // bare id ('#' 없음)
const show = (el) => el && el.classList.remove("hidden");
const hide = (el) => el && el.classList.add("hidden");
function escapeHtml(s) {
  return String(s == null ? "" : s).replace(/[&<>"']/g, (c) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[c]));
}
function setStatus(msg, spinner) {
  // DOM API 로 조립: 서버 문자열(msg)이 HTML 로 해석되지 않도록 text node 사용
  const el = $("status");
  el.replaceChildren();
  if (spinner) {
    const s = document.createElement("span");
    s.className = "spinner";
    el.appendChild(s);
  }
  el.appendChild(document.createTextNode(msg || ""));
}

// ====== 상태 ======
let state = initialState();
let capabilities = null; // GET /api/v1/search/capabilities 결과
let currentItem = null;  // 모달에 띄운 아이템(post_url 포함; 재생실패 대체용)
let currentVideoURL = ""; // detail 로 해결한 URL 포함, modal/copy 의 단일 기준
let lastSearchData = null; // 토글 시 side/pending 상태 재렌더용(탭 메모리만)
let fallbackFired = false; // 모달 fallback(onerror/play.catch) 중복 실행 가드

// ====== API 클라이언트 (같은 출처) ======
const api = {
  async capabilities() { const r = await fetch("/api/v1/search/capabilities"); return r.json(); },
  async search(body) {
    const r = await fetch("/api/v1/search", {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body),
    });
    return r.json();
  },
  async feedDetail(feedId, xsecToken) {
    const r = await fetch("/api/v1/feeds/detail", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ feed_id: feedId, xsec_token: xsecToken, load_all_comments: false }),
    });
    return r.json();
  },
};

// ====== 필터 값 수집 → filters ======
function collectFilters() {
  return parseFilters({
    include: $("f-include").value,
    exclude: $("f-exclude").value,
    date_from: $("f-date-from").value,
    date_to: $("f-date-to").value,
    duration_min: $("f-duration-min").value,
    duration_max: $("f-duration-max").value,
    min_likes: $("f-min-likes").value,
    min_comments: $("f-min-comments").value,
    min_favorites: $("f-min-favorites").value,
    min_views: $("f-min-views").value,
    per_platform_limit: $("f-per-platform-limit").value,
    video_only: $("f-video-only").checked,
  });
}

// ====== 플랫폼 칩 → state 반영 + capability 기반 필터 비활성화 ======
function syncChipsFromState() {
  document.querySelectorAll("#platform-chips input[data-platform]").forEach((cb) => {
    cb.checked = state.selected.has(cb.dataset.platform);
  });
  const all = $("platform-all");
  if (all) all.setAttribute("aria-pressed", String(PLATFORMS.every((p) => state.selected.has(p))));
}
function applyCapabilityDisabling() {
  if (!(capabilities && capabilities.platforms)) return;
  const sel = platformsFromState(state);
  const hasMetric = (p, key) => (capabilities.platforms[p]?.metrics || []).includes(key);
  const hasFilter = (p, key) => {
    const c = capabilities.platforms[p] || {};
    return [...(c.native_filters || []), ...(c.post_filters || [])].includes(key);
  };
  const toggle = (id, disable) => { const el = $(id); if (el) el.disabled = disable; };
  toggle("f-min-views", sel.every((p) => !hasMetric(p, "views")));
  toggle("f-duration-min", sel.every((p) => !hasFilter(p, "duration_min")));
  toggle("f-duration-max", sel.every((p) => !hasFilter(p, "duration_max")));
  toggle("f-date-to", sel.every((p) => !hasFilter(p, "date_to")));
  toggle("f-date-from", sel.every((p) => !hasFilter(p, "date_from")));
}

// ====== 사이드 상태 패널(status/cursor 용 sides 만 사용) ======
function renderSideStatus(data) {
  const ul = $("side-status");
  const sides = (data && data.sides) || {};
  const labels = { xiaohongshu: "XHS", douyin: "Douyin", tiktok: "TikTok" };
  const rows = PLATFORMS.map((p) => {
    const s = sides[p];
    if (!s) return "";
    const label = labels[p];
    if (!s.available || !s.available.available) {
      const reason = (s.available && s.available.reason) || s.error || "준비 중";
      return `<li class="side-unavailable"><b>${label}</b>: ${escapeHtml(reason)}</li>`;
    }
    return `<li class="side-ok"><b>${label}</b>: (더 보기 ${s.has_more ? "가능" : "불가"})</li>`;
  }).filter(Boolean);
  const pending = pendingPlatforms(state).map(
    (p) => `<li class="side-pending"><b>${labels[p]}</b>: 검색 실행 필요</li>`
  );
  ul.innerHTML = [...rows, ...pending].join("");
}

// ====== 카드 렌더 (data-idx 로 조회, escape 불필요) ======
function metric(v, prefix) { return v == null ? "" : `${prefix} ${Number(v).toLocaleString()}`; }
function cardHTML(it, idx) {
  const meta = [
    metric(it.likes, "❤"), metric(it.comments, "💬"), metric(it.favorites, "🔖"),
    metric(it.views, "👁"), metric(it.shares, "↗"),
  ].filter(Boolean).join(" · ");
  const cover = it.thumbnail_url
    ? `<img class="card-cover" src="${escapeHtml(it.thumbnail_url)}" alt="" loading="lazy">`
    : `<div class="card-cover"></div>`;
  const badge = it.platform === "xiaohongshu" ? "XHS" : it.platform === "douyin" ? "Douyin" : "TikTok";
  return `
    <article class="card" data-idx="${idx}">
      <div style="position:relative">
        ${cover}
        <span class="badge-platform">${badge}</span>
      </div>
      <div class="card-body">
        <p class="card-title">${escapeHtml(it.title)}</p>
        <div class="card-meta"><span>${escapeHtml(it.author)}</span><span>${meta}</span></div>
      </div>
    </article>`;
}

function renderResults() {
  const grid = $("results");
  const items = visibleItems(state);
  if (!items.length) {
    grid.innerHTML = "";
    setStatus("검색 결과가 없습니다.");
    hide($("more-btn"));
    return;
  }
  setStatus(`영상 결과 ${items.length}개`);
  grid.innerHTML = items.map((it) => cardHTML(it, state.items.indexOf(it))).join("");
  if (selectLoadMorePlatforms(state).length > 0) show($("more-btn"));
  else hide($("more-btn"));
}

// ====== 검색 실행 ======
async function runSearch(isMore) {
  const platforms = isMore ? selectLoadMorePlatforms(state) : platformsFromState(state);
  if (platforms.length === 0) {
    setStatus("최소 한 개의 플랫폼을 선택하세요.");
    return;
  }
  const pageCursors = {};
  for (const p of platforms) pageCursors[p] = state.pageCursors[p] || "";
  const body = {
    keyword: state.keyword,
    platforms,
    sort: state.sort,
    page_cursors: pageCursors,
    filters: collectFilters(),
  };
  state.lastReqPlatforms = platforms;
  setStatus(isMore ? "더 불러오는 중…" : "검색 중…", true);
  try {
    const res = await api.search(body);
    if (!res.success) {
      setStatus("오류: " + (res.message || ""));
      return;
    }
    state = ingestItems(state, res.data, platforms);
    lastSearchData = res.data;
    renderSideStatus(res.data);
    renderResults();
  } catch (e) {
    setStatus("서버에 연결할 수 없습니다. 잠시 후 다시 시도해주세요.");
  }
}

// ====== 검색 폼 제출 (입력값을 먼저 읽고 state.keyword 검증) ======
function onSearchSubmit(ev) {
  ev.preventDefault();
  const kw = $("keyword").value.trim(); // 입력값을 먼저 읽는다
  if (!kw) {
    setStatus("검색어를 입력해 주세요.");
    return;
  }
  state = resetForNewSearch(state, kw, $("sort").value); // 커서/hasMore/아이템 리셋
  runSearch(false);
}

// ====== 영상 재생 + 원본 보기(재생 실패 대체) ======
async function playItem(idx) {
  const it = state.items[idx];
  if (!it) return;
  if (it.video_url) { openVideoModal(it, it.video_url); return; } // Douyin/TikTok
  if (!it.needs_detail) {
    if (it.post_url) window.open(it.post_url, "_blank", "noopener");
    else setStatus("재생하거나 열 수 있는 URL 이 없습니다.");
    return;
  }
  setStatus("영상 불러오는 중…", true);
  try {
    const res = await api.feedDetail(it.post_id, it.detail_token);
    if (!res.success || !(res.data && res.data.video_url)) {
      if (it.post_url) window.open(it.post_url, "_blank", "noopener");
      else setStatus("영상 URL 을 가져오지 못했습니다.");
      return;
    }
    openVideoModal(it, res.data.video_url);
    setStatus("");
  } catch (e) {
    if (it.post_url) window.open(it.post_url, "_blank", "noopener");
    else setStatus("영상을 불러오는 중 오류가 발생했습니다.");
  }
}
function openVideoModal(it, url) {
  currentItem = it;
  currentVideoURL = url;
  const v = $("video-player");
  v.src = url;
  // 원본 링크 항상 세팅(post_url 없으면 숨김). 재생 실패 시 대체 경로.
  const link = $("original-link");
  if (it && it.post_url) { link.href = it.post_url; show(link); }
  else { link.removeAttribute("href"); hide(link); }
  show($("video-modal"));
  // 재생 실패(error 이벤트) → 원본 페이지로 대체 이동.
  // onerror 와 play().catch() 가 모두 발생할 수 있으므로 fallbackFired 로 한 번만 연다.
  v.onended = null;
  v.onerror = () => {
    if (fallbackFired) return;
    fallbackFired = true;
    if (it && it.post_url) { window.open(it.post_url, "_blank", "noopener"); closeVideoModal(); }
  };
  v.play().catch(() => {
    if (fallbackFired) return;
    fallbackFired = true;
    if (it && it.post_url) window.open(it.post_url, "_blank", "noopener");
  });
}
function closeVideoModal() {
  const v = $("video-player");
  v.pause(); v.removeAttribute("src"); v.onerror = null; v.load();
  currentVideoURL = "";
  currentItem = null;
  fallbackFired = false; // 다음 모달 오픈을 위해 가드 리셋
  hide($("video-modal"));
}
async function copyVideoUrl() {
  const url = currentVideoURL || (currentItem && currentItem.post_url) || "";
  if (!url) return;
  try {
    await navigator.clipboard.writeText(url);
    $("copy-url-btn").textContent = "복사됨 ✓";
    setTimeout(() => ($("copy-url-btn").textContent = "URL 복사"), 1500);
  } catch (e) {
    prompt("이 URL 을 복사하세요:", url);
  }
}

// ====== 초기화 (로그인 없음 — 검색 UI 만) ======
async function init() {
  try {
    const cap = await api.capabilities();
    if (cap && cap.success) capabilities = cap.data;
  } catch (e) { /* 사이드카 미가동 시 무방 */ }

  syncChipsFromState();
  applyCapabilityDisabling();

  $("search-form").addEventListener("submit", onSearchSubmit);
  $("more-btn").addEventListener("click", () => runSearch(true));
  $("platform-chips").addEventListener("change", (ev) => {
    const cb = ev.target.closest("input[data-platform]");
    if (!cb) return;
    state = togglePlatform(state, cb.dataset.platform);
    syncChipsFromState();
    applyCapabilityDisabling();
    renderResults();                 // off 즉시 로컬 hide
    renderSideStatus(lastSearchData); // 새로 on 된 플랫폼은 '검색 실행 필요'
  });
  $("platform-all").addEventListener("click", () => {
    state = toggleAllPlatforms(state);
    syncChipsFromState();
    applyCapabilityDisabling();
    renderResults();
    renderSideStatus(lastSearchData);
  });
  $("results").addEventListener("click", (ev) => {
    const card = ev.target.closest(".card");
    if (!card) return;
    playItem(Number(card.getAttribute("data-idx"))); // data-idx 로 조회(escape 불필요)
  });
  $("modal-close").addEventListener("click", closeVideoModal);
  $("copy-url-btn").addEventListener("click", copyVideoUrl);
  $("video-modal").addEventListener("click", (ev) => {
    if (ev.target === $("video-modal")) closeVideoModal();
  });
  document.addEventListener("keydown", (ev) => { if (ev.key === "Escape") closeVideoModal(); });
}

document.addEventListener("DOMContentLoaded", init);
