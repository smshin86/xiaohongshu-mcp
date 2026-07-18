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

// ====== 로그인 ======
function renderLoginState() {
  if (state.loggedIn) {
    hide($("login-section"));
    show($("search-section"));
    setStatus(state.username ? `로그인됨: ${state.username}` : "");
  } else {
    show($("login-section"));
    $("login-status").textContent = "QR 코드로 로그인 버튼을 눌러주세요.";
    $("qrcode-img").hidden = true;
  }
}

let pollTimer = null;
async function startLogin() {
  $("login-status").textContent = "QR 코드를 가져오는 중…";
  const res = await api.loginQrcode();
  if (!res.success) {
    $("login-status").textContent = "오류: " + (res.message || "");
    return;
  }
  const data = res.data || {};
  if (data.is_logged_in) {
    state.loggedIn = true;
    renderLoginState();
    return;
  }
  // img 가 data: 접두사 없는 순수 base64 면 보정
  let src = data.img || "";
  if (src && !src.startsWith("data:")) src = "data:image/png;base64," + src;
  $("qrcode-img").src = src;
  $("qrcode-img").hidden = false;
  $("login-status").textContent = "샤오홍슈 앱으로 아래 QR을 스캔하세요.";
  pollLoginStatus();
}

async function pollLoginStatus() {
  if (pollTimer) clearTimeout(pollTimer);
  const res = await api.loginStatus();
  if (res.data && res.data.is_logged_in) {
    state.loggedIn = true;
    state.username = (res.data && res.data.username) || "";
    renderLoginState();
    return;
  }
  pollTimer = setTimeout(pollLoginStatus, 2000);
}

// ====== 초기화 ======
async function init() {
  try {
    const res = await api.loginStatus();
    state.loggedIn = res.data && res.data.is_logged_in;
    state.username = (res.data && res.data.username) || "";
  } catch (e) {
    state.loggedIn = false;
  }
  renderLoginState();

  $("login-btn").addEventListener("click", startLogin);

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
