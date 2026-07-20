package xiaohongshu

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/xpzouying/xiaohongshu-mcp/errors"
)

type SearchResult struct {
	Search struct {
		Feeds FeedsValue `json:"feeds"`
	} `json:"search"`
}

// FilterOption 筛选选项结构体
type FilterOption struct {
	SortBy      string `json:"sort_by,omitempty" jsonschema:"排序依据: 综合|最新|最多点赞|最多评论|最多收藏,默认为'综合'"`
	NoteType    string `json:"note_type,omitempty" jsonschema:"笔记类型: 不限|视频|图文,默认为'不限'"`
	PublishTime string `json:"publish_time,omitempty" jsonschema:"发布时间: 不限|一天内|一周内|半年内,默认为'不限'"`
	SearchScope string `json:"search_scope,omitempty" jsonschema:"搜索范围: 不限|已看过|未看过|已关注,默认为'不限'"`
	Location    string `json:"location,omitempty" jsonschema:"位置距离: 不限|同城|附近,默认为'不限'"`
}

// internalFilterOption 内部使用的筛选选项(基于索引)
type internalFilterOption struct {
	FiltersIndex int    // 筛选组索引
	TagsIndex    int    // 标签索引
	Text         string // 标签文本描述
}

// 预定义的筛选选项映射表（内部使用）
var filterOptionsMap = map[int][]internalFilterOption{
	1: { // 排序依据
		{FiltersIndex: 1, TagsIndex: 1, Text: "综合"},
		{FiltersIndex: 1, TagsIndex: 2, Text: "最新"},
		{FiltersIndex: 1, TagsIndex: 3, Text: "最多点赞"},
		{FiltersIndex: 1, TagsIndex: 4, Text: "最多评论"},
		{FiltersIndex: 1, TagsIndex: 5, Text: "最多收藏"},
	},
	2: { // 笔记类型
		{FiltersIndex: 2, TagsIndex: 1, Text: "不限"},
		{FiltersIndex: 2, TagsIndex: 2, Text: "视频"},
		{FiltersIndex: 2, TagsIndex: 3, Text: "图文"},
	},
	3: { // 发布时间
		{FiltersIndex: 3, TagsIndex: 1, Text: "不限"},
		{FiltersIndex: 3, TagsIndex: 2, Text: "一天内"},
		{FiltersIndex: 3, TagsIndex: 3, Text: "一周内"},
		{FiltersIndex: 3, TagsIndex: 4, Text: "半年内"},
	},
	4: { // 搜索范围
		{FiltersIndex: 4, TagsIndex: 1, Text: "不限"},
		{FiltersIndex: 4, TagsIndex: 2, Text: "已看过"},
		{FiltersIndex: 4, TagsIndex: 3, Text: "未看过"},
		{FiltersIndex: 4, TagsIndex: 4, Text: "已关注"},
	},
	5: { // 位置距离
		{FiltersIndex: 5, TagsIndex: 1, Text: "不限"},
		{FiltersIndex: 5, TagsIndex: 2, Text: "同城"},
		{FiltersIndex: 5, TagsIndex: 3, Text: "附近"},
	},
}

// convertToInternalFilters 将 FilterOption 转换为内部的 internalFilterOption 列表
func convertToInternalFilters(filter FilterOption) ([]internalFilterOption, error) {
	var internalFilters []internalFilterOption

	// 处理排序依据
	if filter.SortBy != "" {
		internal, err := findInternalOption(1, filter.SortBy)
		if err != nil {
			return nil, fmt.Errorf("排序依据错误: %w", err)
		}
		internalFilters = append(internalFilters, internal)
	}

	// 处理笔记类型
	if filter.NoteType != "" {
		internal, err := findInternalOption(2, filter.NoteType)
		if err != nil {
			return nil, fmt.Errorf("笔记类型错误: %w", err)
		}
		internalFilters = append(internalFilters, internal)
	}

	// 处理发布时间
	if filter.PublishTime != "" {
		internal, err := findInternalOption(3, filter.PublishTime)
		if err != nil {
			return nil, fmt.Errorf("发布时间错误: %w", err)
		}
		internalFilters = append(internalFilters, internal)
	}

	// 处理搜索范围
	if filter.SearchScope != "" {
		internal, err := findInternalOption(4, filter.SearchScope)
		if err != nil {
			return nil, fmt.Errorf("搜索范围错误: %w", err)
		}
		internalFilters = append(internalFilters, internal)
	}

	// 处理位置距离
	if filter.Location != "" {
		internal, err := findInternalOption(5, filter.Location)
		if err != nil {
			return nil, fmt.Errorf("位置距离错误: %w", err)
		}
		internalFilters = append(internalFilters, internal)
	}

	return internalFilters, nil
}

// findInternalOption 根据筛选组索引和文本查找内部筛选选项
func findInternalOption(filtersIndex int, text string) (internalFilterOption, error) {
	options, exists := filterOptionsMap[filtersIndex]
	if !exists {
		return internalFilterOption{}, fmt.Errorf("筛选组 %d 不存在", filtersIndex)
	}

	for _, option := range options {
		if option.Text == text {
			return option, nil
		}
	}

	return internalFilterOption{}, fmt.Errorf("在筛选组 %d 中未找到文本 '%s'", filtersIndex, text)
}

// validateInternalFilterOption 验证内部筛选选项是否在有效范围内
func validateInternalFilterOption(filter internalFilterOption) error {
	// 检查筛选组索引是否有效
	if filter.FiltersIndex < 1 || filter.FiltersIndex > 5 {
		return fmt.Errorf("无效的筛选组索引 %d，有效范围为 1-5", filter.FiltersIndex)
	}

	// 检查标签索引是否在对应筛选组的有效范围内
	options, exists := filterOptionsMap[filter.FiltersIndex]
	if !exists {
		return fmt.Errorf("筛选组 %d 不存在", filter.FiltersIndex)
	}

	if filter.TagsIndex < 1 || filter.TagsIndex > len(options) {
		return fmt.Errorf("筛选组 %d 的标签索引 %d 超出范围，有效范围为 1-%d",
			filter.FiltersIndex, filter.TagsIndex, len(options))
	}

	return nil
}

// computeFilterActions 把 FilterOption 列表归约为实际的筛选动作。
// 零值 FilterOption(GET 关键词搜索会传入)返回空切片——用于 fast-path:
// 没有实际筛选值时不进入筛选 hover 流程,避免不必要的交互与超时。
func computeFilterActions(filters ...FilterOption) ([]internalFilterOption, error) {
	var actions []internalFilterOption
	for _, filter := range filters {
		internal, err := convertToInternalFilters(filter)
		if err != nil {
			return nil, fmt.Errorf("筛选选项转换失败: %w", err)
		}
		for _, f := range internal {
			if err := validateInternalFilterOption(f); err != nil {
				return nil, fmt.Errorf("筛选选项验证失败: %w", err)
			}
			actions = append(actions, f)
		}
	}
	return actions, nil
}

type SearchAction struct {
	page *rod.Page
}

// 검색 페이지 로드 후 인증/feeds 준비를 기다리는 bounded 상한들.
// 예전 WaitStable(time.Second) 은 로그인 프롬프트가 뜬 불안정 페이지에서
// page.Timeout 전체(60s)를 채워 "응답 시간 초과" 로 위장했던 원인.
// 아래 상한들은 명시적으로 짧게 잡아 인증 단절을 빠르게(fast-fail) 감지한다.
const (
	searchPageTimeout       = 60 * time.Second // 전체 페이지 안전망(마지막 상한)
	searchAuthReadyTimeout  = 8 * time.Second  // __INITIAL_STATE__.user 가 읽힐 때까지
	searchAuthCheckTimeout  = 5 * time.Second  // robust auth 판정 Eval 상한
	searchFeedsReadyTimeout = 10 * time.Second // search 상태가 읽힐 때까지
)

func NewSearchAction(page *rod.Page) *SearchAction {
	pp := page.Timeout(searchPageTimeout)

	return &SearchAction{page: pp}
}

// decideAuthLost: 검색 페이지 인증 단절 판정(순수, 페이지 없이 테스트 가능).
// user-state 대기 실패 OR robust auth(false/eval 실패) 중 하나라도 걸리면 ErrAuthLost.
// 이 결정이 60s timeout 대신 빠른 fast-fail 을 담당한다.
func decideAuthLost(userStateWaitErr error, authed bool, authErr error) bool {
	if userStateWaitErr != nil {
		return true
	}
	if authErr != nil || !authed {
		return true
	}
	return false
}

// ensureSearchAuthed: Navigate+WaitLoad 직후 bounded 로 인증 단절을 감지한다.
// XHS 가 검색 페이지 이동 시 세션을 무효화(로그인 UI/robust auth false)하면
// 즉시 ErrAuthLost 반환 — broad WaitStable(60s blocker) 을 대체.
func ensureSearchAuthed(page *rod.Page) error {
	userStateWaitErr := page.Timeout(searchAuthReadyTimeout).Wait(
		rod.Eval(`() => window.__INITIAL_STATE__ && window.__INITIAL_STATE__.user !== undefined`))
	authed, authErr := authCheck(page.Timeout(searchAuthCheckTimeout))
	if decideAuthLost(userStateWaitErr, authed, authErr) {
		return errors.ErrAuthLost
	}
	return nil
}

func (s *SearchAction) Search(ctx context.Context, keyword string, filters ...FilterOption) ([]Feed, error) {
	page := s.page.Context(ctx)

	// fast-path: 没有实际筛选值时跳过筛选 hover,直接取搜索结果。
	// GET 关键词搜索会传入零值 FilterOption,旧代码因 len(filters)>0 误入 hover,
	// 在登录态异常或筛选面板缺失时 Must* 超时 panic。此处先归约真实动作。
	actions, err := computeFilterActions(filters...)
	if err != nil {
		return nil, err
	}

	searchURL := makeSearchURL(keyword)
	if err := page.Navigate(searchURL); err != nil {
		return nil, fmt.Errorf("navigate search page failed: %w", err)
	}
	if err := page.WaitLoad(); err != nil {
		return nil, fmt.Errorf("wait search load failed: %w", err)
	}
	// Navigate+WaitLoad 직후의 broad WaitStable(60s blocker 원인) 제거.
	// 대신 bounded 로 인증 단절을 fast-fail: 검색 페이지 이동 시 XHS 가 세션을
	// 무효화하면 로그인 UI 가 뜨고 robust auth 가 false → 즉시 ErrAuthLost.
	if err := ensureSearchAuthed(page); err != nil {
		return nil, err
	}
	// feeds 준비 대기(bounded): search 객체만 생긴 중간 상태가 아니라 feeds
	// 필드까지 읽을 수 있을 때까지 기다린다.
	if err := page.Timeout(searchFeedsReadyTimeout).Wait(
		rod.Eval(`() => window.__INITIAL_STATE__ &&
			window.__INITIAL_STATE__.search !== undefined &&
			window.__INITIAL_STATE__.search.feeds !== undefined`)); err != nil {
		return nil, fmt.Errorf("wait search state failed: %w", err)
	}

	// 仅有实际筛选动作时才进入筛选流程
	if len(actions) > 0 {
		// 悬停在筛选按钮上
		filterButton, err := page.Element(`div.filter`)
		if err != nil {
			return nil, fmt.Errorf("find filter button failed: %w", err)
		}
		if filterButton == nil {
			return nil, fmt.Errorf("filter button not found")
		}
		if err := filterButton.Hover(); err != nil {
			return nil, fmt.Errorf("hover filter button failed: %w", err)
		}

		// 等待筛选面板出现
		if err := page.Wait(rod.Eval(`() => document.querySelector('div.filter-panel') !== null`)); err != nil {
			return nil, fmt.Errorf("wait filter panel failed: %w", err)
		}

		// 应用所有筛选条件
		for _, filter := range actions {
			selector := fmt.Sprintf(`div.filter-panel div.filters:nth-child(%d) div.tags:nth-child(%d)`,
				filter.FiltersIndex, filter.TagsIndex)
			option, err := page.Element(selector)
			if err != nil {
				return nil, fmt.Errorf("find filter option failed: %w", err)
			}
			if option == nil {
				return nil, fmt.Errorf("filter option not found: %s", selector)
			}
			if err := option.Click(proto.InputMouseButtonLeft, 1); err != nil {
				return nil, fmt.Errorf("click filter option failed: %w", err)
			}
		}

		// 等待页面更新后重新读取 __INITIAL_STATE__
		if err := page.WaitStable(time.Second); err != nil {
			return nil, fmt.Errorf("wait filter stable failed: %w", err)
		}
		if err := page.Wait(rod.Eval(`() => window.__INITIAL_STATE__ !== undefined`)); err != nil {
			return nil, fmt.Errorf("wait state after filter failed: %w", err)
		}
	}

	res, err := page.Eval(`() => {
		if (window.__INITIAL_STATE__ &&
		    window.__INITIAL_STATE__.search &&
		    window.__INITIAL_STATE__.search.feeds) {
			const feeds = window.__INITIAL_STATE__.search.feeds;
			const feedsData = feeds.value !== undefined ? feeds.value : feeds._value;
			if (feedsData) {
				return JSON.stringify(feedsData);
			}
		}
		return "";
	}`)
	if err != nil {
		return nil, fmt.Errorf("eval feeds failed: %w", err)
	}
	result := res.Value.String()

	if result == "" {
		return nil, errors.ErrNoFeeds
	}

	var feeds []Feed
	if err := json.Unmarshal([]byte(result), &feeds); err != nil {
		return nil, fmt.Errorf("failed to unmarshal feeds: %w", err)
	}

	return feeds, nil
}

func makeSearchURL(keyword string) string {

	values := url.Values{}
	values.Set("keyword", keyword)
	values.Set("source", "web_explore_feed")

	//https://www.xiaohongshu.com/search_result?keyword=%25E7%258E%258B%25E5%25AD%2590&source=web_search_result_notes
	//https://www.xiaohongshu.com/search_result?keyword=%25E7%258E%258B%25E5%25AD%2590&source=web_explore_feed
	return fmt.Sprintf("https://www.xiaohongshu.com/search_result?%s", values.Encode())
}
