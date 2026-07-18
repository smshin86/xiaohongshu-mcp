package search

// int64Ptr 는 int64 값의 포인터를 반환한다(미지원 지표 구분용).
func int64Ptr(v int64) *int64 { return &v }

// dedupeKey 는 platform:post_url 우선, 없으면 platform:post_id. 크로스플랫폼 제거 금지.
func dedupeKey(v VideoItem) string {
	if v.PostURL != "" {
		return v.Platform + ":" + v.PostURL
	}
	return v.Platform + ":" + v.PostID
}

// assignDedupeKey 는 SourceKeyword 와 DedupeKey 를 채운다.
func assignDedupeKey(items []VideoItem, keyword string) []VideoItem {
	for i := range items {
		items[i].SourceKeyword = keyword
		items[i].DedupeKey = dedupeKey(items[i])
	}
	return items
}

// dedupeByKey 는 첫 등장 항목만 남긴다(입력 순서 보존).
// it.DedupeKey 를 읽는다 — 호출 전에 assignDedupeKey 로 스테이징해야 한다(Task 5 파이프라인).
func dedupeByKey(items []VideoItem) []VideoItem {
	seen := make(map[string]struct{}, len(items))
	out := make([]VideoItem, 0, len(items))
	for _, it := range items {
		if _, ok := seen[it.DedupeKey]; ok {
			continue
		}
		seen[it.DedupeKey] = struct{}{}
		out = append(out, it)
	}
	return out
}
