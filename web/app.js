// ====== API 클라이언트 (같은 출처) ======
const api = {
  async loginStatus() {
    const r = await fetch("/api/v1/login/status");
    return r.json();
  },
  async loginQrcode() {
    const r = await fetch("/api/v1/login/qrcode");
    return r.json();
  },
  async search(keyword) {
    const r = await fetch("/api/v1/feeds/search", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ keyword, filters: { note_type: "视频" } }),
    });
    return r.json();
  },
  async feedDetail(feedId, xsecToken) {
    const r = await fetch("/api/v1/feeds/detail", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        feed_id: feedId,
        xsec_token: xsecToken,
        load_all_comments: false,
      }),
    });
    return r.json();
  },
};

// ====== 상태 ======
const state = { loggedIn: false, username: "", loading: false };

// ====== DOM 헬퍼 ======
const $ = (id) => document.getElementById(id);
const show = (el) => el && el.classList.remove("hidden");
const hide = (el) => el && el.classList.add("hidden");

// ====== 상태 메시지 ======
function setStatus(msg, spinner) {
  const el = $("status");
  el.innerHTML = (spinner ? '<span class="spinner"></span>' : "") + (msg || "");
}

// ====== Feed 헬퍼 ======
const isVideo = (feed) => feed && feed.noteCard && feed.noteCard.type === "video";
const pickCover = (feed) => {
  const c = feed.noteCard.cover || {};
  return c.urlDefault || c.urlPre || "";
};
const pickNickname = (feed) => {
  const u = feed.noteCard.user || {};
  return u.nickname || u.nickName || "";
};

// ====== 카드 렌더 ======
function cardHTML(feed) {
  const i = feed.noteCard.interactInfo || {};
  const meta = [
    `❤ ${i.likedCount || 0}`,
    `💬 ${i.commentCount || 0}`,
    `🔖 ${i.collectedCount || 0}`,
  ].join(" · ");
  const cover = pickCover(feed);
  const nick = pickNickname(feed);
  return `
    <article class="card" data-id="${feed.id}" data-token="${feed.xsecToken || ""}">
      <div style="position:relative">
        ${cover ? `<img class="card-cover" src="${cover}" alt="" loading="lazy">` : `<div class="card-cover"></div>`}
        <span class="badge-video">▶ 영상</span>
      </div>
      <div class="card-body">
        <p class="card-title">${escapeHtml(feed.noteCard.displayTitle || "")}</p>
        <div class="card-meta"><span>${escapeHtml(nick)}</span><span>${meta}</span></div>
      </div>
    </article>`;
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[c]));
}

function renderCards(feeds) {
  const videos = (feeds || []).filter(isVideo);
  const grid = $("results");
  if (videos.length === 0) {
    grid.innerHTML = "";
    setStatus("검색 결과가 없습니다.");
    return;
  }
  setStatus(`영상 결과 ${videos.length}개`);
  grid.innerHTML = videos.map(cardHTML).join("");
}

// ====== 검색 ======
async function doSearch(keyword) {
  state.loading = true;
  setStatus("검색 중…", true);
  try {
    const res = await api.search(keyword);
    if (!res.success) {
      setStatus("오류가 발생했습니다: " + (res.message || ""));
      return;
    }
    renderCards(res.data && res.data.feeds);
  } catch (e) {
    setStatus("서버에 연결할 수 없습니다. 서버가 실행 중인지 확인해주세요.");
  } finally {
    state.loading = false;
  }
}

// ====== 초기화 ======
async function init() {
  // Task 6 에서 로그인 패널로 확장됨. 우선 미로그인이면 안내만.
  try {
    const res = await api.loginStatus();
    state.loggedIn = res.data && res.data.is_logged_in;
    state.username = (res.data && res.data.username) || "";
  } catch (e) {
    state.loggedIn = false;
  }
  if (!state.loggedIn) {
    setStatus("로그인이 필요합니다.");
  }

  $("search-form").addEventListener("submit", (ev) => {
    ev.preventDefault();
    if (!state.loggedIn) {
      setStatus("로그인이 필요합니다.");
      return;
    }
    const kw = $("keyword").value.trim();
    if (kw) doSearch(kw);
  });
}

document.addEventListener("DOMContentLoaded", init);
