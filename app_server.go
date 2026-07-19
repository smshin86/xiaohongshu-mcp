package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/search"
)

// AppServer 应用服务器结构体，封装所有服务和处理器
type AppServer struct {
	xiaohongshuService  *XiaohongshuService
	xhsDownloadResolver xhsDownloadResolver
	xhsDownloadOpener   xhsDownloadOpener
	sidecarDownloader   sidecarDownloader
	sidecarKeywords     sidecarKeywords
	downloadGuard       *downloadURLGuard
	aggregator          *search.AggregatorService
	mcpServer           *mcp.Server
	router              *gin.Engine
	httpServer          *http.Server
}

// NewAppServer 创建新的应用服务器实例
func NewAppServer(xiaohongshuService *XiaohongshuService) *AppServer {
	appServer := &AppServer{
		xiaohongshuService: xiaohongshuService,
	}

	// 初始化 MCP Server（需要在创建 appServer 之后，因为工具注册需要访问 appServer）
	appServer.mcpServer = InitMCPServer(appServer)

	// 통합 검색 aggregator 조립(SIDECAR_URL 환경변수로 사이드카 주소 덮어쓰기).
	sidecarURL := "http://127.0.0.1:18061"
	if v := os.Getenv("SIDECAR_URL"); v != "" {
		sidecarURL = v
	}
	sidecar := NewSidecarClient(sidecarURL, 0)
	downloadGuard := newDownloadURLGuard()
	appServer.xhsDownloadResolver = xiaohongshuService
	appServer.xhsDownloadOpener = &guardedXHSDownloadOpener{
		client: downloadGuard.NewClient("xiaohongshu"),
	}
	appServer.sidecarDownloader = sidecar
	appServer.sidecarKeywords = sidecar
	appServer.downloadGuard = downloadGuard
	appServer.aggregator = search.NewAggregatorService(map[string]search.VideoAdapter{
		"xiaohongshu": NewXhsAdapter(xiaohongshuService),
		"douyin":      NewDouyinAdapter(sidecar),
		"tiktok":      NewTikTokAdapter(sidecar),
	})

	return appServer
}

// Start 启动服务器
func (s *AppServer) Start(port string) error {
	s.router = setupRoutes(s)

	s.httpServer = &http.Server{
		Addr:    port,
		Handler: s.router,
	}

	// 启动服务器的 goroutine
	go func() {
		logrus.Infof("启动 HTTP 服务器: %s", port)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logrus.Errorf("服务器启动失败: %v", err)
			os.Exit(1)
		}
	}()

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logrus.Infof("正在关闭服务器...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		logrus.Warnf("等待连接关闭超时，强制退出: %v", err)
	} else {
		logrus.Infof("服务器已优雅关闭")
	}

	return nil
}
