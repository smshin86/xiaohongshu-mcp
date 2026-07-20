package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/go-rod/rod"
	"github.com/sirupsen/logrus"
	"github.com/xpzouying/headless_browser"
	"github.com/xpzouying/xiaohongshu-mcp/browser"
	"github.com/xpzouying/xiaohongshu-mcp/configs"
	"github.com/xpzouying/xiaohongshu-mcp/cookies"
	xhserrors "github.com/xpzouying/xiaohongshu-mcp/errors"
	"github.com/xpzouying/xiaohongshu-mcp/localstorage"
	"github.com/xpzouying/xiaohongshu-mcp/pkg/downloader"
	"github.com/xpzouying/xiaohongshu-mcp/pkg/xhsutil"
	"github.com/xpzouying/xiaohongshu-mcp/xhssession"
	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

// sessionRunner: 세션 매니저 사용 메서드만 노출(테스트용 fake 교체 가능).
// *xhssession.Manager 가 이 인터페이스를 충족한다.
type sessionRunner interface {
	State() xhssession.State
	LoggedIn() bool
	StartLogin(ctx context.Context) (string, bool, error)
	WithPage(ctx context.Context, fn func(*rod.Page) error) error
	Logout() error
	Close() error
}

// XiaohongshuService 小红书业务服务.
// session 은 QR 로그인한 동일 Browser/Page 를 유지·재사용하는 세션 매니저
// (direction #2). 로그인 후 닫지 않고 CheckLoginStatus/SearchFeeds 가 재사용한다.
// cookie/localStorage 파일은 앱 재시작 후 세션 복원용 fallback 으로 유지.
type XiaohongshuService struct {
	session sessionRunner
	// liveAuth: live 세션 페이지에서 robust auth 를 재검증(캐시 bool 만 신뢰 금지).
	// NewXiaohongshuService 가 checkLiveAuth 로 초기화; 단위 테스트가 stub 주입.
	liveAuth func(context.Context) (bool, error)
	// deleteAuthNow: purgeAuthState 의 파일 정리(기본 nil → 실제 파일 삭제).
	// 단위 테스트가 no-op/counter stub 을 주입해 실제 저장 파일을 보호한다.
	deleteAuthNow func() error
	// fallbackStatus: 저장 인증 복원 경로의 테스트 seam. 기본 nil 이면 실제
	// checkLoginStatusFallback 을 호출한다.
	fallbackStatus func(context.Context) (*LoginStatusResponse, error)
}

// NewXiaohongshuService 创建小红书服务实例.
// onConfirm 콜백으로 로그인 확정 시 cookie/localStorage 를 영속화(restart 복원용).
func NewXiaohongshuService() *XiaohongshuService {
	onConfirm := func(page *rod.Page) {
		if er := saveCookies(page); er != nil {
			logrus.Errorf("failed to save cookies: %v", er)
		}
		if er := saveXhsLocalStorage(page); er != nil {
			logrus.Warnf("failed to save local storage: %v", er)
		}
	}
	svc := &XiaohongshuService{
		session: xhssession.NewManager(
			func() xhssession.BrowserSession { return newXhsBrowserSession() },
			xhssession.WithOnConfirm(onConfirm),
		),
	}
	svc.liveAuth = svc.checkLiveAuth
	return svc
}

// liveAuthProbe: liveAuth stub 이 주입됐으면 그것을, 아니면 실제 checkLiveAuth 를
// 쓴다. 필드가 미설정인 경로(단위 테스트 등)의 nil deref 도 막는다.
func (s *XiaohongshuService) liveAuthProbe(ctx context.Context) (bool, error) {
	if s.liveAuth != nil {
		return s.liveAuth(ctx)
	}
	return s.checkLiveAuth(ctx)
}

// Close: 앱 종료 시 live 세션 브라우저를 정리한다.
func (s *XiaohongshuService) Close() error {
	if s.session != nil {
		return s.session.Close()
	}
	return nil
}

// PublishRequest 发布请求
type PublishRequest struct {
	Title      string   `json:"title" binding:"required"`
	Content    string   `json:"content" binding:"required"`
	Images     []string `json:"images" binding:"required,min=1"`
	Tags       []string `json:"tags,omitempty"`
	ScheduleAt string   `json:"schedule_at,omitempty"` // 定时发布时间，ISO8601格式，为空则立即发布
	IsOriginal bool     `json:"is_original,omitempty"` // 是否声明原创
	Visibility string   `json:"visibility,omitempty"`  // 可见范围: "公开可见"(默认), "仅自己可见", "仅互关好友可见"
	Products   []string `json:"products,omitempty"`    // 商品关键词列表，用于绑定带货商品
}

// LoginStatusResponse 登录状态响应
type LoginStatusResponse struct {
	IsLoggedIn bool   `json:"is_logged_in"`
	Username   string `json:"username,omitempty"`
}

// LoginQrcodeResponse 登录扫码二维码
type LoginQrcodeResponse struct {
	Timeout    string `json:"timeout"`
	IsLoggedIn bool   `json:"is_logged_in"`
	Img        string `json:"img,omitempty"`
}

// PublishResponse 发布响应
type PublishResponse struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Images  int    `json:"images"`
	Status  string `json:"status"`
	PostID  string `json:"post_id,omitempty"`
}

// PublishVideoRequest 发布视频请求（仅支持本地单个视频文件）
type PublishVideoRequest struct {
	Title      string   `json:"title" binding:"required"`
	Content    string   `json:"content" binding:"required"`
	Video      string   `json:"video" binding:"required"`
	Tags       []string `json:"tags,omitempty"`
	ScheduleAt string   `json:"schedule_at,omitempty"` // 定时发布时间，ISO8601格式，为空则立即发布
	Visibility string   `json:"visibility,omitempty"`  // 可见范围: "公开可见"(默认), "仅自己可见", "仅互关好友可见"
	Products   []string `json:"products,omitempty"`    // 商品关键词列表，用于绑定带货商品
}

// PublishVideoResponse 发布视频响应
type PublishVideoResponse struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Video   string `json:"video"`
	Status  string `json:"status"`
	PostID  string `json:"post_id,omitempty"`
}

// FeedsListResponse Feeds列表响应
type FeedsListResponse struct {
	Feeds []xiaohongshu.Feed `json:"feeds"`
	Count int                `json:"count"`
}

// UserProfileResponse 用户主页响应
type UserProfileResponse struct {
	UserBasicInfo xiaohongshu.UserBasicInfo      `json:"userBasicInfo"`
	Interactions  []xiaohongshu.UserInteractions `json:"interactions"`
	Feeds         []xiaohongshu.Feed             `json:"feeds"`
}

// DeleteCookies: live 세션 종료 + cookie/localStorage 파일 삭제로 완전 초기화.
func (s *XiaohongshuService) DeleteCookies(ctx context.Context) error {
	if s.session != nil {
		_ = s.session.Logout()
	}
	return deleteAuthFiles(cookies.GetCookiesFilePath(), localstorage.GetFilePath())
}

// deleteAuthFiles: cookie/localStorage 파일 삭제(명시적 path — 단위 테스트 안전).
// GetCookiesFilePath() 는 /tmp/cookies.json 존재 시 COOKIES_PATH 를 무시하므로
// 테스트는 임시 path 를 직접 넘겨 실제 저장 파일에 손대지 않는다.
func deleteAuthFiles(cookiePath, lsPath string) error {
	if err := cookies.NewLoadCookie(cookiePath).DeleteCookies(); err != nil {
		return err
	}
	// localStorage 도 함께 삭제(세션 완전 초기화).
	return localstorage.NewFileStorer(lsPath).Delete()
}

// purgeAuthState: 인증 단절(ErrAuthLost) 시 live 세션 + 저장 인증 파일을 모두 정리.
// capabilities(Available) 가 즉시 available=false 가 되도록 한다.
// 정리 실패는 검색 실패를 가리키면 안 되므로 로그만 남기고 무시한다.
// deleteAuthNow 가 주입(테스트)됐으면 그것을 쓰고, 아니면 실제 파일을 지운다.
func (s *XiaohongshuService) purgeAuthState() {
	if s.session != nil {
		_ = s.session.Logout()
	}
	fn := s.deleteAuthNow
	if fn == nil {
		fn = func() error {
			return deleteAuthFiles(cookies.GetCookiesFilePath(), localstorage.GetFilePath())
		}
	}
	if err := fn(); err != nil {
		logrus.Warnf("failed to purge auth files on auth loss: %v", err)
	}
}

// CheckLoginStatus: live 세션이 있으면 같은 페이지에서 robust auth 를 재검증한다.
// Manager.LoggedIn 은 로그인 확정 시점의 캐시 bool 이라, 검색/유휴 중 XHS 가 세션을
// 무효화하면 stale true 가 된다. 그래서 캐시만 신뢰하지 않고 live 페이지로 재확인하고,
// 미인증이 확인되면 세션을 invalidate/Close 한다(잘못된 true 차단). live 세션이 없으면
// restart fallback(cookie/localStorage 복원) 한다.
func (s *XiaohongshuService) CheckLoginStatus(ctx context.Context) (*LoginStatusResponse, error) {
	if !s.session.LoggedIn() {
		switch s.session.State() {
		case xhssession.StateQRPending:
			// QR 대기 중에는 StartLogin 이 만든 live 브라우저가 이미 있다.
			// settings UI 의 status polling 때마다 파일 복원용 브라우저를 새로
			// 띄우면 Chrome 창이 반복 생성되므로 즉시 false 만 반환한다.
			return &LoginStatusResponse{IsLoggedIn: false, Username: configs.Username}, nil
		case xhssession.StateLoggedIn:
			// LoggedIn 확인 직후 QR 확인 goroutine 이 로그인 전이를 완료한 경우다.
			// fallback 대신 아래 live auth 재검증 경로를 사용한다.
		default:
			return s.loginStatusFallbackProbe(ctx)
		}
	}

	authed, err := s.liveAuthProbe(ctx)
	switch {
	case err == nil && authed:
		return &LoginStatusResponse{IsLoggedIn: true, Username: configs.Username}, nil
	case err == nil && !authed:
		// live 페이지가 미인증(세션 단절) 확인 → 세션과 저장 인증을 함께
		// 무효화한다. 파일을 남기면 다음 status 호출이 fallback 으로 같은
		// 무효 세션을 다시 재생할 수 있다.
		s.purgeAuthState()
		return &LoginStatusResponse{IsLoggedIn: false, Username: configs.Username}, nil
	default:
		// 일시적 에러(네트워크/ctx) 는 단절 확정이 아니므로 세션 유지 + false.
		return &LoginStatusResponse{IsLoggedIn: false, Username: configs.Username}, nil
	}
}

func (s *XiaohongshuService) loginStatusFallbackProbe(ctx context.Context) (*LoginStatusResponse, error) {
	if s.fallbackStatus != nil {
		return s.fallbackStatus(ctx)
	}
	return s.checkLoginStatusFallback(ctx)
}

// checkLiveAuth: live 세션 페이지에서 robust auth(IsAuthenticated) 재검증.
// Manager.WithPage 가 세션 접근을 직렬화하며, fn 안에서 manager 재진입이 없어
// lock deadlock 도 없다. WithPage 가 ErrNoSession(세션 없음/만료) 이면 (false, err).
func (s *XiaohongshuService) checkLiveAuth(ctx context.Context) (bool, error) {
	var authed bool
	err := s.session.WithPage(ctx, func(page *rod.Page) error {
		// 현재 live 페이지를 그대로 검사한다. CheckLoginStatus 는 /explore 로
		// navigate 하므로 검색 중인 페이지를 바꾸고 실제 auth-loss 상태를
		// 가릴 수 있다.
		ok, e := xiaohongshu.NewLogin(page).IsAuthenticated(ctx)
		authed = ok
		return e
	})
	return authed, err
}

// checkLoginStatusFallback: live 세션이 없을 때(앱 재시작 등) 파일 기반 복원 시도.
func (s *XiaohongshuService) checkLoginStatusFallback(ctx context.Context) (*LoginStatusResponse, error) {
	// fast path: 저장된 쿠키가 없으면 무조건 미로그인. 브라우저 기동 생략(crash 회피).
	if !xhsHasSavedCookies() {
		return &LoginStatusResponse{IsLoggedIn: false, Username: configs.Username}, nil
	}

	b := newBrowser()
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	// 페이지 최초 네비게이션 전 localStorage 복원(세션 재사용). 실패는 graceful 스킵.
	if err := restoreXhsLocalStorage(page); err != nil {
		logrus.Warnf("failed to restore local storage: %v", err)
	}

	loginAction := xiaohongshu.NewLogin(page)
	isLoggedIn, err := loginAction.CheckLoginStatus(ctx)
	if err != nil {
		return nil, err
	}
	return &LoginStatusResponse{IsLoggedIn: isLoggedIn, Username: configs.Username}, nil
}

// savedCookiesAt: 주어진 경로에 0보다 큰 크기의 쿠키 파일이 존재하면 true.
// 임시 경로로 단위 테스트 가능하도록 path 인자로 분리했다.
func savedCookiesAt(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir() && info.Size() > 0
}

// xhsHasSavedCookies: XHS 쿠키 파일 존재 여부(savedCookiesAt 의 실제 경로 바인딩).
func xhsHasSavedCookies() bool {
	return savedCookiesAt(cookies.GetCookiesFilePath())
}

// GetLoginQrcode: 세션 매니저로 새 QR 세션 시작. 동일 Browser/Page 를 유지해
// 로그인 후에도 닫지 않는다(direction #2). QR 만료/재시도 시 반복 호출하면
// 기존 세션을 안전하게 교체한다.
func (s *XiaohongshuService) GetLoginQrcode(ctx context.Context) (*LoginQrcodeResponse, error) {
	img, loggedIn, err := s.session.StartLogin(ctx)
	if err != nil {
		return nil, err
	}
	timeout := 4 * time.Minute
	return &LoginQrcodeResponse{
		Timeout: func() string {
			if loggedIn {
				return "0s"
			}
			return timeout.String()
		}(),
		Img:        img,
		IsLoggedIn: loggedIn,
	}, nil
}

// PublishContent 发布内容
func (s *XiaohongshuService) PublishContent(ctx context.Context, req *PublishRequest) (*PublishResponse, error) {
	// 验证标题长度（小红书限制：最大20个字）
	if xhsutil.CalcTitleLength(req.Title) > 20 {
		return nil, fmt.Errorf("标题长度超过限制")
	}

	// 处理图片：下载URL图片或使用本地路径
	imagePaths, err := s.processImages(req.Images)
	if err != nil {
		return nil, err
	}

	// 解析定时发布时间
	var scheduleTime *time.Time
	if req.ScheduleAt != "" {
		t, err := time.Parse(time.RFC3339, req.ScheduleAt)
		if err != nil {
			return nil, fmt.Errorf("定时发布时间格式错误，请使用 ISO8601 格式: %v", err)
		}

		// 校验定时发布时间范围：1小时至14天
		now := time.Now()
		minTime := now.Add(1 * time.Hour)
		maxTime := now.Add(14 * 24 * time.Hour)

		if t.Before(minTime) {
			return nil, fmt.Errorf("定时发布时间必须至少在1小时后，当前设置: %s，最早可选: %s",
				t.Format("2006-01-02 15:04"), minTime.Format("2006-01-02 15:04"))
		}
		if t.After(maxTime) {
			return nil, fmt.Errorf("定时发布时间不能超过14天，当前设置: %s，最晚可选: %s",
				t.Format("2006-01-02 15:04"), maxTime.Format("2006-01-02 15:04"))
		}

		scheduleTime = &t
		logrus.Infof("设置定时发布时间: %s", t.Format("2006-01-02 15:04"))
	}

	// 构建发布内容
	content := xiaohongshu.PublishImageContent{
		Title:        req.Title,
		Content:      req.Content,
		Tags:         req.Tags,
		ImagePaths:   imagePaths,
		ScheduleTime: scheduleTime,
		IsOriginal:   req.IsOriginal,
		Visibility:   req.Visibility,
		Products:     req.Products,
	}

	// 执行发布
	if err := s.publishContent(ctx, content); err != nil {
		logrus.Errorf("发布内容失败: title=%s %v", content.Title, err)
		return nil, err
	}

	response := &PublishResponse{
		Title:   req.Title,
		Content: req.Content,
		Images:  len(imagePaths),
		Status:  "发布完成",
	}

	return response, nil
}

// processImages 处理图片列表，支持URL下载和本地路径
func (s *XiaohongshuService) processImages(images []string) ([]string, error) {
	processor := downloader.NewImageProcessor()
	return processor.ProcessImages(images)
}

// publishContent 执行内容发布
func (s *XiaohongshuService) publishContent(ctx context.Context, content xiaohongshu.PublishImageContent) error {
	b := newBrowser()
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action, err := xiaohongshu.NewPublishImageAction(page)
	if err != nil {
		return err
	}

	// 执行发布
	return action.Publish(ctx, content)
}

// PublishVideo 发布视频（本地文件）
func (s *XiaohongshuService) PublishVideo(ctx context.Context, req *PublishVideoRequest) (*PublishVideoResponse, error) {
	// 标题长度校验（小红书限制：最大20个字）
	if xhsutil.CalcTitleLength(req.Title) > 20 {
		return nil, fmt.Errorf("标题长度超过限制")
	}

	// 本地视频文件校验
	if req.Video == "" {
		return nil, fmt.Errorf("必须提供本地视频文件")
	}
	if _, err := os.Stat(req.Video); err != nil {
		return nil, fmt.Errorf("视频文件不存在或不可访问: %v", err)
	}

	// 解析定时发布时间
	var scheduleTime *time.Time
	if req.ScheduleAt != "" {
		t, err := time.Parse(time.RFC3339, req.ScheduleAt)
		if err != nil {
			return nil, fmt.Errorf("定时发布时间格式错误，请使用 ISO8601 格式: %v", err)
		}

		// 校验定时发布时间范围：1小时至14天
		now := time.Now()
		minTime := now.Add(1 * time.Hour)
		maxTime := now.Add(14 * 24 * time.Hour)

		if t.Before(minTime) {
			return nil, fmt.Errorf("定时发布时间必须至少在1小时后，当前设置: %s，最早可选: %s",
				t.Format("2006-01-02 15:04"), minTime.Format("2006-01-02 15:04"))
		}
		if t.After(maxTime) {
			return nil, fmt.Errorf("定时发布时间不能超过14天，当前设置: %s，最晚可选: %s",
				t.Format("2006-01-02 15:04"), maxTime.Format("2006-01-02 15:04"))
		}

		scheduleTime = &t
		logrus.Infof("设置定时发布时间: %s", t.Format("2006-01-02 15:04"))
	}

	// 构建发布内容
	content := xiaohongshu.PublishVideoContent{
		Title:        req.Title,
		Content:      req.Content,
		Tags:         req.Tags,
		VideoPath:    req.Video,
		ScheduleTime: scheduleTime,
		Visibility:   req.Visibility,
		Products:     req.Products,
	}

	// 执行发布
	if err := s.publishVideo(ctx, content); err != nil {
		return nil, err
	}

	resp := &PublishVideoResponse{
		Title:   req.Title,
		Content: req.Content,
		Video:   req.Video,
		Status:  "发布完成",
	}
	return resp, nil
}

// publishVideo 执行视频发布
func (s *XiaohongshuService) publishVideo(ctx context.Context, content xiaohongshu.PublishVideoContent) error {
	b := newBrowser()
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action, err := xiaohongshu.NewPublishVideoAction(page)
	if err != nil {
		return err
	}

	return action.PublishVideo(ctx, content)
}

// ListFeeds 获取Feeds列表
func (s *XiaohongshuService) ListFeeds(ctx context.Context) (*FeedsListResponse, error) {
	b := newBrowser()
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	// 创建 Feeds 列表 action
	action := xiaohongshu.NewFeedsListAction(page)

	// 获取 Feeds 列表
	feeds, err := action.GetFeedsList(ctx)
	if err != nil {
		logrus.Errorf("获取 Feeds 列表失败: %v", err)
		return nil, err
	}

	response := &FeedsListResponse{
		Feeds: feeds,
		Count: len(feeds),
	}

	return response, nil
}

// SearchFeeds: live 인증 세션 우선 재사용(direction #2). 세션이 없으면
// restart fallback(cookie/localStorage 복원) 시도. live 페이지에서 검색 에러는
// 그대로 반환하고, ErrNoSession 일 때만 fallback 한다.
func (s *XiaohongshuService) SearchFeeds(ctx context.Context, keyword string, filters ...xiaohongshu.FilterOption) (*FeedsListResponse, error) {
	var feeds []xiaohongshu.Feed
	err := s.session.WithPage(ctx, func(page *rod.Page) error {
		action := xiaohongshu.NewSearchAction(page)
		f, e := action.Search(ctx, keyword, filters...)
		if e != nil {
			return e
		}
		feeds = f
		return nil
	})
	if err == nil {
		return &FeedsListResponse{Feeds: feeds, Count: len(feeds)}, nil
	}
	// 인증 단절(ErrAuthLost): live 세션 + 저장 cookie/localStorage 를 정리해
	// capabilities 가 즉시 available=false 가 되게 한다. generic fallback 재시도 금지
	// (무효 세션으로는 어차피 실패하므로, 60s timeout 재발도 막는다).
	if errors.Is(err, xhserrors.ErrAuthLost) {
		s.purgeAuthState()
		return nil, err
	}
	if !errors.Is(err, xhssession.ErrNoSession) {
		return nil, err
	}
	return s.searchFeedsFallback(ctx, keyword, filters...)
}

// searchFeedsFallback: live 세션이 없을 때(앱 재시작 등) 임시 브라우저 + localStorage 복원.
func (s *XiaohongshuService) searchFeedsFallback(ctx context.Context, keyword string, filters ...xiaohongshu.FilterOption) (*FeedsListResponse, error) {
	b := newBrowser()
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	// 페이지 최초 네비게이션 전 localStorage 복원(검색 세션 재사용). 실패는 graceful 스킵.
	if err := restoreXhsLocalStorage(page); err != nil {
		logrus.Warnf("failed to restore local storage: %v", err)
	}

	action := xiaohongshu.NewSearchAction(page)
	feeds, err := action.Search(ctx, keyword, filters...)
	if err != nil {
		return nil, err
	}
	return &FeedsListResponse{Feeds: feeds, Count: len(feeds)}, nil
}

// GetFeedDetail 获取Feed详情
func (s *XiaohongshuService) GetFeedDetail(ctx context.Context, feedID, xsecToken string, loadAllComments bool) (*FeedDetailResponse, error) {
	return s.GetFeedDetailWithConfig(ctx, feedID, xsecToken, loadAllComments, xiaohongshu.DefaultCommentLoadConfig())
}

// GetFeedDetailWithConfig 使用配置获取Feed详情
func (s *XiaohongshuService) GetFeedDetailWithConfig(ctx context.Context, feedID, xsecToken string, loadAllComments bool, config xiaohongshu.CommentLoadConfig) (*FeedDetailResponse, error) {
	b := newBrowser()
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	// 创建 Feed 详情 action
	action := xiaohongshu.NewFeedDetailAction(page)

	// 获取 Feed 详情
	result, err := action.GetFeedDetailWithConfig(ctx, feedID, xsecToken, loadAllComments, config)
	if err != nil {
		return nil, err
	}

	response := &FeedDetailResponse{
		FeedID: feedID,
		Data:   result,
	}
	// 영상 URL 평탄화: 프론트가 간단히 쓸 수 있도록 최상위 필드로 노출
	if result.Note.Video != nil {
		response.VideoURL = result.Note.Video.VideoURL()
	}

	return response, nil
}

// UserProfile 获取用户信息
func (s *XiaohongshuService) UserProfile(ctx context.Context, userID, xsecToken string) (*UserProfileResponse, error) {
	b := newBrowser()
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewUserProfileAction(page)

	result, err := action.UserProfile(ctx, userID, xsecToken)
	if err != nil {
		return nil, err
	}
	response := &UserProfileResponse{
		UserBasicInfo: result.UserBasicInfo,
		Interactions:  result.Interactions,
		Feeds:         result.Feeds,
	}

	return response, nil

}

// PostCommentToFeed 发表评论到Feed
func (s *XiaohongshuService) PostCommentToFeed(ctx context.Context, feedID, xsecToken, content string) (*PostCommentResponse, error) {
	b := newBrowser()
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewCommentFeedAction(page)

	if err := action.PostComment(ctx, feedID, xsecToken, content); err != nil {
		return nil, err
	}

	return &PostCommentResponse{FeedID: feedID, Success: true, Message: "评论发表成功"}, nil
}

// LikeFeed 点赞笔记
func (s *XiaohongshuService) LikeFeed(ctx context.Context, feedID, xsecToken string) (*ActionResult, error) {
	b := newBrowser()
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewLikeAction(page)
	if err := action.Like(ctx, feedID, xsecToken); err != nil {
		return nil, err
	}
	return &ActionResult{FeedID: feedID, Success: true, Message: "点赞成功或已点赞"}, nil
}

// UnlikeFeed 取消点赞笔记
func (s *XiaohongshuService) UnlikeFeed(ctx context.Context, feedID, xsecToken string) (*ActionResult, error) {
	b := newBrowser()
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewLikeAction(page)
	if err := action.Unlike(ctx, feedID, xsecToken); err != nil {
		return nil, err
	}
	return &ActionResult{FeedID: feedID, Success: true, Message: "取消点赞成功或未点赞"}, nil
}

// FavoriteFeed 收藏笔记
func (s *XiaohongshuService) FavoriteFeed(ctx context.Context, feedID, xsecToken string) (*ActionResult, error) {
	b := newBrowser()
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewFavoriteAction(page)
	if err := action.Favorite(ctx, feedID, xsecToken); err != nil {
		return nil, err
	}
	return &ActionResult{FeedID: feedID, Success: true, Message: "收藏成功或已收藏"}, nil
}

// UnfavoriteFeed 取消收藏笔记
func (s *XiaohongshuService) UnfavoriteFeed(ctx context.Context, feedID, xsecToken string) (*ActionResult, error) {
	b := newBrowser()
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewFavoriteAction(page)
	if err := action.Unfavorite(ctx, feedID, xsecToken); err != nil {
		return nil, err
	}
	return &ActionResult{FeedID: feedID, Success: true, Message: "取消收藏成功或未收藏"}, nil
}

// ReplyCommentToFeed 回复指定评论
func (s *XiaohongshuService) ReplyCommentToFeed(ctx context.Context, feedID, xsecToken, commentID, userID, content string) (*ReplyCommentResponse, error) {
	b := newBrowser()
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewCommentFeedAction(page)

	if err := action.ReplyToComment(ctx, feedID, xsecToken, commentID, userID, content); err != nil {
		return nil, err
	}

	return &ReplyCommentResponse{
		FeedID:          feedID,
		TargetCommentID: commentID,
		TargetUserID:    userID,
		Success:         true,
		Message:         "评论回复成功",
	}, nil
}

func newBrowser() *headless_browser.Browser {
	return browser.NewBrowser(configs.IsHeadless(), browser.WithBinPath(configs.GetBinPath()))
}

func saveCookies(page *rod.Page) error {
	cks, err := page.Browser().GetCookies()
	if err != nil {
		return err
	}

	data, err := json.Marshal(cks)
	if err != nil {
		return err
	}

	cookieLoader := cookies.NewLoadCookie(cookies.GetCookiesFilePath())
	return cookieLoader.SaveCookies(data)
}

// withBrowserPage 执行需要浏览器页面的操作的通用函数
func withBrowserPage(fn func(*rod.Page) error) error {
	b := newBrowser()
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	return fn(page)
}

// GetMyProfile 获取当前登录用户的个人信息
func (s *XiaohongshuService) GetMyProfile(ctx context.Context) (*UserProfileResponse, error) {
	var result *xiaohongshu.UserProfileResponse
	var err error

	err = withBrowserPage(func(page *rod.Page) error {
		action := xiaohongshu.NewUserProfileAction(page)
		result, err = action.GetMyProfileViaSidebar(ctx)
		return err
	})

	if err != nil {
		return nil, err
	}

	response := &UserProfileResponse{
		UserBasicInfo: result.UserBasicInfo,
		Interactions:  result.Interactions,
		Feeds:         result.Feeds,
	}

	return response, nil
}
