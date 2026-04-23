# ollama-openai-proxy

一个可直接运行的 `Go` 项目，用来把 `Ollama` 暴露成更通用的 OpenAI 风格接口。

适合你的场景：

- 你在 `Windows 10` 上已经运行了 `Ollama`
- 希望外部程序统一按 OpenAI 接口调用
- 希望项目可以打包成 `Windows .exe` 和 `Linux amd64` 单文件二进制

## 功能

- 对外提供 OpenAI 风格接口：`/v1/*`
- 自动转发到 Ollama 上游地址，默认是 `http://127.0.0.1:11434`
- 支持模型别名映射
- 强制要求 `Bearer API Token`
- 支持 `CORS`
- 提供健康检查接口：`/healthz`
- 可跨平台编译为 `Windows exe` 和 `Linux amd64` 二进制

## 已覆盖的接口

- `POST /v1/chat/completions`
- `POST /v1/completions`
- `POST /v1/embeddings`
- `GET /v1/models`
- `GET /v1/models/{model}`
- 其他 `/v1/*` 请求也会继续透传到 Ollama

## 项目结构

```text
.
├── cmd/ollama-openai-proxy
├── internal/config
├── internal/server
├── scripts/build.sh
├── scripts/build.bat
└── config.example.json
```

## 本地启动

### 1. 先确认 Ollama 正在运行

Windows 10 本机默认地址一般就是：

```text
http://127.0.0.1:11434
```

先拉一个模型，例如：

```bash
ollama pull qwen2.5:7b
```

### 2. 直接运行项目

```bash
go run ./cmd/ollama-openai-proxy -config config.example.json
```

默认会监听：

```text
http://127.0.0.1:8080
```

注意：

- `api_key` 现在是必填项
- 如果没有配置 `api_key`，程序会直接启动失败
- 所有 `/v1/*` 接口调用都必须带 `Authorization: Bearer <token>`

### 3. 健康检查

```bash
curl http://127.0.0.1:8080/healthz
```

## OpenAI 风格调用示例

### cURL

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer ollama-local" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-3.5-turbo",
    "messages": [
      { "role": "user", "content": "你好，请介绍一下你自己" }
    ]
  }'
```

上面示例里的 `gpt-3.5-turbo` 会根据 `config.example.json` 自动映射到：

```text
qwen2.5:7b
```

### Python OpenAI SDK

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:8080/v1",
    api_key="ollama-local",
)

resp = client.chat.completions.create(
    model="gpt-3.5-turbo",
    messages=[
        {"role": "user", "content": "用一句话介绍 Ollama"}
    ],
)

print(resp.choices[0].message.content)
```

## 配置说明

示例配置见 [config.example.json](/root/ollama-api/config.example.json)。

```json
{
  "listen": "0.0.0.0:8080",
  "upstream_base_url": "http://127.0.0.1:11434",
  "api_key": "ollama-local",
  "request_timeout_seconds": 120,
  "log_requests": true,
  "model_aliases": {
    "gpt-3.5-turbo": "qwen2.5:7b",
    "gpt-4o-mini": "llama3.2:3b"
  }
}
```

关键字段：

- `listen`：本服务监听地址
- `upstream_base_url`：Ollama 地址
- `api_key`：必填，所有 OpenAI 风格接口都要使用这个 Bearer Token
- `model_aliases`：把 OpenAI 风格模型名映射到 Ollama 本地模型名

## Token 使用方式

如果你的配置里是：

```json
{
  "api_key": "ollama-local"
}
```

那么客户端必须这样传：

```text
Authorization: Bearer ollama-local
```

很多 OpenAI 类型工具在配置时都会要求填写：

- `Base URL`：`http://你的地址:8080/v1`
- `API Token`：`ollama-local`

也就是工具界面里填 token，程序发请求时会自动带上：

```text
Authorization: Bearer ollama-local
```

## 打包

### Linux / macOS 下执行

```bash
bash scripts/build.sh
```

### Windows 下执行

```bat
scripts\build.bat
```

打包后产物在：

```text
dist/
├── ollama-openai-proxy-windows-amd64.exe
└── ollama-openai-proxy-linux-amd64
```

## 运行打包后的二进制

### Windows

```bat
dist\ollama-openai-proxy-windows-amd64.exe -config config.example.json
```

### Linux

```bash
chmod +x dist/ollama-openai-proxy-linux-amd64
./dist/ollama-openai-proxy-linux-amd64 -config config.example.json
```

## 如果 Linux 二进制要连接 Windows 上的 Ollama

把配置里的 `upstream_base_url` 改成你 `Windows 10` 机器的局域网 IP，例如：

```json
{
  "upstream_base_url": "http://192.168.1.88:11434"
}
```

同时要确保：

- Windows 上的 Ollama 已经在对应地址可访问
- 防火墙已经放行 `11434` 端口

## 测试

```bash
go test ./...
```
