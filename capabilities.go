package main

import "github.com/xpzouying/xiaohongshu-mcp/search"

type platformCapability struct {
	Sorts           []string `json:"sorts"`
	Metrics         []string `json:"metrics"`
	NativeFilters   []string `json:"native_filters"`
	PostFilters     []string `json:"post_filters"`
	Pagination      string   `json:"pagination"` // single_page|opaque_cursor
	Available       bool     `json:"available"`
	UnsupportedNote string   `json:"unsupported_note"`
}

type searchCapabilitiesData struct {
	Platforms map[string]platformCapability `json:"platforms"`
	MergeRule string                        `json:"merge_rule"`
}

func searchPlatformCapabilities(avail map[string]search.Availability) map[string]platformCapability {
	commonPost := []string{"include_keywords", "exclude_keywords", "min_likes", "min_comments", "min_favorites"}
	return map[string]platformCapability{
		"xiaohongshu": {
			Sorts:         []string{"relevance", "popularity", "latest"},
			Metrics:       []string{"likes", "comments", "favorites", "shares", "duration"},
			NativeFilters: []string{"keyword", "sort", "publish_time", "video_only"},
			PostFilters:   append(append([]string{}, commonPost...), "date_from", "date_to", "duration_min", "duration_max"),
			Pagination:    "single_page", Available: avail["xiaohongshu"].Available,
			UnsupportedNote: "조회수 미지원; M1 단일 페이지",
		},
		"douyin": {
			Sorts:         []string{"relevance", "popularity", "latest"},
			Metrics:       []string{"likes", "comments", "favorites", "views", "shares", "duration"},
			NativeFilters: []string{"keyword", "sort", "publish_time", "duration", "video_only"},
			PostFilters:   append(append([]string{}, commonPost...), "min_views", "date_from", "date_to", "duration_min", "duration_max"),
			Pagination:    "opaque_cursor", Available: avail["douyin"].Available,
		},
		"tiktok": {
			Sorts:         []string{"relevance", "popularity", "latest"},
			Metrics:       []string{"likes", "comments", "favorites", "views", "shares", "duration"},
			NativeFilters: []string{"keyword", "video_only"},
			PostFilters:   append(append([]string{}, commonPost...), "min_views", "date_from", "date_to", "duration_min", "duration_max"),
			Pagination:    "single_page", Available: avail["tiktok"].Available,
			UnsupportedNote: "인기/최신은 반환 집합 후처리; M1 단일 페이지",
		},
	}
}
