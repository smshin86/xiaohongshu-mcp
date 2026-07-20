package search

import "sort"

// platformOrder 는 rank 머지의 결정적 라운드로빈 순서(동점 타이브레이커).
// sort.Strings 로 platformOrder 자체를 정렬하면 douyin 이 xiaohongshu 보다 먼저 와 기대 순서가 깨지므로 금지.
var platformOrder = []string{"xiaohongshu", "douyin", "tiktok"}

// rankMerge 는 각 플랫폼 집합을 within-platform rank 기반으로 머지한다.
// 규칙(spec §통합 정렬): 각 플랫폼의 adapter 입력 순서를 그대로 보존해 rank r(0부터) 부여 →
// score = 1/(r+1). 같은 r(동점) 은 platformOrder 라운드로빈으로 배치.
// raw 지표(likes/time/PostID) 재정렬 금지 — adapter 순서가 곧 rank.
// 입력 groups 는 호출자(Task 5)가 filter→dedupe→per_platform_limit 한 결과이므로 여기서 재정렬/절단 금지.
// cross-platform dedupe 도 하지 않는다. RankScore 는 복사본에 설정(원본 슬라이스 미변경).
func rankMerge(groups map[string][]VideoItem) []VideoItem {
	order := presentPlatformOrder(groups)
	maxLen := 0
	for _, items := range groups {
		if len(items) > maxLen {
			maxLen = len(items)
		}
	}
	out := make([]VideoItem, 0)
	for r := 0; r < maxLen; r++ {
		for _, p := range order {
			items := groups[p]
			if r < len(items) {
				it := items[r]                    // 값 복사(VideoItem 은 값 타입)
				it.RankScore = 1.0 / float64(r+1) // r=0 → 1.0, r=1 → 0.5, ...
				out = append(out, it)
			}
		}
	}
	return out
}

// presentPlatformOrder 는 platformOrder 중 존재하는 플랫폼을 그 순서로, 미등록 플랫폼은 이름순 뒤에.
func presentPlatformOrder(groups map[string][]VideoItem) []string {
	present := make(map[string]bool, len(groups))
	for p := range groups {
		present[p] = true
	}
	order := make([]string, 0, len(groups))
	for _, p := range platformOrder {
		if present[p] {
			order = append(order, p)
			delete(present, p)
		}
	}
	extras := make([]string, 0)
	for p := range present {
		extras = append(extras, p)
	}
	sort.Strings(extras) // 미등록 플랫폼만 결정적 정렬(platformOrder 자체 정렬 금지)
	return append(order, extras...)
}
