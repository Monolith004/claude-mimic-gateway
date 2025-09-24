package utils

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"
)

// Logger 全局日志实例
var Logger *logrus.Logger

// LogLevel 日志级别类型
type LogLevel string

const (
	INFO    LogLevel = "INFO"
	DEBUG   LogLevel = "DEBUG"
	SUCCESS LogLevel = "SUCCESS"
	ERROR   LogLevel = "ERROR"
)

// ANSI颜色代码常量
const (
	Reset  = "\033[0m"
	Blue   = "\033[34m"  // INFO - 蓝色
	White  = "\033[37m"  // DEBUG - 白色
	Green  = "\033[32m"  // SUCCESS - 绿色
	Red    = "\033[31m"  // ERROR - 红色
	Yellow = "\033[33m"  // WARNING - 黄色
)

// CustomFormatter 自定义日志格式器
type CustomFormatter struct{}

// Format 格式化日志条目，添加颜色编码和时间戳
//
// 参数:
//   - entry: 要格式化的日志条目
//
// 返回值:
//   - []byte: 格式化后的字节数组
//   - error: 可能的错误
func (f *CustomFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	var color string
	var levelText string

	// 检查是否为SUCCESS级别
	if successLevel, ok := entry.Data["level"]; ok && successLevel == "SUCCESS" {
		color = Green
		levelText = "SUCCESS"
	} else {
		switch entry.Level {
		case logrus.InfoLevel:
			color = Blue
			levelText = "INFO"
		case logrus.DebugLevel:
			color = White
			levelText = "DEBUG"
		case logrus.ErrorLevel:
			color = Red
			levelText = "ERROR"
		case logrus.WarnLevel:
			color = Yellow
			levelText = "WARN"
		default:
			color = White
			levelText = "UNKNOWN"
		}
	}

	// 获取任务ID
	taskID := "0000"
	if taskIDValue, ok := entry.Data["taskID"]; ok {
		if taskIDStr, ok := taskIDValue.(string); ok {
			taskID = taskIDStr
		}
	}

	// 计算缩进空格，让所有级别对齐（最长为7个字符"SUCCESS"）
	padding := ""
	for i := len(levelText); i < 7; i++ {
		padding += " "
	}

	// 格式: [LEVEL][TASKID] 时间 消息
	formatted := []byte(color + "[" + taskID + "]" + "[" + levelText + "]"  + padding + " " +
		entry.Time.Format("2006-01-02 15:04:05") + " " + entry.Message + Reset + "\n")

	return formatted, nil
}

// RequestLogData 请求日志数据结构
type RequestLogData struct {
	TaskID              string                 `json:"task_id"`
	Timestamp           string                 `json:"timestamp"`
	DownstreamRequest   *RequestDetails        `json:"downstream_request"`
	UpstreamRequest     *RequestDetails        `json:"upstream_request"`
	UpstreamResponse    *ResponseDetails       `json:"upstream_response"`
	Error               string                 `json:"error,omitempty"`
	Success             bool                   `json:"success"`
}

// RequestDetails 请求详细信息
type RequestDetails struct {
	Method         string            `json:"method"`
	URL            string            `json:"url"`
	Headers        map[string]string `json:"headers"`
	Body           string            `json:"body"`
	OriginalBody   string            `json:"original_body,omitempty"`   // 仅用于上游请求，记录转换前的原始请求体
	TransformedBody string           `json:"transformed_body,omitempty"` // 仅用于上游请求，记录转换后的请求体
}

// ResponseDetails 响应详细信息
type ResponseDetails struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
}

// init 初始化日志器，设置默认配置
func init() {
	Logger = logrus.New()
	Logger.SetOutput(os.Stdout)
	Logger.SetLevel(logrus.DebugLevel)
	Logger.SetFormatter(&CustomFormatter{})

	// 初始化随机种子
	rand.Seed(time.Now().UnixNano())

	// 确保日志目录存在
	ensureLogDirectories()
}

// ensureLogDirectories 确保日志目录存在
func ensureLogDirectories() {
	dirs := []string{"logs", "errors"}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Printf("创建日志目录失败: %s, 错误: %v\n", dir, err)
		}
	}
}

// SaveRequestLog 保存详细的请求日志到文件
//
// 参数:
//   - logData: 请求日志数据
func SaveRequestLog(logData *RequestLogData) {
	// 使用UTC时间加8小时（东八区时间）作为文件名
	chinaTime := time.Now().UTC().Add(8 * time.Hour)
	timestamp := chinaTime.Format("20060102150405")
	filename := fmt.Sprintf("%s.log", timestamp)

	// 选择存储目录
	dir := "logs"
	if !logData.Success {
		dir = "errors"
	}

	filePath := filepath.Join(dir, filename)

	// 转换为JSON格式
	jsonData, err := json.MarshalIndent(logData, "", "  ")
	if err != nil {
		LogErrorLegacy("序列化日志数据失败: " + err.Error())
		return
	}

	// 写入文件
	if err := os.WriteFile(filePath, jsonData, 0644); err != nil {
		LogErrorLegacy("写入日志文件失败: " + err.Error())
		return
	}

	LogDebugLegacy("已保存请求日志到: " + filePath)
}

// GenerateTaskID 生成随机4位数任务ID
//
// 返回值:
//   - string: 4位数字符串格式的任务ID
func GenerateTaskID() string {
	return fmt.Sprintf("%04d", rand.Intn(10000))
}

// LogInfo 记录INFO级别日志消息
//
// 参数:
//   - taskID: 任务ID
//   - message: 要记录的日志消息
func LogInfo(taskID, message string) {
	Logger.WithField("taskID", taskID).Info(message)
}

// LogDebug 记录DEBUG级别日志消息
//
// 参数:
//   - taskID: 任务ID
//   - message: 要记录的日志消息
func LogDebug(taskID, message string) {
	Logger.WithField("taskID", taskID).Debug(message)
}

// LogError 记录ERROR级别日志消息
//
// 参数:
//   - taskID: 任务ID
//   - message: 要记录的日志消息
func LogError(taskID, message string) {
	Logger.WithField("taskID", taskID).Error(message)
}

// LogSuccess 记录SUCCESS级别日志消息，使用绿色格式
//
// 参数:
//   - taskID: 任务ID
//   - message: 要记录的日志消息
func LogSuccess(taskID, message string) {
	Logger.WithField("level", "SUCCESS").WithField("taskID", taskID).Info(message)
}

// 兼容旧版本的日志函数（不带任务ID）

// LogInfoLegacy 记录INFO级别日志消息（兼容旧版本）
//
// 参数:
//   - message: 要记录的日志消息
func LogInfoLegacy(message string) {
	LogInfo("0000", message)
}

// LogDebugLegacy 记录DEBUG级别日志消息（兼容旧版本）
//
// 参数:
//   - message: 要记录的日志消息
func LogDebugLegacy(message string) {
	LogDebug("0000", message)
}

// LogErrorLegacy 记录ERROR级别日志消息（兼容旧版本）
//
// 参数:
//   - message: 要记录的日志消息
func LogErrorLegacy(message string) {
	LogError("0000", message)
}

// LogSuccessLegacy 记录SUCCESS级别日志消息（兼容旧版本）
//
// 参数:
//   - message: 要记录的日志消息
func LogSuccessLegacy(message string) {
	LogSuccess("0000", message)
}