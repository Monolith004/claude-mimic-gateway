## 项目概述

这是一个用Go语言实现的Claude Code代理网关，用于将下游请求伪装成标准的Claude Code请求。项目核心功能是作为中间件，接收下游请求，验证密钥，修改请求体和请求头，然后转发到上游Claude API。

**简而言之可以帮助你实现伪装claude code请求 将CC分发贩子提供的API 用于更多其他用途**

有的中转站可能会检测系统提示词长度，当检测到上下文太短时，会将system_prompt中的预设提示词加入到系统提示词中。


## 构建方法

### 环境要求
- Go 1.21 或更高版本

### 快速开始

1. **克隆项目**
   ```bash
   git clone https://github.com/Monolith004/claude-mimic-gateway.git
   cd claude-mimic-gateway
   ```

2. **安装依赖**
   ```bash
   cd src
   go mod tidy
   ```

3. **配置文件**
   ```bash
   cp config.example.yaml config.yaml
   # 编辑 config.yaml 填入你的配置信息
   ```

4. **运行程序**
   ```bash
   go run main.go
   ```

### 编译构建

**编译为可执行文件**
```bash
mkdir build
cd src
go build -o claude-mimic-gateway main.go
```

**交叉编译**
```bash
# Windows 64位
GOOS=windows GOARCH=amd64 go build -o ../build/claude-mimic-gateway.exe main.go

# Linux 64位
GOOS=linux GOARCH=amd64 go build -o ../build/claude-mimic-gateway-linux-amd64 main.go

# Windows编译Linux 64位时
sh -c "GOOS=linux GOARCH=amd64 go build -o ../build/claude-mimic-gateway-linux-amd64 main.go"

# macOS 64位
GOOS=darwin GOARCH=amd64 go build -o ../build/claude-mimic-gateway main.go
```

### Docker 构建（可选）
```dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY src/ .
RUN go mod tidy && go build -ldflags="-s -w" -o claude-mimic-gateway main.go

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/claude-mimic-gateway .
COPY config.yaml .
CMD ["./claude-mimic-gateway"]
```

### 免责声明
---

**仅供个人学习交流使用，仅限在中转站用户协议许可内使用**

