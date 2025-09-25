package utils

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"claude-mimic-gateway/config"
)

// SystemMessage 系统消息结构体
type SystemMessage struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// CacheControl 缓存控制配置结构体
type CacheControl struct {
	Type string `json:"type"`
}

// Metadata 请求元数据结构体
type Metadata struct {
	UserID string `json:"user_id"`
}

// RequestBody API请求体结构体
type RequestBody struct {
	Model    string           `json:"model,omitempty"`
	Messages []interface{}    `json:"messages,omitempty"`
	System   []*SystemMessage `json:"system,omitempty"`
	Metadata *Metadata        `json:"metadata,omitempty"`
	// 其他可能的字段使用interface{}保留原始结构
	MaxTokens   interface{} `json:"max_tokens,omitempty"`
	Stream      interface{} `json:"stream,omitempty"`
	Temperature interface{} `json:"temperature,omitempty"`
}

// claudeCodeSystemMessage Claude Code标准系统消息
var claudeCodeSystemMessage = &SystemMessage{
	Type: "text",
	Text: "You are Claude Code, Anthropic's official CLI for Claude.",
	CacheControl: &CacheControl{
		Type: "ephemeral",
	},
}


// 请求体大小阈值（字节）
const requestBodySizeThreshold = 20000

// SystemPromptCache 系统提示词缓存管理
type SystemPromptCache struct {
	mu    sync.RWMutex
	cache map[string]string
}

// 全局系统提示词缓存实例
var globalSystemPromptCache = &SystemPromptCache{
	cache: make(map[string]string),
}

// Set 设置模型的系统提示词
//
// 参数:
//   - model: 模型名称
//   - prompt: 系统提示词内容
func (spc *SystemPromptCache) Set(model, prompt string) {
	spc.mu.Lock()
	defer spc.mu.Unlock()
	spc.cache[model] = prompt
}

// Get 获取模型的系统提示词
//
// 参数:
//   - model: 模型名称
//
// 返回值:
//   - string: 系统提示词内容
//   - bool: 是否存在
func (spc *SystemPromptCache) Get(model string) (string, bool) {
	spc.mu.RLock()
	defer spc.mu.RUnlock()
	prompt, exists := spc.cache[model]
	return prompt, exists
}

// Has 检查是否存在指定模型的系统提示词
//
// 参数:
//   - model: 模型名称
//
// 返回值:
//   - bool: 是否存在
func (spc *SystemPromptCache) Has(model string) bool {
	spc.mu.RLock()
	defer spc.mu.RUnlock()
	_, exists := spc.cache[model]
	return exists
}

// SetSystemPrompt 设置模型系统提示词到全局缓存
//
// 参数:
//   - model: 模型名称
//   - prompt: 系统提示词内容
func SetSystemPrompt(model, prompt string) {
	globalSystemPromptCache.Set(model, prompt)
}

// LoadSystemPrompts 从指定目录加载所有系统提示词文件
//
// 参数:
//   - promptDir: 提示词文件目录路径
//
// 返回值:
//   - int: 加载的提示词数量
//   - error: 可能的错误
func LoadSystemPrompts(promptDir string) (int, error) {
	// 检查目录是否存在
	if _, err := os.Stat(promptDir); os.IsNotExist(err) {
		LogDebugLegacy(fmt.Sprintf("系统提示词目录不存在: %s", promptDir))
		return 0, nil
	}

	// 读取目录下的所有文件
	files, err := ioutil.ReadDir(promptDir)
	if err != nil {
		return 0, fmt.Errorf("读取系统提示词目录失败: %v", err)
	}

	loadedCount := 0
	for _, file := range files {
		// 只处理.txt文件
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".txt") {
			continue
		}

		// 提取模型名称（去掉.txt扩展名）
		modelName := strings.TrimSuffix(file.Name(), ".txt")
		filePath := filepath.Join(promptDir, file.Name())

		// 读取文件内容
		content, err := ioutil.ReadFile(filePath)
		if err != nil {
			LogErrorLegacy(fmt.Sprintf("读取系统提示词文件失败 %s: %v", filePath, err))
			continue
		}

		// 将内容存储到缓存中
		globalSystemPromptCache.Set(modelName, string(content))
		loadedCount++
		LogDebugLegacy(fmt.Sprintf("已加载系统提示词: %s (%d bytes)", modelName, len(content)))
	}

	LogDebugLegacy(fmt.Sprintf("系统提示词加载完成，共加载 %d 个模型的提示词", loadedCount))
	return loadedCount, nil
}

// LoadSystemPromptsFromDefault 从默认目录加载系统提示词
//
// 返回值:
//   - int: 加载的提示词数量
//   - error: 可能的错误
func LoadSystemPromptsFromDefault() (int, error) {
	return LoadSystemPrompts("system_prompt")
}

// GetAvailableModels 获取已加载的所有模型列表
//
// 返回值:
//   - []string: 模型名称列表
func GetAvailableModels() []string {
	globalSystemPromptCache.mu.RLock()
	defer globalSystemPromptCache.mu.RUnlock()

	models := make([]string, 0, len(globalSystemPromptCache.cache))
	for model := range globalSystemPromptCache.cache {
		models = append(models, model)
	}
	return models
}

// TransformRequestBody 转换请求体以符合Claude Code标准
//
// 参数:
//   - body: 原始请求体字节数组
//
// 返回值:
//   - []byte: 转换后的请求体字节数组
//   - error: 可能的错误
func TransformRequestBody(body []byte) ([]byte, error) {
	// 解析原始请求体为map，保持灵活性
	var originalBody map[string]interface{}
	if err := json.Unmarshal(body, &originalBody); err != nil {
		return nil, fmt.Errorf("解析原始请求体失败: %v", err)
	}

	// 阶段1: 验证请求体格式
	if err := validateRequestBody(originalBody); err != nil {
		return nil, err
	}

	// 阶段2: 修复请求内容
	if err := repairRequestContent(originalBody); err != nil {
		LogErrorLegacy("修复请求内容失败: " + err.Error())
		// 修复失败不阻止继续处理
	}

	// 阶段3: 优化模型参数
	if err := optimizeModelParameters(originalBody); err != nil {
		LogErrorLegacy("优化模型参数失败: " + err.Error())
		// 优化失败不阻止继续处理
	}

	// 阶段4: 添加metadata参数（现有逻辑）
	cfg := config.GetConfig()
	if cfg == nil {
		return nil, fmt.Errorf("无法获取配置实例")
	}

	originalBody["metadata"] = map[string]interface{}{
		"user_id": cfg.Gateway.UserID,
	}

	// 阶段5: 处理system参数（现有逻辑）
	if err := processSystemMessages(originalBody); err != nil {
		return nil, fmt.Errorf("处理系统消息失败: %v", err)
	}

	// 阶段6: 处理temperature、top_p、max_tokens范围
	processlimit(originalBody,"temperature",0,1)
	processlimit(originalBody,"top_p",0,1)
	processlimit(originalBody,"max_tokens",4096,64000)

	// 重新序列化
	transformedBody, err := json.Marshal(originalBody)
	if err != nil {
		return nil, fmt.Errorf("序列化转换后的请求体失败: %v", err)
	}

	return transformedBody, nil
}

// processlimit 尝试把参数限制在合理范围
func processlimit(body map[string]interface{}, key string, min, max float32) {
	// 保证 min <= max
	if min > max {
		min, max = max, min
	}

	// 不存在返回即可
	v, ok := body[key]
	if !ok {
		return
	}

	// 尝试转为 float64
	if f, ok := toFloat64(v); ok {
		if f < float64(min){
			LogDebugLegacy(key + "参数太小进行修正")
			body[key] = min
		}else if f > float64(max){
			LogDebugLegacy(key + "参数太大进行修正")
			body[key] = max
		}
		return
	}
	// 非数值，默认设为 max
	body[key] = float64(max)
}

// toFloat64 尝试把各种数值类型转为 float64
//
// 参数:
//   - v: any
//
// 返回值:
//   - float64: 转换为float64的数值
//   - bool: 是否是数值
func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case int16:
		return float64(n), true
	case int8:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint64:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint8:
		return float64(n), true
	default:
		return 0, false
	}
}

// processSystemMessages 处理系统消息数组，实现系统提示词注册优化
//
// 参数:
//   - body: 请求体映射
//
// 返回值:
//   - error: 可能的错误
func processSystemMessages(body map[string]interface{}) error {
	// 检查是否存在system字段
	systemField, exists := body["system"]
	if !exists {
		systemField = []interface{}{}
	}

	// 将system字段转换为slice
	systemSlice, ok := systemField.([]interface{})
	if !ok {
		return fmt.Errorf("system字段格式不正确，应为数组")
	}

	// 检查第一项是否为Claude Code系统消息
	if len(systemSlice) > 0 && isClaudeCodeMessage(systemSlice[0]) {
		LogDebugLegacy("该请求为Claude Code系统消息 > 直接转发")
		return nil
	}

	// 计算请求体大小
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("序列化请求体失败: %v", err)
	}
	contentLength := len(bodyBytes)

	var newSystemSlice []interface{}

	// 如果请求体小于阈值，需要注入官方提示词避免风控
	if contentLength < requestBodySizeThreshold {
		LogDebugLegacy(fmt.Sprintf("Content-Length: %d 内容太短 需要注入官方提示词避免风控", contentLength))

		// 处理现有system消息：合并多个system消息并添加XML标签
		if len(systemSlice) > 0 {
			wrappedMessage := mergeAndWrapSystemMessages(systemSlice)
			if wrappedMessage != nil {
				newSystemSlice = append(newSystemSlice, wrappedMessage)
			}
		}

		// 注册官方模型提示词信息
		if model, ok := body["model"].(string); ok && model != "" {
			if globalSystemPromptCache.Has(model) {
				if systemPromptContent, exists := globalSystemPromptCache.Get(model); exists {
					modelSystemMessage := createModelSystemMessage(systemPromptContent)
					newSystemSlice = append(newSystemSlice, modelSystemMessage)
					LogDebugLegacy(fmt.Sprintf("已注入模型 %s 的系统提示词", model))
				}
			}else{
				LogDebugLegacy("模型提示词不存在 :" + model)
			}
		}
	} else {
		// 请求体大小足够，保持原有system消息
		newSystemSlice = systemSlice
	}

	// 设置Claude Code系统消息为首位，伪装成Claude Code请求
	finalSystemSlice := make([]interface{}, 0, len(newSystemSlice)+1)
	finalSystemSlice = append(finalSystemSlice, claudeCodeSystemMessage)
	finalSystemSlice = append(finalSystemSlice, newSystemSlice...)

	body["system"] = finalSystemSlice
	LogDebugLegacy("已将Claude Code系统消息插入到system数组首位")

	return nil
}

// isClaudeCodeMessage 检查消息是否为Claude Code标准系统消息
//
// 参数:
//   - message: 要检查的消息对象
//
// 返回值:
//   - bool: 是否为Claude Code消息
func isClaudeCodeMessage(message interface{}) bool {
	messageMap, ok := message.(map[string]interface{})
	if !ok {
		return false
	}

	// 检查type字段
	msgType, ok := messageMap["type"].(string)
	if !ok || msgType != claudeCodeSystemMessage.Type {
		return false
	}

	// 检查text字段
	msgText, ok := messageMap["text"].(string)
	if !ok || msgText != claudeCodeSystemMessage.Text {
		return false
	}

	// 检查cache_control字段
	cacheControl, ok := messageMap["cache_control"].(map[string]interface{})
	if !ok {
		return false
	}

	cacheType, ok := cacheControl["type"].(string)
	if !ok || cacheType != claudeCodeSystemMessage.CacheControl.Type {
		return false
	}

	return true
}

// mergeAndWrapSystemMessages 合并系统消息并用XML标签包装
//
// 参数:
//   - systemSlice: 系统消息数组
//
// 返回值:
//   - *SystemMessage: 合并后的系统消息
func mergeAndWrapSystemMessages(systemSlice []interface{}) *SystemMessage {
	// 过滤出text类型的系统消息
	var textMessages []string
	for _, msg := range systemSlice {
		if messageMap, ok := msg.(map[string]interface{}); ok {
			if msgType, ok := messageMap["type"].(string); ok && msgType == "text" {
				if msgText, ok := messageMap["text"].(string); ok {
					textMessages = append(textMessages, msgText)
				}
			}
		}
	}

	if len(textMessages) == 0 {
		return nil
	}

	// 合并所有text消息内容
	combinedText := strings.Join(textMessages, "\n\n")

	// 创建包装了XML标签的system消息
	return &SystemMessage{
		Type: "text",
		Text: fmt.Sprintf("<system_prompt>\n%s\n</system_prompt>", combinedText),
		CacheControl: &CacheControl{
			Type: "ephemeral",
		},
	}
}

// createModelSystemMessage 创建模型特定的系统消息
//
// 参数:
//   - content: 系统提示词内容
//
// 返回值:
//   - *SystemMessage: 模型系统消息
func createModelSystemMessage(content string) *SystemMessage {
	return &SystemMessage{
		Type: "text",
		Text: content,
		CacheControl: &CacheControl{
			Type: "ephemeral",
		},
	}
}

// validateRequestBody 验证请求体基本格式
//
// 参数:
//   - body: 请求体映射
//
// 返回值:
//   - error: 验证错误，格式异常时返回特定错误用于401响应
func validateRequestBody(body map[string]interface{}) error {
	// 检查system字段格式，如果存在且不为数组则返回401错误
	if systemField, exists := body["system"]; exists {
		if _, ok := systemField.([]interface{}); !ok {
			LogErrorLegacy("system字段格式异常，应为数组类型")
			return fmt.Errorf("格式异常")
		}
	}

	LogDebugLegacy("请求体格式验证通过")
	return nil
}

// repairRequestContent 修复请求内容问题
//
// 参数:
//   - body: 请求体映射
//
// 返回值:
//   - error: 可能的修复错误
func repairRequestContent(body map[string]interface{}) error {
	// 检查messages字段是否存在
	messagesField, exists := body["messages"]
	if !exists {
		return nil // 没有messages字段，无需修复
	}

	// 将messages转换为数组
	messages, ok := messagesField.([]interface{})
	if !ok {
		return fmt.Errorf("messages字段格式不正确")
	}

	repairCount := 0
	// 遍历处理每个消息
	for _, msg := range messages {
		if messageMap, ok := msg.(map[string]interface{}); ok {
			if repaired := repairMessageContent(messageMap); repaired {
				repairCount++
			}
		}
	}

	if repairCount > 0 {
		LogDebugLegacy(fmt.Sprintf("已修复 %d 个消息的content内容", repairCount))
	}

	return nil
}

// repairMessageContent 修复单个消息的content内容
//
// 参数:
//   - message: 消息映射
//
// 返回值:
//   - bool: 是否进行了修复
func repairMessageContent(message map[string]interface{}) bool {
	// 检查content字段是否存在且为数组
	contentField, exists := message["content"]
	if !exists {
		return false
	}

	contentArray, ok := contentField.([]interface{})
	if !ok || len(contentArray) != 2 {
		return false // 不是双元素数组，不符合修复条件
	}

	// 检查第一个元素是否为空text
	firstElement, ok1 := contentArray[0].(map[string]interface{})
	secondElement, ok2 := contentArray[1].(map[string]interface{})

	if !ok1 || !ok2 {
		return false
	}

	// 检查第一个元素是否为空的text类型
	firstType, hasFirstType := firstElement["type"].(string)
	firstText, hasFirstText := firstElement["text"].(string)

	if !hasFirstType || firstType != "text" || !hasFirstText || firstText != "" {
		return false // 不符合修复条件
	}

	// 检查第二个元素是否有text内容，用于推断文件类型
	secondText, hasSecondText := secondElement["text"].(string)
	if !hasSecondText {
		return false
	}

	// 根据第二个元素的内容推断文件类型
	fileType := detectFileType(secondText)

	// 修复第一个元素的text内容
	firstElement["text"] = fileType + "文件"

	LogDebugLegacy("已修复content中的空text内容为: " + fileType + "文件")
	return true
}

// detectFileType 根据文件内容检测文件类型
//
// 参数:
//   - content: 文件内容字符串
//
// 返回值:
//   - string: 检测到的文件类型
func detectFileType(content string) string {
	// 检查是否包含文件名模式
	if strings.Contains(content, "temp_file_") && strings.Contains(content, ".txt") {
		return "text"
	}

	// 根据内容特征检测文件类型
	lowerContent := strings.ToLower(content)

	if strings.Contains(lowerContent, ".jpg") || strings.Contains(lowerContent, ".png") ||
	   strings.Contains(lowerContent, ".gif") || strings.Contains(lowerContent, ".jpeg") {
		return "image"
	}

	if strings.Contains(lowerContent, ".pdf") {
		return "pdf"
	}

	if strings.Contains(lowerContent, ".doc") || strings.Contains(lowerContent, ".docx") {
		return "document"
	}

	// 默认返回text类型
	return "text"
}

// optimizeModelParameters 优化模型参数，处理参数冲突
//
// 参数:
//   - body: 请求体映射
//
// 返回值:
//   - error: 可能的优化错误
func optimizeModelParameters(body map[string]interface{}) error {
	// 获取模型名称
	model, exists := body["model"].(string)
	if !exists || model == "" {
		return nil // 没有模型信息，无需优化
	}

	// 针对claude-opus-4-1-20250805模型的特殊处理
	if model == "claude-opus-4-1-20250805" {
		return handleOpusModelParameters(body)
	}

	return nil
}

// handleOpusModelParameters 处理Opus模型的参数冲突问题
//
// 参数:
//   - body: 请求体映射
//
// 返回值:
//   - error: 可能的处理错误
func handleOpusModelParameters(body map[string]interface{}) error {
	// 检查temperature和top_p是否同时存在
	_, hasTemperature := body["temperature"]
	_, hasTopP := body["top_p"]

	if hasTemperature && hasTopP {
		// 去掉top_p参数，避免冲突
		delete(body, "top_p")
		LogDebugLegacy("已移除top_p参数，避免与temperature在claude-opus-4-1-20250805模型中冲突")
		return nil
	}

	return nil
}