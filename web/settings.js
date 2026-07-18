// web/settings.js — 플랫폼 로그인(로컬 설정 영역). 검색 화면과 분리.
const $ = (id) => document.getElementById(id);

const api = {
  async loginStatus() { const r = await fetch("/api/v1/login/status"); return r.json(); },
  async loginQrcode() { const r = await fetch("/api/v1/login/qrcode"); return r.json(); },
};

function setStatus(msg) { $("login-status").textContent = msg; }

async function startLogin() {
  setStatus("QR 코드를 가져오는 중…");
  try {
    const res = await api.loginQrcode();
    if (!res.success) { setStatus("오류: " + (res.message || "")); return; }
    const data = res.data || {};
    if (data.is_logged_in) { setStatus("이미 로그인되어 있습니다."); return; }
    let src = data.img || "";
    if (src && !src.startsWith("data:")) src = "data:image/png;base64," + src;
    $("qrcode-img").src = src;
    $("qrcode-img").hidden = false;
    setStatus("샤오홍슈 앱으로 QR 을 스캔하세요.");
    pollLoginStatus();
  } catch (e) {
    setStatus("서버에 연결할 수 없습니다. 잠시 후 다시 시도해주세요.");
  }
}

let pollTimer = null;
async function pollLoginStatus() {
  if (pollTimer) clearTimeout(pollTimer);
  try {
    const res = await api.loginStatus();
    if (res.data && res.data.is_logged_in) {
      setStatus(res.data.username ? `로그인됨: ${res.data.username}` : "로그인되었습니다.");
      $("qrcode-img").hidden = true;
      return;
    }
  } catch (e) { /* 재시도 */ }
  pollTimer = setTimeout(pollLoginStatus, 2000);
}

async function init() {
  try {
    const res = await api.loginStatus();
    if (res.data && res.data.is_logged_in) {
      setStatus((res.data.username ? `로그인됨: ${res.data.username}` : "이미 로그인됨.") + " (검색에 자동 적용)");
    }
  } catch (e) { /* 무시 */ }
  $("login-btn").addEventListener("click", startLogin);
}

document.addEventListener("DOMContentLoaded", init);
