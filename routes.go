package main

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// setupRoutes 设置路由配置
func setupRoutes(appServer *AppServer) *gin.Engine {
	// 设置 Gin 模式
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()
	// Signed media URL query에는 token/cookie가 포함될 수 있으므로 path만 기록한다.
	router.Use(gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		return fmt.Sprintf("[GIN] %v | %3d | %13v | %-7s %s\n",
			param.TimeStamp.Format("2006/01/02 - 15:04:05"),
			param.StatusCode,
			param.Latency,
			param.Method,
			param.Request.URL.Path,
		)
	}))
	router.Use(gin.Recovery())

	// 添加中间件
	router.Use(errorHandlingMiddleware())
	router.Use(corsMiddleware())

	// 健康检查
	router.GET("/health", healthHandler)

	// MCP 端点 - 使用官方 SDK 的 Streamable HTTP Handler
	mcpHandler := mcp.NewStreamableHTTPHandler(
		func(r *http.Request) *mcp.Server {
			return appServer.mcpServer
		},
		&mcp.StreamableHTTPOptions{
			JSONResponse: true, // 支持 JSON 响应
		},
	)
	router.Any("/mcp", gin.WrapH(mcpHandler))
	router.Any("/mcp/*path", gin.WrapH(mcpHandler))

	// API 路由组
	api := router.Group("/api/v1")
	{
		api.GET("/login/status", appServer.checkLoginStatusHandler)
		api.GET("/login/qrcode", appServer.getLoginQrcodeHandler)
		api.DELETE("/login/cookies", appServer.deleteCookiesHandler)
		api.POST("/publish", appServer.publishHandler)
		api.POST("/publish_video", appServer.publishVideoHandler)
		api.GET("/feeds/list", appServer.listFeedsHandler)
		api.GET("/feeds/search", appServer.searchFeedsHandler)
		api.POST("/feeds/search", appServer.searchFeedsHandler)
		api.POST("/feeds/detail", appServer.getFeedDetailHandler)
		api.POST("/user/profile", appServer.userProfileHandler)
		api.POST("/feeds/comment", appServer.postCommentHandler)
		api.POST("/feeds/comment/reply", appServer.replyCommentHandler)
		api.GET("/user/me", appServer.myProfileHandler)
		api.POST("/search", appServer.unifiedSearchHandler)
		api.GET("/search/capabilities", appServer.searchCapabilitiesHandler)
		api.GET("/download", appServer.downloadHandler)
	}

	// 한국어 검색 페이지 정적 서빙 (같은 출처)
	router.Static("/static", "./web")
	router.GET("/", func(c *gin.Context) {
		c.File("./web/index.html")
	})
	// 로컬 설정 페이지(플랫폼 로그인). 검색 화면과 분리.
	router.GET("/settings", func(c *gin.Context) {
		c.File("./web/settings.html")
	})

	return router
}
