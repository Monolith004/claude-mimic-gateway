package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"sync"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v2"
)

// Config 网关配置结构体，定义所有配置参数
type Config struct {
	// Upstream 上游服务配置
	Upstream struct {
		URL string `yaml:"url"` // 上游Claude API地址
		Key string `yaml:"key"` // 上游API密钥
	} `yaml:"upstream"`

	// Server 服务器配置
	Server struct {
		Port int `yaml:"port"` // 服务监听端口
	} `yaml:"server"`

	// Auth 认证配置
	Auth struct {
		Key string `yaml:"key"` // 下游客户端验证密钥
	} `yaml:"auth"`

	// Gateway 网关特定配置
	Gateway struct {
		UserID string `yaml:"user_id"` // 固定用户ID，用于伪装成Claude Code请求
	} `yaml:"gateway"`
}

var (
	instance *Config
	once     sync.Once
)

// LoadConfig 从指定文件路径加载配置
//
// 参数:
//   - configPath: 配置文件路径
//
// 返回值:
//   - *Config: 加载的配置实例
//   - error: 可能的错误
func LoadConfig(configPath string) (*Config, error) {
	var err error
	once.Do(func() {
		instance = &Config{}
		err = loadConfigFromFile(configPath, instance)
	})
	return instance, err
}

// GetConfig 获取当前配置实例
//
// 返回值:
//   - *Config: 当前的配置实例
func GetConfig() *Config {
	return instance
}

// generateUserID 生成Claude Code风格的用户ID
//
// 返回值:
//   - string: 格式化的用户ID字符串
func generateUserID() string {
	// 使用当前时间戳作为种子生成唯一哈希
	input := fmt.Sprintf("claude-mimic-gateway_%d", time.Now().UnixNano())
	hash := sha256.Sum256([]byte(input))
	userHash := hex.EncodeToString(hash[:])

	// 生成会话UUID
	sessionUUID := uuid.New().String()

	// 组合完整的user_id
	return fmt.Sprintf("user_%s_account__session_%s", userHash, sessionUUID)
}

// loadConfigFromFile 从指定文件加载配置到给定的配置结构体中
//
// 参数:
//   - configPath: 配置文件路径
//   - cfg: 要填充的配置结构体指针
//
// 返回值:
//   - error: 可能的错误
func loadConfigFromFile(configPath string, cfg *Config) error {
	// 读取配置文件
	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("读取配置文件失败: %v", err)
	}

	// 解析YAML配置
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("解析配置文件失败: %v", err)
	}

	// 验证配置
	if err := validateConfig(cfg); err != nil {
		return fmt.Errorf("配置验证失败: %v", err)
	}

	return nil
}

// validateConfig 验证提供的配置参数是否有效
//
// 参数:
//   - cfg: 要验证的配置结构体指针
//
// 返回值:
//   - error: 验证失败时的错误
func validateConfig(cfg *Config) error {
	if cfg.Upstream.URL == "" {
		return fmt.Errorf("上游URL不能为空")
	}
	if cfg.Upstream.Key == "" {
		return fmt.Errorf("上游密钥不能为空")
	}
	if cfg.Server.Port <= 0 || cfg.Server.Port > 65535 {
		return fmt.Errorf("服务端口必须在1-65535之间")
	}
	if cfg.Auth.Key == "" {
		return fmt.Errorf("验证密钥不能为空")
	}
	if cfg.Gateway.UserID == "" {
		// 自动生成UserID
		cfg.Gateway.UserID = generateUserID()
		// 使用fmt.Printf直接输出，避免循环依赖
		fmt.Printf("\033[34m[0000][INFO]   %s 检测到user_id为空，已自动生成: %s\033[0m\n",
			time.Now().Format("2006-01-02 15:04:05"), cfg.Gateway.UserID)
	}
	return nil
}