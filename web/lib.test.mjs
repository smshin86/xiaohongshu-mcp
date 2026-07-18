import { test } from "node:test";
import assert from "node:assert/strict";
import {
  initialState, togglePlatform, toggleAllPlatforms, platformsFromState,
  pendingPlatforms, visibleItems, selectLoadMorePlatforms,
  resetForNewSearch, ingestItems, dedupeKey, parseFilters,
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
