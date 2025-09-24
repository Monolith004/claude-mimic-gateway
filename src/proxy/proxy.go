package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"claude-mimic-gateway/config"
	"claude-mimic-gateway/utils"
)

// ProxyHandler 代理处理器结构体
type ProxyHandler struct {
	config *config.Config
	client *http.Client
}

// NewProxyHandler 创建新的代理处理器实例
//
// 参数:
//   - cfg: 配置实例
//
// 返回值:
//   - *ProxyHandler: 代理处理器实例
func NewProxyHandler(cfg *config.Config) *ProxyHandler {
	// 创建自定义DialContext函数，禁用Nagle算法
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	dialContext := func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}

		// 禁用Nagle算法（TCP_NODELAY）
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			if err := tcpConn.SetNoDelay(true); err != nil {
				utils.LogErrorLegacy("设置TCP_NODELAY失败: " + err.Error())
			} else {
				utils.LogDebugLegacy("已禁用Nagle算法，启用TCP_NODELAY")
			}
		}

		return conn, nil
	}

	// 创建HTTP/1.1传输层，禁用HTTP/2
	transport := &http.Transport{
		DialContext: dialContext,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: false,
		},
		// 连接池设置，提升性能
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 90 * time.Second,
		// 禁用压缩，避免影响流式传输
		DisableCompression: true,
		// 强制使用HTTP/1.1
		ForceAttemptHTTP2: false,
		TLSNextProto:      make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
	}

	utils.LogDebugLegacy("已配置HTTP/1.1传输层，禁用Nagle算法")

	return &ProxyHandler{
		config: cfg,
		client: &http.Client{
			Transport: transport,
			Timeout:   600 * time.Second, // 与X-Stainless-Timeout保持一致
		},
	}
}

// HandleRequest 处理代理请求的主要方法
//
// 参数:
//   - w: HTTP响应写入器
//   - r: HTTP请求对象
func (p *ProxyHandler) HandleRequest(w http.ResponseWriter, r *http.Request) {
	// 生成任务ID
	taskID := utils.GenerateTaskID()
	utils.LogInfo(taskID, "收到下游请求: " + r.Method + " " + r.URL.Path)

	// 初始化日志数据
	logData := &utils.RequestLogData{
		TaskID:    taskID,
		Timestamp: time.Now().Format("2006-01-02 15:04:05"),
		DownstreamRequest: &utils.RequestDetails{
			Method:  r.Method,
			URL:     r.URL.String(),
			Headers: make(map[string]string),
		},
	}

	// 记录下游请求头
	for key, values := range r.Header {
		logData.DownstreamRequest.Headers[key] = strings.Join(values, ", ")
	}

	// 验证密钥
	if !p.validateAuth(r) {
		utils.LogError(taskID, "密钥验证失败")
		logData.Success = false
		logData.Error = "密钥验证失败"
		utils.SaveRequestLog(logData)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	utils.LogDebug(taskID, "密钥验证成功")

	// 读取原始请求体
	body, err := io.ReadAll(r.Body)
	if err != nil {
		utils.LogError(taskID, "读取请求体失败: " + err.Error())
		logData.Success = false
		logData.Error = "读取请求体失败: " + err.Error()
		utils.SaveRequestLog(logData)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// 记录下游请求体
	logData.DownstreamRequest.Body = string(body)

	// 解析请求体中的stream参数
	isStream := p.parseStreamParameter(body)
	utils.LogDebug(taskID, fmt.Sprintf("检测到stream参数: %t", isStream))

	// 转换请求体
	transformedBody, err := utils.TransformRequestBody(body)
	if err != nil {
		utils.LogError(taskID, "转换请求体失败: " + err.Error())
		logData.Success = false
		logData.Error = "转换请求体失败: " + err.Error()
		utils.SaveRequestLog(logData)

		// 检查是否为格式异常错误，返回对应状态码
		if err.Error() == "格式异常" {
			http.Error(w, "格式异常", http.StatusUnauthorized)
		} else {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}
	utils.LogDebug(taskID, "请求体转换成功")

	// 创建上游请求
	upstreamReq, err := p.createUpstreamRequest(r, transformedBody)
	if err != nil {
		utils.LogError(taskID, "创建上游请求失败: " + err.Error())
		logData.Success = false
		logData.Error = "创建上游请求失败: " + err.Error()
		utils.SaveRequestLog(logData)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// 记录上游请求信息
	logData.UpstreamRequest = &utils.RequestDetails{
		Method:          upstreamReq.Method,
		URL:             upstreamReq.URL.String(),
		Headers:         make(map[string]string),
		Body:            string(transformedBody), // 保持向后兼容
		OriginalBody:    string(body),           // 转换前的原始请求体
		TransformedBody: string(transformedBody), // 转换后的请求体
	}

	// 记录上游请求头
	for key, values := range upstreamReq.Header {
		logData.UpstreamRequest.Headers[key] = strings.Join(values, ", ")
	}

	// 发起上游请求
	utils.LogInfo(taskID, "向上游发起请求: " + upstreamReq.URL.String())
	upstreamResp, err := p.client.Do(upstreamReq)
	if err != nil {
		utils.LogError(taskID, "上游请求失败: " + err.Error())
		logData.Success = false
		logData.Error = "上游请求失败: " + err.Error()
		utils.SaveRequestLog(logData)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}
	defer upstreamResp.Body.Close()

	utils.LogInfo(taskID, "收到上游响应，状态码: " + upstreamResp.Status)

	// 初始化上游响应信息
	logData.UpstreamResponse = &utils.ResponseDetails{
		StatusCode: upstreamResp.StatusCode,
		Headers:    make(map[string]string),
	}

	// 记录上游响应头
	for key, values := range upstreamResp.Header {
		logData.UpstreamResponse.Headers[key] = strings.Join(values, ", ")
	}

	// 根据stream参数选择不同的处理方式
	if isStream {
		// 流式处理：边转发边记录
		utils.LogDebug(taskID, "使用流式处理模式")
		p.handleStreamResponse(w, upstreamResp, logData, taskID)
	} else {
		// 非流式处理：读取完整响应体
		utils.LogDebug(taskID, "使用非流式处理模式")
		p.handleNonStreamResponse(w, upstreamResp, logData, taskID)
	}
}

// validateAuth 验证请求密钥，支持多种认证头格式
//
// 参数:
//   - r: HTTP请求对象
//
// 返回值:
//   - bool: 验证结果
func (p *ProxyHandler) validateAuth(r *http.Request) bool {
	// 检查 Authorization 头
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		// 支持Bearer token格式
		if strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimPrefix(authHeader, "Bearer ")
			return token == p.config.Auth.Key
		}
		// 直接比较Authorization头
		if authHeader == p.config.Auth.Key {
			return true
		}
	}

	// 检查 x-api-key 头
	apiKeyHeader := r.Header.Get("x-api-key")
	if apiKeyHeader != "" {
		return apiKeyHeader == p.config.Auth.Key
	}

	// 检查 X-API-Key 头（大小写兼容）
	apiKeyHeaderCap := r.Header.Get("X-API-Key")
	if apiKeyHeaderCap != "" {
		return apiKeyHeaderCap == p.config.Auth.Key
	}

	return false
}

// createUpstreamRequest 创建上游请求
//
// 参数:
//   - originalReq: 原始HTTP请求
//   - body: 转换后的请求体
//
// 返回值:
//   - *http.Request: 创建的上游请求
//   - error: 可能的错误
func (p *ProxyHandler) createUpstreamRequest(originalReq *http.Request, body []byte) (*http.Request, error) {
	// 直接使用配置文件中的完整上游URL，不进行路径拼接
	upstreamURL := p.config.Upstream.URL

	// 创建新请求，使用完整的上游URL
	req, err := http.NewRequest(originalReq.Method, upstreamURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	// 设置Claude Code标准请求头
	p.setClaudeCodeHeaders(req)

	return req, nil
}

// setClaudeCodeHeaders 设置Claude Code标准请求头
//
// 参数:
//   - req: HTTP请求对象
func (p *ProxyHandler) setClaudeCodeHeaders(req *http.Request) {
	// 设置标准的Claude Code请求头
	headers := map[string]string{
		"Accept":                                    "application/json",
		"X-Stainless-Retry-Count":                  "0",
		"X-Stainless-Timeout":                      "600",
		"X-Stainless-Lang":                         "js",
		"X-Stainless-Package-Version":              "0.60.0",
		"X-Stainless-OS":                           "Windows",
		"X-Stainless-Arch":                         "x64",
		"X-Stainless-Runtime":                      "node",
		"X-Stainless-Runtime-Version":              "v22.13.0",
		"anthropic-dangerous-direct-browser-access": "true",
		"anthropic-version":                        "2023-06-01",
		"x-app":                                    "cli",
		"User-Agent":                               "claude-cli/1.0.108 (external, cli)",
		"content-type":                             "application/json",
		"anthropic-beta":                           "claude-code-20250219,interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14",
		"x-stainless-helper-method":                "stream",
		"accept-language":                          "*",
		"sec-fetch-mode":                           "cors",
		"Authorization":                            "Bearer " + p.config.Upstream.Key,
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	utils.LogDebugLegacy("已设置Claude Code标准请求头")
}

// parseStreamParameter 解析请求体中的stream参数
//
// 参数:
//   - body: 请求体字节数组
//
// 返回值:
//   - bool: 是否为流式请求
func (p *ProxyHandler) parseStreamParameter(body []byte) bool {
	// 解析JSON请求体
	var requestData map[string]interface{}
	if err := json.Unmarshal(body, &requestData); err != nil {
		// 如果解析失败，默认为非流式
		return false
	}

	// 检查stream字段
	if streamValue, exists := requestData["stream"]; exists {
		// 尝试转换为布尔类型
		if streamBool, ok := streamValue.(bool); ok {
			return streamBool
		}
		// 尝试从字符串转换
		if streamStr, ok := streamValue.(string); ok {
			if streamBool, err := strconv.ParseBool(streamStr); err == nil {
				return streamBool
			}
		}
	}

	// 默认为非流式
	return false
}

// handleStreamResponse 处理流式响应：边转发边记录
//
// 参数:
//   - w: HTTP响应写入器
//   - upstreamResp: 上游响应
//   - logData: 日志数据
//   - taskID: 任务ID
func (p *ProxyHandler) handleStreamResponse(w http.ResponseWriter, upstreamResp *http.Response, logData *utils.RequestLogData, taskID string) {
	// 设置流式响应头
	for key, values := range upstreamResp.Header {
		w.Header().Set(key, strings.Join(values, ", "))
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(upstreamResp.StatusCode)

	// 创建缓冲区用于记录响应体
	var responseBuffer bytes.Buffer

	// 获取flusher
	flusher, canFlush := w.(http.Flusher)
	if !canFlush {
		utils.LogError(taskID, "HTTP连接不支持流式传输")
		logData.Success = false
		logData.Error = "HTTP连接不支持流式传输"
		utils.SaveRequestLog(logData)
		return
	}

	// 流式转发并记录响应体
	const bufferSize = 4096
	buffer := make([]byte, bufferSize)
	totalBytesRead := 0

	for {
		n, err := upstreamResp.Body.Read(buffer)
		if n > 0 {
			totalBytesRead += n
			chunk := buffer[:n]

			// 同时写入响应和缓冲区
			if _, writeErr := w.Write(chunk); writeErr != nil {
				utils.LogError(taskID, "写入响应失败: " + writeErr.Error())
				break
			}
			responseBuffer.Write(chunk)

			// 立即刷新
			flusher.Flush()
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			utils.LogError(taskID, "读取上游响应体失败: " + err.Error())
			logData.Success = false
			logData.Error = "读取上游响应体失败: " + err.Error()
			utils.SaveRequestLog(logData)
			return
		}
	}

	// 最后刷新一次
	flusher.Flush()

	// 记录响应体
	logData.UpstreamResponse.Body = p.fixEncoding(responseBuffer.Bytes())

	// 判断请求是否成功
	logData.Success = upstreamResp.StatusCode == 200
	if !logData.Success {
		logData.Error = fmt.Sprintf("上游响应状态码错误: %d", upstreamResp.StatusCode)
	}

	// 保存日志
	utils.SaveRequestLog(logData)

	utils.LogDebug(taskID, fmt.Sprintf("流式响应传输完成，总计传输: %d bytes", totalBytesRead))

	if logData.Success {
		utils.LogSuccess(taskID, "流式请求处理成功")
	} else {
		utils.LogError(taskID, "流式请求处理失败")
	}
}

// handleNonStreamResponse 处理非流式响应：读取完整响应体
//
// 参数:
//   - w: HTTP响应写入器
//   - upstreamResp: 上游响应
//   - logData: 日志数据
//   - taskID: 任务ID
func (p *ProxyHandler) handleNonStreamResponse(w http.ResponseWriter, upstreamResp *http.Response, logData *utils.RequestLogData, taskID string) {
	// 读取完整响应体
	responseBody, err := io.ReadAll(upstreamResp.Body)
	if err != nil {
		utils.LogError(taskID, "读取上游响应体失败: " + err.Error())
		logData.Success = false
		logData.Error = "读取上游响应体失败: " + err.Error()
		utils.SaveRequestLog(logData)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}

	// 记录响应体（修复编码问题）
	logData.UpstreamResponse.Body = p.fixEncoding(responseBody)

	// 判断请求是否成功
	logData.Success = upstreamResp.StatusCode == 200
	if !logData.Success {
		logData.Error = fmt.Sprintf("上游响应状态码错误: %d", upstreamResp.StatusCode)
	}

	// 保存日志
	utils.SaveRequestLog(logData)

	// 设置响应头
	for key, values := range upstreamResp.Header {
		w.Header().Set(key, strings.Join(values, ", "))
	}
	w.WriteHeader(upstreamResp.StatusCode)

	// 输出响应体
	if _, err := w.Write(responseBody); err != nil {
		utils.LogError(taskID, "输出响应体失败: " + err.Error())
		return
	}

	utils.LogDebug(taskID, fmt.Sprintf("非流式响应处理完成，响应体大小: %d bytes", len(responseBody)))

	if logData.Success {
		utils.LogSuccess(taskID, "非流式请求处理成功")
	} else {
		utils.LogError(taskID, "非流式请求处理失败")
	}
}

// fixEncoding 修复中文编码问题
//
// 参数:
//   - data: 原始字节数据
//
// 返回值:
//   - string: 修复后的字符串
func (p *ProxyHandler) fixEncoding(data []byte) string {
	// 检查是否为有效的UTF-8编码
	if utf8.Valid(data) {
		return string(data)
	}

	// 如果不是有效的UTF-8，尝试修复
	// 这里使用Go的utf8.ValidString来清理无效字符
	result := make([]rune, 0, len(data))
	for i, r := range string(data) {
		if r == utf8.RuneError {
			// 如果遇到错误字符，检查原始字节
			if i < len(data) && data[i] < 128 {
				// ASCII字符，直接保留
				result = append(result, rune(data[i]))
			}
			// 否则跳过该字符
		} else {
			result = append(result, r)
		}
	}

	return string(result)
}

