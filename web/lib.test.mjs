import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import {
  initialState, togglePlatform, toggleAllPlatforms, platformsFromState,
  pendingPlatforms, visibleItems, selectLoadMorePlatforms,
  resetForNewSearch, ingestItems, dedupeKey, parseFilters,
  buildDownloadURL, canDownload, hasXHSManualFallback,
  parseUrlList, isKorean, normalizeExtractResponse, normalizeTranslateResponse,
  basisLabel,
} from "./lib.js";

test("initial platforms are XHS + Douyin and individual toggle keeps one", () => {
  let s = initialState();
  assert.deepEqual(platformsFromState(s), ["xiaohongshu", "douyin"]);
  s = togglePlatform(s, "douyin");
  s = togglePlatform(s, "xiaohongshu"); // 마지막 1개는 해제되지 않음
  assert.deepEqual(platformsFromState(s), ["xiaohongshu"]);
});

test("all toggle selects all and toggling again restores core defaults", () => {
  let s = toggleAllPlatforms(initialState());
  assert.deepEqual(platformsFromState(s), ["xiaohongshu", "douyin", "tiktok"]);
  s = toggleAllPlatforms(s);
  assert.deepEqual(platformsFromState(s), ["xiaohongshu", "douyin"]);
});

test("off hides locally and newly-on platform is pending until search", () => {
  let s = initialState();
  s.items = [
    { platform: "xiaohongshu", post_id: "x" },
    { platform: "douyin", post_id: "d" },
    { platform: "tiktok", post_id: "t" },
  ];
  s.lastReqPlatforms = ["xiaohongshu", "douyin"];
  s = togglePlatform(s, "douyin");
  assert.deepEqual(visibleItems(s).map((v) => v.post_id), ["x"]); // 재검색 없이 숨김
  s = togglePlatform(s, "tiktok");
  assert.deepEqual(pendingPlatforms(s), ["tiktok"]);
});

test("resetForNewSearch clears cursors/items/hasMore", () => {
  let s = initialState();
  s.pageCursors = { xiaohongshu: "x", douyin: "y", tiktok: "z" };
  s.items = [{ platform: "douyin", post_id: "1" }];
  s.hasMore = { xiaohongshu: true, douyin: true, tiktok: true };
  s = resetForNewSearch(s, "k", "latest");
  assert.equal(s.keyword, "k");
  assert.equal(s.sort, "latest");
  assert.equal(s.pageCursors.douyin, "");
  assert.equal(s.items.length, 0);
  assert.equal(s.hasMore.douyin, false);
});

test("ingestItems appends merged data.items and clears cursor when next_cursor empty", () => {
  let s = initialState();
  s.pageCursors.douyin = "old";
  const agg = {
    items: [{ platform: "douyin", post_id: "a" }],          // 서버 머지 결과(탑레벨)
    sides: { douyin: { available: { available: true }, next_cursor: "", has_more: false } },
  };
  s = ingestItems(s, agg, ["douyin"]);
  assert.equal(s.items.length, 1);                           // data.items 에서 append
  assert.equal(s.pageCursors.douyin, "");
  assert.equal(s.hasMore.douyin, false);
});

test("selectLoadMorePlatforms filters by has_more and lastReq", () => {
  let s = toggleAllPlatforms(initialState());
  s.hasMore = { xiaohongshu: false, douyin: true, tiktok: true };
  s.lastReqPlatforms = ["douyin", "tiktok"];
  assert.deepEqual(selectLoadMorePlatforms(s), ["douyin", "tiktok"]);
  s.lastReqPlatforms = ["douyin"];
  assert.deepEqual(selectLoadMorePlatforms(s), ["douyin"]);
});

test("dedupeKey includes platform even when post_url exists", () => {
  assert.equal(dedupeKey({ platform: "douyin", post_id: "1", post_url: "u" }), "douyin:u");
  assert.equal(dedupeKey({ platform: "xiaohongshu", post_id: "1" }), "xiaohongshu:1");
});

test("ingestItems preserves same url across different platforms", () => {
  let s = initialState();
  s = ingestItems(s, { items: [{ platform: "xiaohongshu", post_id: "a", post_url: "u1" }] }, ["xiaohongshu"]);
  s = ingestItems(s, { items: [{ platform: "douyin", post_id: "b", post_url: "u1" }] }, ["douyin"]);
  assert.equal(s.items.length, 2);
});

test("parseFilters splits keywords, parses numbers, video_only, per_platform_limit", () => {
  const f = parseFilters({ include: "风扇 便携", exclude: "避雷", min_likes: "50", duration_min: "60", per_platform_limit: "20", video_only: true });
  assert.deepEqual(f.include_keywords, ["风扇", "便携"]);
  assert.equal(f.min_likes, 50);
  assert.equal(f.duration_min, 60);
  assert.equal(f.per_platform_limit, 20);
  assert.equal(f.video_only, true);
});

test("parseFilters defaults per_platform_limit and fixes video_only true for M1", () => {
  const f = parseFilters({});
  assert.equal(f.per_platform_limit, 15);
  assert.equal(f.video_only, true);
});

test("style.css hides #qrcode-img when [hidden] (regression: broken QR icon before click)", () => {
  // 브라우저 smoke 결함: #qrcode-img { display:block }(id 선택자)가 UA [hidden]을 이겨서
  // QR 버튼 누르기 전 hidden 상태의 img 가 깨진 이미지로 노출됨. JS 토글(.hidden=true/false)은
  // 정상이므로 [hidden] 가드가 display:none 을 보장해야 함. 가드 삭제 시 회귀.
  const css = fs.readFileSync(new URL("./style.css", import.meta.url), "utf8");
  assert.match(
    css,
    /#qrcode-img\[hidden\]\s*\{[^}]*display:\s*none/i,
    "#qrcode-img[hidden] { display:none } 가드가 style.css 에 있어야 함"
  );
});

test("buildDownloadURL: XHS carries post_id/detail_token and excludes raw video_url", () => {
  const url = buildDownloadURL({
    platform: "xiaohongshu",
    post_id: "abc",
    detail_token: "tok123",
    // 원문 video_url 은 서명이 포함될 수 있으므로 절대 URL 에 넣지 않는다.
    video_url: "https://sns-img-bd.xhscdn.com/secret.mp4?sig=hush",
  });
  assert.ok(url.startsWith("/api/v1/download?"), url);
  const p = new URL(url, "http://x").searchParams;
  assert.equal(p.get("platform"), "xiaohongshu");
  assert.equal(p.get("post_id"), "abc");
  assert.equal(p.get("detail_token"), "tok123");
  assert.equal(p.get("video_url"), null, "XHS download URL must NOT carry raw video_url");
});

test("buildDownloadURL: optional filename appended (default absent)", () => {
  const withName = buildDownloadURL(
    { platform: "xiaohongshu", post_id: "x", detail_token: "t" },
    "내 클립.mp4"
  );
  const p1 = new URL(withName, "http://x").searchParams;
  assert.equal(p1.get("filename"), "내 클립.mp4");
  const withoutName = buildDownloadURL({ platform: "xiaohongshu", post_id: "x", detail_token: "t" });
  const p2 = new URL(withoutName, "http://x").searchParams;
  assert.equal(p2.has("filename"), false, "filename 생략 시 파라미터 없음");
});

test("buildDownloadURL: Douyin/TikTok encodes special chars and signed query round-trip", () => {
  const raw = "https://v.douyinvod.com/x.m4v?X-Bogus=abc def&sign=p&q=한국";
  const url = buildDownloadURL({ platform: "douyin", post_id: "d1", video_url: raw });
  const p = new URL(url, "http://x").searchParams;
  assert.ok(url.startsWith("/api/v1/download?"));
  assert.equal(p.get("platform"), "douyin");
  assert.equal(p.get("post_id"), "d1");
  assert.equal(p.get("url"), raw, "video_url 이 URLSearchParams 로 정확히 복원되어야 함");
  assert.equal(p.get("video_url"), null, "Douyin/TikTok 은 url= 만 사용(video_url= 아님)");
});

test("canDownload: XHS needs detail_token, Douyin/TikTok need video_url", () => {
  assert.equal(canDownload({ platform: "xiaohongshu", post_id: "x" }), false);
  assert.equal(canDownload({ platform: "xiaohongshu", post_id: "x", detail_token: "t" }), true);
  assert.equal(canDownload({ platform: "douyin", post_id: "d" }), false);
  assert.equal(canDownload({ platform: "douyin", post_id: "d", video_url: "https://v.douyinvod.com/x" }), true);
  assert.equal(canDownload({ platform: "tiktok", post_id: "t", video_url: "https://v16.tiktokcdn.com/x" }), true);
  assert.equal(canDownload({ platform: "tiktok" }), false);
  assert.equal(canDownload({ platform: "myspace", post_id: "m", video_url: "u" }), false);
});

test("buildDownloadURL: empty string when required identifiers missing", () => {
  assert.equal(buildDownloadURL({ platform: "xiaohongshu", post_id: "x" }), "", "XHS detail_token 누락");
  assert.equal(buildDownloadURL({ platform: "xiaohongshu", detail_token: "t" }), "", "XHS post_id 누락");
  assert.equal(buildDownloadURL({ platform: "douyin", post_id: "d" }), "", "Douyin video_url 누락");
  assert.equal(buildDownloadURL({ platform: "douyin", video_url: "u" }), "", "Douyin post_id 누락");
  assert.equal(buildDownloadURL({ platform: "myspace", post_id: "m", video_url: "u" }), "", "지원 않는 platform");
});

test("hasXHSManualFallback: true only for XHS with post_url", () => {
  assert.equal(hasXHSManualFallback({ platform: "xiaohongshu", post_url: "https://xhslink.com/a" }), true);
  assert.equal(hasXHSManualFallback({ platform: "xiaohongshu" }), false, "post_url 필요");
  assert.equal(hasXHSManualFallback({ platform: "xiaohongshu", post_url: "" }), false);
  assert.equal(hasXHSManualFallback({ platform: "douyin", post_url: "https://www.douyin.com/v/1" }), false, "XHS 전용");
  assert.equal(hasXHSManualFallback({ platform: "tiktok", post_url: "https://www.tiktok.com/x" }), false);
});

// ====== Task 5: 키워드 발견 헬퍼 ======
test("parseUrlList: split/dedupe/drop-empty, no cap (4 URL → 4)", () => {
  assert.deepEqual(parseUrlList("https://a.com\nhttps://b.com, https://a.com https://c.com\nhttps://d.com"),
    ["https://a.com", "https://b.com", "https://c.com", "https://d.com"]);
  assert.deepEqual(parseUrlList("  ,,\n \t"), []);
  assert.deepEqual(parseUrlList(""), []);
  // first-seen order preserved on dedupe (same token later is dropped)
  assert.deepEqual(parseUrlList("b a b a"), ["b", "a"], "공백 분할 + 중복(first-seen) 제거");
});

test("isKorean: hangul 여부 (손 선풍기 true / 便携风扇 false / fan false)", () => {
  assert.equal(isKorean("손 선풍기"), true);
  assert.equal(isKorean("便携风扇"), false);
  assert.equal(isKorean("fan"), false);
  assert.equal(isKorean(""), false);
  // 한글 자음/모음 없이 받침 syllable 만 판정 — 영문+한글 혼용도 true
  assert.equal(isKorean("travel 풀다"), true);
});

test("normalizeExtractResponse: success → candidates+note; !success/결측/빈 → failed", () => {
  const ok = normalizeExtractResponse({
    success: true,
    data: {
      candidates: [
        { keyword: "便携风扇", source_url: "u1", basis: "title", confidence: 0.9 },
        { keyword: "", basis: "hashtag", confidence: 0.5 }, // 빈 keyword 제거
        { keyword: "x".repeat(80), basis: "description" },                   // 64자 절단
      ],
      note: "추출 노트",
    },
  });
  assert.equal(ok.failed, false);
  assert.equal(ok.note, "추출 노트");
  assert.equal(ok.candidates.length, 2, "빈 keyword 제거 + 64자 절단 후 2개");
  assert.equal(ok.candidates[0].keyword, "便携风扇");
  assert.equal(ok.candidates[1].keyword.length, 64, "64자 절단");

  assert.equal(normalizeExtractResponse({ success: false }).failed, true);
  assert.equal(normalizeExtractResponse({ success: true }).failed, true, "data 결측");
  assert.equal(normalizeExtractResponse({ success: true, data: {} }).failed, true, "candidates 비배열");
  assert.equal(normalizeExtractResponse({ success: true, data: { candidates: "x" } }).failed, true);
  assert.equal(normalizeExtractResponse({ success: true, data: { candidates: [] } }).failed, true, "빈 배열");
  assert.equal(normalizeExtractResponse({
    success: true, data: { candidates: [{ keyword: "" }] },
  }).failed, true, "정규화 후 빈 배열도 failed");
  // note 결측 시 빈 문자열
  const noNote = normalizeExtractResponse({ success: true, data: { candidates: [{ keyword: "k" }] } });
  assert.equal(noNote.note, "");
});

test("normalizeTranslateResponse: success → [{zh}]; !success → failed", () => {
  const ok = normalizeTranslateResponse({
    success: true,
    data: { candidates: [{ zh: "便携风扇" }, { zh: "" }, { zh: "便携风扇" }, { zh: "x".repeat(80) }] },
  });
  assert.equal(ok.failed, false);
  assert.deepEqual(ok.candidates.map((c) => c.zh), ["便携风扇", "x".repeat(64)], "빈/중복 제거 + 64자 절단");

  assert.equal(normalizeTranslateResponse({ success: false }).failed, true);
  assert.equal(normalizeTranslateResponse({ success: true, data: { candidates: [] } }).failed, true);
  assert.equal(normalizeTranslateResponse({ success: true, data: { candidates: [{ zh: "" }] } }).failed, true);
  // 캡 5개
  const six = normalizeTranslateResponse({
    success: true,
    data: { candidates: [1, 2, 3, 4, 5, 6].map((n) => ({ zh: "k" + n })) },
  });
  assert.equal(six.candidates.length, 5, "최대 5개");
});

test("basisLabel: title/hashtag/description/metadata 매핑 + default 메타", () => {
  assert.equal(basisLabel("title"), "제목");
  assert.equal(basisLabel("hashtag"), "해시태그");
  assert.equal(basisLabel("description"), "설명");
  assert.equal(basisLabel("metadata"), "메타");
  assert.equal(basisLabel("unknown"), "메타");
  assert.equal(basisLabel(undefined), "메타");
  assert.equal(basisLabel(""), "메타");
});
