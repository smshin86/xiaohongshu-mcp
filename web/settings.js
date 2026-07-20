// web/settings.js — 플랫폼 로그인(로컬 설정 영역). 검색 화면과 분리.
// 순수 결정 로직(timeout 파싱/폴링 결정)은 lib.js(import).
import { parseGoDurationMs, loginPollDecision } from "./lib.js";

const $ = (id) => document.getElementById(id);

const POLL_INTERVAL_MS = 2000;
// 서버가 timeout 을 주지 않을 때의 안전 상한(무한 폴링 방지).
const DEFAULT_QR_TIMEOUT_MS = 5 * 60 * 1000;

const api = {
  async loginStatus() { const r = await fetch("/api/v1/login/status"); return r.json(); },
  async loginQrcode() { const r = await fetch("/api/v1/login/qrcode"); return r.json(); },
};

function setStatus(msg) { $("login-status").textContent = msg; }

let pollTimer = null;
let pollDeadline = 0; // epoch ms. 0 = 활성 폴링 없음.

function stopPolling() {
  if (pollTimer) { clearTimeout(pollTimer); pollTimer = null; }
}

async function startLogin() {
  setStatus("QR 코드를 가져오는 중…");
  stopPolling();
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
    // QR 만료 시점(서버 timeout). 파싱 불가 시 안전 상한 적용. 이 시점까지만 폴링.
    const timeoutMs = parseGoDurationMs(data.timeout);
    pollDeadline = Date.now() + (timeoutMs > 0 ? timeoutMs : DEFAULT_QR_TIMEOUT_MS);
    pollLoginStatus();
  } catch (e) {
    setStatus("서버에 연결할 수 없습니다. 잠시 후 다시 시도해주세요.");
  }
}

async function pollLoginStatus() {
  if (pollTimer) { clearTimeout(pollTimer); pollTimer = null; }
  let isLoggedIn = false;
  try {
    const res = await api.loginStatus();
    isLoggedIn = !!(res.data && res.data.is_logged_in);
    if (isLoggedIn) {
      setStatus(res.data.username ? `로그인됨: ${res.data.username}` : "로그인되었습니다.");
      $("qrcode-img").hidden = true;
    }
  } catch (e) { /* 일시적 네트워크 오류: 아래 결정에 따라 재시도 또는 중단 */ }

  const decision = loginPollDecision({ isLoggedIn, now: Date.now(), deadline: pollDeadline });
  if (decision === "logged_in") {
    pollDeadline = 0;
    return;
  }
  if (decision === "expired") {
    // QR timeout 경과: 무한 폴링 중단 + 만료 안내. QR 이미지도 숨김(만료된 코드 스캔 방지).
    pollDeadline = 0;
    $("qrcode-img").hidden = true;
    setStatus("인증이 완료되지 않았거나 QR 이 만료되었습니다. 다시 시도해주세요.");
    return;
  }
  pollTimer = setTimeout(pollLoginStatus, POLL_INTERVAL_MS);
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
