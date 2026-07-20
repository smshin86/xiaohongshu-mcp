package xiaohongshu

import (
	"context"
	"fmt"
	"testing"
	"time"

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

	// 실제 로그인된 XHS 세션이 필요한 live 통합 테스트(TestSearch 와 동일 분류).
	// 세션 없는 환경에선 검색 페이지가 로그인 프롬프트로 떨어지며, 이제 ensureSearchAuthed
	// 가 이를 인증 단절(ErrAuthLost) 로 fast-fail 한다(과거엔 빈 feeds → ErrNoFeeds).
	// CI/클린 환경에선 세션이 없으므로 스킵; 세션 있는 로컬에서만 수동 실행.
	t.Skip("SKIP: live XHS 세션 필요(필터 통합 테스트)")

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

// TestDecideAuthLost: 검색 페이지 인증 단결 판정(순수). ensureSearchAuthed 가
// 60s timeout 대신 ErrAuthLost 로 fast-fail 할지 결정하는 로직이다.
// user-state 대기 실패 OR robust auth(false/eval 실패) → true(ErrAuthLost).
func TestDecideAuthLost(t *testing.T) {
	waitErr := fmt.Errorf("user state wait timeout")
	evalErr := fmt.Errorf("eval auth check failed")

	// (1) user state 가 읽히지 않음 → 단결(로그인 프롬프트 페이지 등).
	require.True(t, decideAuthLost(waitErr, false, nil))
	// (2) user state OK 지만 robust auth false → 단결(XHS 가 세션 무효화).
	require.True(t, decideAuthLost(nil, false, nil))
	// (3) authCheck Eval 자체 실패 → 보수적 단결.
	require.True(t, decideAuthLost(nil, false, evalErr))
	// (4) 정상 인증(user state OK + authed true) → 통과.
	require.False(t, decideAuthLost(nil, true, nil))
	// (5) authCheck 성공은 했지만 authed false 면 evalErr nil 이어도 단결.
	require.True(t, decideAuthLost(nil, false, nil))
}

// TestSearchAuthTimeoutsBounded: 인증/feeds 준비 상한이 page timeout(60s) 보다
// 작아야 한다 — broad WaitStable(60s blocker) 회귀 방지. fast-fail 보장.
func TestSearchAuthTimeoutsBounded(t *testing.T) {
	require.Less(t, searchAuthReadyTimeout, searchPageTimeout, "auth ready 대기가 page timeout 보다 작아야 함")
	require.Less(t, searchAuthCheckTimeout, searchPageTimeout, "auth check 대기가 page timeout 보다 작아야 함")
	require.Less(t, searchFeedsReadyTimeout, searchPageTimeout, "feeds ready 대기가 page timeout 보다 작아야 함")
	// 60s blocker 회귀 가드: 모든 준비 상한이 30s 미만(fast-fail 의도).
	for _, d := range []time.Duration{searchAuthReadyTimeout, searchAuthCheckTimeout, searchFeedsReadyTimeout} {
		require.Less(t, d, 30*time.Second, "준비 상한은 30s 미만이어야 함(60s 대기 회귀 방지)")
	}
}
