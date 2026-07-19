package xiaohongshu

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/browser"
)

func TestSearch(t *testing.T) {

	t.Skip("SKIP: 测试发布")

	b := browser.NewBrowser(false)
	defer b.Close()

	page := b.NewPage()
	defer func() {
		_ = page.Close()
	}()

	action := NewSearchAction(page)

	feeds, err := action.Search(context.Background(), "Kimi")
	require.NoError(t, err)
	require.NotEmpty(t, feeds, "feeds should not be empty")

	fmt.Printf("成功获取到 %d 个 Feed\n", len(feeds))

	for _, feed := range feeds {
		fmt.Printf("Feed ID: %s\n", feed.ID)
		fmt.Printf("Feed Title: %s\n", feed.NoteCard.DisplayTitle)
	}
}

func TestSearchWithFilters(t *testing.T) {

	//t.Skip("SKIP: 测试筛选功能")

	b := browser.NewBrowser(false)
	defer b.Close()

	page := b.NewPage()
	defer func() {
		_ = page.Close()
	}()

	action := NewSearchAction(page)

	// 使用新的 FilterOption 结构
	filter := FilterOption{
		NoteType:    "图文",
		PublishTime: "一天内",
	}

	feeds, err := action.Search(context.Background(), "dn432", filter)
	require.NoError(t, err)
	require.NotEmpty(t, feeds, "feeds should not be empty")

	fmt.Printf("成功获取到 %d 个筛选后的 Feed\n", len(feeds))

	for _, feed := range feeds {
		fmt.Printf("Feed ID: %s\n", feed.ID)
		fmt.Printf("Feed Title: %s\n", feed.NoteCard.DisplayTitle)
	}
}

// TestComputeFilterActions 验证 fast-path 判定逻辑:
// 关键词搜索(GET)会传入零值 FilterOption, 此时不应进入筛选 hover 流程。
func TestComputeFilterActions(t *testing.T) {
	// 零值 FilterOption(无任何筛选值) -> 空动作 -> 跳过 hover
	actions, err := computeFilterActions(FilterOption{})
	require.NoError(t, err)
	require.Empty(t, actions, "零值 FilterOption 不应产生筛选动作")

	// 单个有效筛选 -> 一个动作
	actions, err = computeFilterActions(FilterOption{NoteType: "视频"})
	require.NoError(t, err)
	require.Len(t, actions, 1)
	require.Equal(t, "视频", actions[0].Text)

	// 多个有效筛选 -> 多个动作(顺序保留)
	actions, err = computeFilterActions(FilterOption{
		SortBy:   "最新",
		NoteType: "图文",
	})
	require.NoError(t, err)
	require.Len(t, actions, 2)
	require.Equal(t, "最新", actions[0].Text)
	require.Equal(t, "图文", actions[1].Text)

	// 无效筛选值 -> 错误(不应静默跳过)
	_, err = computeFilterActions(FilterOption{NoteType: "不存在的类型"})
	require.Error(t, err)
}

func TestFilterValidation(t *testing.T) {
	// 测试有效的筛选选项转换
	validFilter := FilterOption{
		NoteType:    "图文",
		PublishTime: "一天内",
	}
	internalFilters, err := convertToInternalFilters(validFilter)
	require.NoError(t, err)
	require.Len(t, internalFilters, 2)

	// 验证转换后的内部筛选选项
	for _, filter := range internalFilters {
		err := validateInternalFilterOption(filter)
		require.NoError(t, err)
	}

	// 测试无效的筛选值
	invalidFilter := FilterOption{
		NoteType: "不存在的类型",
	}
	_, err = convertToInternalFilters(invalidFilter)
	require.Error(t, err)
	require.Contains(t, err.Error(), "未找到文本")

	// 测试所有有效的筛选选项
	allFilters := FilterOption{
		SortBy:      "最新",
		NoteType:    "视频",
		PublishTime: "一周内",
		SearchScope: "已关注",
		Location:    "同城",
	}
	internalFilters, err = convertToInternalFilters(allFilters)
	require.NoError(t, err)
	require.Len(t, internalFilters, 5)
}
