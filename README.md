# Zero Reverse Proxy ⚡

**Zero Reverse Proxy** 是一个轻量级、自包含、高性能的动态反向代理工具。基于 Go 语言开发，专门解决**不使用多级二级域名**、在单一域名或单端口下反向代理多个后端目标（API 及 Web 页面）的需求。

---

## 🌟 核心特性

- 🚀 **极速轻量**：基于 Go 原生 `httputil.ReverseProxy` 与连接池优化，单二进制内嵌前端，Docker 镜像体积仅约 20MB。
- 🎨 **内置 Web 管理控制台**：
  - 前端静态文件通过 `//go:embed` 编译进二进制，免额外 Nginx，开箱即用。
  - 支持随时通过 Web 界面或 API **增加 / 修改 / 删除 / 启停**反向代理规则。
  - 规则实时热加载更新，**无需重启服务或容器**。
  - 一键目标服务连通性与延时测速（Ping）。
  - 一键复制完整代理访问链接。
- 🔗 **单域名 / 单端口路径隔离**：
  - 不依赖 DNS 泛解析和多级二级域名。
  - 访问规范：`http://<host>:<port>/p/<别名>/<上游路径>`。
  - 可配置是否剥离前缀（`strip_prefix`），灵活适应各类上游接口。
- 🌐 **深度兼容带界面的 Web 站点**：
  - **HTML `<base>` 自动注入**：针对网页自动在 `<head>` 中注入 `<base href="/p/<别名>/">`，协助浏览器解析相对路径。
  - **Referer 智能回退机制**：彻底解决 Web 应用中根绝对路径资源（如 `/static/bundle.js`、`/api/user`）引发的 404 问题——当请求打到根路径时，代理会识别 `Referer` 头并自动路由回对应上游服务！
  - **301/302 重定向 Location 重写**：防止上游重定向将客户端跳出代理路径。
  - **Cookie 作用域隔离与重写**：自动修正上游 `Set-Cookie` 的 `Path` 与 `Domain`。
  - **WebSocket 支持**：自动透传协议升级。
- 🔒 **安全鉴权**：
  - 控制台与管理 API 具备 Token / Session 鉴权保护，支持 Web 界面修改管理员密码。
- 📦 **Docker & CI/CD**：
  - 提供多阶段编译 Dockerfile 与 Docker Compose 模板。
  - 自带 GitHub Actions Workflow，推送 Tag 时自动构建多平台镜像发布到 Docker Hub。

---

## 🏗️ 架构与请求流程

```
客户端请求 
   │
   ├─► /admin/ ────────► [内置管理控制台 Web UI]
   │                          │
   ├─► /api/*  ────────► [RESTful 管理 API] (需要 Token 鉴权)
   │                          │
   │                          ▼ (增删改查)
   │                    [JSON 存储 (data/config.json)]
   │                          │
   │                          ▼ (内存零停机热更新)
   │
   ├─► /p/{slug}/* ────► [反向代理引擎 (httputil.ReverseProxy)]
   │                          │
   └─► 根静态资源 (Referer) ─► (智能识别 Referer 所属规则)
                              │
                              ▼ (重写 Header/Location/Cookie/HTML Base)
                        目标服务 (http / https)
```

---

## 🚀 快速启动

### 方式一：使用 Docker Compose（推荐）

1. 创建项目目录并保存 `docker-compose.yml`：

```yaml
services:
  zero-reverse-proxy:
    image: zero-reverse-proxy:latest
    build:
      context: .
      dockerfile: Dockerfile
    container_name: zero-reverse-proxy
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      - PORT=8080
      - ADMIN_USER=admin
      - ADMIN_PASS=admin123
      - CONFIG_PATH=/data/config.json
    volumes:
      - ./data:/data
```

2. 启动服务：

```bash
docker compose up -d
```

3. 打开浏览器访问控制台：
   - 地址：`http://localhost:8080/admin/`
   - 默认账号：`admin`
   - 默认密码：`admin123`

---

### 方式二：使用 Docker 命令直接运行

```bash
docker run -d \
  --name zero-reverse-proxy \
  -p 8080:8080 \
  -e ADMIN_USER=admin \
  -e ADMIN_PASS=admin123 \
  -v $(pwd)/data:/data \
  --restart unless-stopped \
  zero-reverse-proxy:latest
```

---

### 方式三：直接编译运行二进制

```bash
# 1. 编译
go build -o zero-reverse-proxy ./cmd/server

# 2. 运行
./zero-reverse-proxy
```

---

## ⚙️ 环境变量说明

| 变量名 | 默认值 | 说明 |
| :--- | :--- | :--- |
| `PORT` | `8080` | 服务监听端口 |
| `ADMIN_USER` | `admin` | 初始管理员用户名 |
| `ADMIN_PASS` | `admin123` | 初始管理员密码 |
| `CONFIG_PATH` | `data/config.json` | 规则持久化 JSON 文件的存放路径 |

---

## 📖 规则配置项详解

在管理控制台中添加规则时，可配置以下参数：

| 字段 | 类型 | 说明 |
| :--- | :--- | :--- |
| **规则名称 (Name)** | 字符串 | 友好的备注名称（如“内网管理后台”） |
| **路径别名 (Slug)** | 字符串 | 访问路径标识，仅限字母/数字/连字符。访问路径即为 `/p/<slug>/` |
| **目标地址 (Target URL)** | URL | 上游目标，如 `http://192.168.1.100:8080` 或 `https://api.github.com` |
| **剥离前缀 (Strip Prefix)** | 布尔值 | 默认 `true`。访问 `/p/mysvc/v1/users` 时将以 `/v1/users` 请求目标服务 |
| **Web 页面支持 (Inject Base)** | 布尔值 | 默认 `true`。针对 HTML 页面自动注入 `<base href="/p/<slug>/">` 优化相对资源加载 |
| **透传客户端 Host** | 布尔值 | 默认 `false`。为 `false` 时使用目标服务自身的 Host 请求头 |

---

## 📡 RESTful 管理 API

控制台提供完整的 RESTful API，可在自动化部署脚本中直接调用：

| 方法 | 路径 | 鉴权 | 说明 |
| :--- | :--- | :--- | :--- |
| `POST` | `/api/auth/login` | 否 | 登录，获取 Token 及设置 Cookie |
| `POST` | `/api/auth/logout` | 否 | 注销登录 |
| `GET` | `/api/auth/me` | 是 | 获取当前用户信息及规则数量统计 |
| `POST` | `/api/auth/password` | 是 | 修改管理员密码 |
| `GET` | `/api/rules` | 是 | 获取全部代理规则列表 |
| `POST` | `/api/rules` | 是 | 创建新代理规则 |
| `PUT` | `/api/rules/{id}` | 是 | 更新指定规则 |
| `PATCH`| `/api/rules/{id}/toggle`| 是 | 快速启用 / 禁用规则 |
| `DELETE`| `/api/rules/{id}` | 是 | 删除指定规则 |
| `POST` | `/api/rules/test` | 是 | 测试目标 URL 连通性与延迟 |

> 鉴权方式：在请求头中携带 `Authorization: Bearer <token>`，或使用登录时下发的 Cookie。

---

## 🚢 GitHub Actions 自动构建与发布

项目已配置 `.github/workflows/docker-publish.yml`。当你在 GitHub 仓库打 Tag 并推送时，会自动触发构建多架构 Docker 镜像并推送到 Docker Hub。

### 使用步骤：

1. 在 GitHub 仓库设置中添加两个 Secrets：
   - `Settings` -> `Secrets and variables` -> `Actions`
   - `DOCKERHUB_USERNAME`：你的 Docker Hub 用户名
   - `DOCKERHUB_TOKEN`：你的 Docker Hub Access Token
2. 打 Tag 并推送到 GitHub：

```bash
git tag -a v1.0.0 -m "Release v1.0.0"
git push origin v1.0.0
```

3. GitHub Actions 将自动构建并发布：
   - `<你的用户名>/zero-reverse-proxy:v1.0.0`
   - `<你的用户名>/zero-reverse-proxy:v1.0`
   - `<你的用户名>/zero-reverse-proxy:latest`

---

## 📄 License

MIT License
