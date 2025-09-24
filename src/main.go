package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"claude-mimic-gateway/config"
	"claude-mimic-gateway/proxy"
	"claude-mimic-gateway/utils"
)

// defaultConfigPath 默认配置文件路径
const defaultConfigPath = "config.yaml"

// main 程序入口点，初始化并启动Claude Mimic Gateway
//
// 负责配置加载、系统提示词加载、服务器创建和启动等核心初始化流程
func main() {
	utils.LogInfoLegacy("Claude Mimic Gateway 启动中...")

	// 获取配置文件路径
	configPath := getConfigPath()
	utils.LogDebugLegacy("使用配置文件: " + configPath)

	// 加载配置
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		utils.LogErrorLegacy("加载配置失败: " + err.Error())
		os.Exit(1)
	}
	utils.LogSuccessLegacy("配置加载成功")

	// 加载系统提示词
	if count, err := utils.LoadSystemPromptsFromDefault(); err != nil {
		utils.LogErrorLegacy("加载系统提示词失败: " + err.Error())
		// 不退出程序，允许在没有系统提示词的情况下运行
	} else {
		if count > 0 {
			utils.LogSuccessLegacy(fmt.Sprintf("系统提示词加载成功，共加载 %d 个模型", count))
			// 显示已加载的模型列表
			models := utils.GetAvailableModels()
			for _, model := range models {
				utils.LogDebugLegacy("  - " + model)
			}
		} else {
			utils.LogInfoLegacy("未发现系统提示词文件")
		}
	}

	// 创建代理处理器
	proxyHandler := proxy.NewProxyHandler(cfg)
	utils.LogDebugLegacy("代理处理器已创建")

	// 创建HTTP服务器
	server := createHTTPServer(cfg, proxyHandler)
	utils.LogInfoLegacy(fmt.Sprintf("HTTP服务器已创建，监听端口: %d", cfg.Server.Port))

	// 启动服务器
	go func() {
		utils.LogSuccessLegacy(fmt.Sprintf("Claude Mimic Gateway 运行在端口 %d", cfg.Server.Port))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			utils.LogErrorLegacy("服务器启动失败: " + err.Error())
			os.Exit(1)
		}
	}()

	// 等待中断信号
	waitForShutdown(server)
}

// getConfigPath 获取配置文件路径
//
// 返回值:
//   - string: 配置文件路径
func getConfigPath() string {
	if len(os.Args) > 1 {
		return os.Args[1]
	}
	return defaultConfigPath
}

// createHTTPServer 创建HTTP服务器实例
//
// 参数:
//   - cfg: 配置实例
//   - proxyHandler: 代理处理器实例
//
// 返回值:
//   - *http.Server: 配置好的HTTP服务器实例
func createHTTPServer(cfg *config.Config, proxyHandler *proxy.ProxyHandler) *http.Server {
	mux := http.NewServeMux()

	setupRoutes(mux, proxyHandler)

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      loggingMiddleware(mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 600 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return server
}

// setupRoutes 设置HTTP路由
//
// 参数:
//   - mux: HTTP路由复用器
//   - proxyHandler: 代理处理器实例
func setupRoutes(mux *http.ServeMux, proxyHandler *proxy.ProxyHandler) {

	mux.HandleFunc("/v1/messages", proxyHandler.HandleRequest)

	mux.HandleFunc("/health", handleHealthCheck)

	utils.LogDebugLegacy("路由设置完成")
}

// handleHealthCheck 处理健康检查请求
//
// 参数:
//   - w: HTTP响应写入器
//   - r: HTTP请求对象
func handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok","service":"claude-mimic-gateway"}`))
}

// loggingMiddleware HTTP请求日志中间件
//
// 参数:
//   - next: 下一个HTTP处理器
//
// 返回值:
//   - http.Handler: 包装了日志功能的HTTP处理器
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// 包装ResponseWriter以捕获状态码
		wrappedWriter := &responseWriter{ResponseWriter: w, statusCode: 200}

		// 调用下一个处理器
		next.ServeHTTP(wrappedWriter, r)

		// 记录请求日志
		duration := time.Since(start)
		logMessage := fmt.Sprintf("%s %s - %d - %v",
			r.Method, r.URL.Path, wrappedWriter.statusCode, duration)

		if wrappedWriter.statusCode >= 400 {
			utils.LogErrorLegacy("请求处理失败: " + logMessage)
		} else {
			utils.LogDebugLegacy("请求处理完成: " + logMessage)
		}
	})
}

// responseWriter 响应写入器包装器，用于捕获HTTP状态码
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader 写入HTTP状态码
//
// 参数:
//   - code: HTTP状态码
func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Flush 实现http.Flusher接口，支持流式传输
func (rw *responseWriter) Flush() {
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// waitForShutdown 等待关闭信号并优雅关闭服务器
//
// 参数:
//   - server: HTTP服务器实例
func waitForShutdown(server *http.Server) {
	// 创建信号通道
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// 等待信号
	sig := <-quit
	utils.LogInfoLegacy("收到关闭信号: " + sig.String())

	// 设置关闭超时
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 优雅关闭服务器
	if err := server.Shutdown(ctx); err != nil {
		utils.LogErrorLegacy("服务器关闭失败: " + err.Error())
		os.Exit(1)
	}

	utils.LogSuccessLegacy("Claude Mimic Gateway 已关闭")
}