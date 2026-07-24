# 开发指南

## 工具要求

| 工具 | 要求/用途 |
| --- | --- |
| Go | `go.mod` 声明 1.24.4；使用 1.24.4 或更高兼容版本 |
| Bun | 前端安装、Vite、Vitest；Docker builder 固定 1.3.14 |
| FFmpeg | Opus 转码、内嵌封面；完整 Go 测试强制要求在 `PATH` |
| ffprobe | 音频 metadata；完整功能和测试需要在 `PATH` |
| Docker + Compose | 生产镜像和 Compose 验证 |

Docker 构建使用 Go 1.26.5，并不意味着本地开发必须与 builder 完全相同；代码的最低工具链契约来自 `go.mod`。

## 干净检出后的第一次构建

`web/embed.go` 使用：

```go
//go:embed all:dist
```

仓库保留 `web/dist/.gitkeep`，因此干净检出可以直接执行 `go test ./...` 和 Go 编译。占位文件不包含可用界面；要运行服务或制作发布构建，仍须先生成前端产物：

```bash
cd web
bun install --frozen-lockfile
bun run build
cd ..
```

Vite 构建配置会保留 `.gitkeep`，生成的其他 `web/dist` 内容继续由 Git 忽略。

### Bun lockfile 与双构建源

仓库中的 `web/bun.lock` 固定使用官方 `https://registry.npmjs.org/` tarball URL。默认 Dockerfile 直接 frozen install，不改写 registry。

中国大陆替代构建使用 `Dockerfile.cn`。它在镜像层内只把锁文件中的官方 registry 前缀改为 `BUN_REGISTRY`，默认是 `https://registry.npmmirror.com`，然后继续执行 `--frozen-lockfile`。该操作不修改工作树中的 lockfile，版本和 integrity 字段保持不变。

依赖变更应维持以下规则：

1. 使用官方 npm registry 生成或更新提交到仓库的 lockfile；
2. 确认 `web/bun.lock` 不包含 npmmirror 等部署特定主机；
3. 不扩大 `Dockerfile.cn` 的替换范围，只映射精确的官方 registry URL 前缀；
4. 分别用默认和大陆 Dockerfile 做无缓存 frozen install；
5. 运行前端测试、生产 build 和两套 Docker build。

## 本地双服务器开发

### 后端

应用不读取 `.env`。从项目根目录显式传入环境：

```bash
MUSIC_DIR="$PWD/music" \
MUSIC_BOLTDB_PATH="$PWD/data/tags.db" \
MUSIC_PASSWORD="" \
go run ./cmd/server
```

`cmd/server` 会创建 bbolt 父目录。默认监听 `:8080`。开发期间如启用密码，使用与生产相同的 Cookie、Host 和 CSRF 语义。

### 前端

另一个终端：

```bash
cd web
bun run dev
```

Vite 把 `/api` 代理到 `http://localhost:8080`。访问 Vite 输出的地址，不要把根 Go 服务的嵌入式旧 dist 与 Vite 热更新页面混淆。

### 生产式本地运行

```bash
cd web
bun run build
cd ..
go build -trimpath -o shufflemuse ./cmd/server
MUSIC_DIR="$PWD/music" \
MUSIC_BOLTDB_PATH="$PWD/data/tags.db" \
./shufflemuse
```

这会从二进制中提供刚生成的 SPA。

## 前端命令

在 `web/` 目录：

| 命令 | 用途 |
| --- | --- |
| `bun run dev` | Vite 开发服务器 |
| `bun run test` | Vitest watch 模式 |
| `bun run test:run` | 一次性前端测试，适合自动化 |
| `bun run build` | `vue-tsc -b` 类型检查后生产构建 |
| `bun run preview` | 本地预览 `dist` |

Vitest 使用 `happy-dom`。当前没有 ESLint、stylelint、coverage script 或浏览器 E2E script。

## Go 命令

```bash
go test ./...
go test -race ./...
go vet ./...
```

`internal/stream/transcode_test.go` 在包初始化时找不到 FFmpeg 会直接 panic，不是 skip。媒体测试也调用 ffprobe，所以完整测试环境必须安装两者。

需要排除缓存重新验证时：

```bash
go test -count=1 ./...
go test -race -count=1 ./...
go test -cover -count=1 ./internal/...
```

## Docker 验证

```bash
docker compose config --quiet

docker compose -f docker-compose.build.yml config --quiet
docker compose -f docker-compose.build.yml build
docker compose -f docker-compose.build-cn.yml config --quiet
docker compose -f docker-compose.build-cn.yml build
```

Docker build 执行：

1. Bun frozen install 与前端 build；
2. Go module download；
3. CGO 关闭的静态 Go build；
4. Alpine runtime 安装固定 FFmpeg 包。

它不会运行 Go tests 或 Vitest。绿色 Docker build 不能替代测试命令。

受限 Go 网络可传 build arg：

```bash
docker compose -f docker-compose.build.yml build \
  --build-arg GOPROXY=https://your-go-proxy.example,direct
```

这只改变默认 Dockerfile 的 Go modules 下载端点，不改变 Bun lockfile。需要同时代理 Docker Hub、Bun/npm、Go 和 Alpine apk 时，应直接验证 `Dockerfile.cn` 与 `docker-compose.build-cn.yml`，不要把大陆镜像设置混入默认 Dockerfile。

## 项目结构

```text
.
├── cmd/server/                 # 进程入口和依赖装配
├── internal/
│   ├── api/                    # HTTP contract 和安全边界
│   ├── auth/                   # Session、代理 IP、登录封禁
│   ├── config/                 # 环境变量
│   ├── cover/                  # 封面提取/fallback
│   ├── index/                  # 扫描和快照
│   ├── mediaexec/              # 子进程并发/队列
│   ├── stream/                 # 原文件、Opus、metadata
│   └── tags/                   # bbolt、Graveyard、CSV 数据源
├── web/
│   ├── src/api/                # Axios API client
│   ├── src/components/         # 播放、搜索、列表和 Modal
│   ├── src/stores/             # Pinia 状态
│   ├── src/views/              # 路由页面
│   ├── embed.go                # embed dist
│   └── package.json
├── docs/                       # 项目文档
├── Dockerfile
├── Dockerfile.cn               # 中国大陆网络构建变体
├── docker-compose.yml          # 固定版本 GHCR 部署
├── docker-compose.build.yml    # 官方上游源码构建
└── docker-compose.build-cn.yml # 独立大陆源码构建
```

当前仓库的 `internal/shuffle/` 是空目录，不是有效运行模块。

## 后端修改约束

### API

- 新 JSON 错误应继续使用稳定 `code`；
- 新 JSON body 应使用严格、有界解码；
- 新查询参数应定义字节/数值上限；
- 新曲库数据路由应考虑 auth 和 readiness gate；
- 列表响应应包含 generation，并保持分页最大 1000；
- 浏览器写请求必须继续通过 Host/Origin/Sec-Fetch 安全中间件；
- 音频响应必须保持 `private, no-store`。

修改路由时同时更新：

- `internal/api/handler.go` 的无密码与密码路由；
- `web/src/api/index.ts`；
- `docs/API.md`；
- API 与前端 tests。

### 扫描和快照

必须保持：

- 扫描完整成功后才发布；
- 失败不替换旧快照；
- request 只捕获一次快照；
- generation 语义明确；
- 依赖路径在线状态的标签事务与发布同步；
- Stop/Start/Shutdown 不死锁。

### 标签

`files` 和 `tags` bucket 是同一关系的两个方向。任何写操作必须在同一个 bbolt transaction 内同步维护。新增持久字段前应设计 schema/version 和备份兼容策略；当前项目没有正式 migration framework。

### 媒体

所有 FFmpeg/ffprobe 新调用都应使用 request context 和共享 limiter，并确保所有成功 Acquire 都有 Release。HEAD 路径不能启动昂贵子进程。输出提交后不能再假装可以返回 JSON 错误。

## 前端修改约束

### 状态一致性

现有异步防护包括：

- Auth 四态状态机；
- Library status/rescan lifecycle epoch + AbortController；
- Player play epoch、playlist request ID、source request ID；
- Search/Browse/Tags AbortController + request ID；
- 文本 Preview 卸载取消；
- 播放队列跨页 generation 校验。

新增异步操作必须明确：什么用户动作会使旧结果失效、停止/退出后是否还能回写、跨页数据是否属于同一 generation。

### 可访问性

- 原生交互优先使用 `button`、`a`、`input`、`select`；
- 自定义 combobox/listbox 保持键盘与 ARIA 关系；
- 全局快捷键不得覆盖交互元素原生行为；
- Modal 保持初始焦点、焦点锁、Escape、背景 inert 和恢复焦点；
- 只在 hover 显示的操作也必须在 `:focus-visible` 或父容器 `:focus-within` 时可见。

Tags 文件行播放按钮已覆盖 hover、按钮 `:focus-visible` 和行 `:focus-within` 三种可见状态。

### 大库

- Search：API/UI 50 一页；
- Browse：目录+文件合计 50 一页；
- Playlist API/UI 200 一页，浏览器最多缓存 5 页；
- Tag 文件 API/UI 200 一页，只保留当前页；
- Browse 最多排序 50,000 项前缀，禁止仍有数据的更深分页。

不要重新引入一次性渲染全部结果或无限 append；如改变上限，同步更新性能测试和用户文档。

## 当前测试范围

后端测试覆盖配置、认证、代理、严格请求、扫描、重扫、Browse、stream、media queue、tags、Graveyard、CSV 和静态资源缓存。前端当前有 17 个测试文件、64 个用例，覆盖主要 stores、分页/取消竞态、登录封禁、路由、Search 语义、Playlist 分块、Tags 二级导航与导出、Graveyard、Modal 焦点和 metadata 标题切换。

当前明确缺口：

- `cmd/server` 真正启动、信号和关机顺序；
- App 全局 401 清理、扫描 gate 和全局快捷键；
- Axios interceptor 本身；
- 响应式布局和移动导航；
- 浏览器 E2E、axe 和自动对比度；
- CSV 失败 UI、PDF/图片的真实浏览器 Preview。

不要用当前单元测试绿色证明这些未覆盖行为。完整清单见[项目审计](PROJECT_AUDIT.md)。

## 依赖与项目元数据

- Go module 为 `github.com/ColderCoder/ShuffleMuse`。
- 前端和后端发布版本均为 `0.1.1`；容器构建通过 linker flags 注入版本、commit
  和构建时间，`shufflemuse --version` 可直接读取。
- 发布与治理入口包括 LICENSE、CHANGELOG、CONTRIBUTING、SECURITY policy、
  完整 CI 和仅 GHCR 的标签发布 workflow。
- 两套 Dockerfile 的 Bun、Go、Alpine 基础镜像使用相同版本与 digest，FFmpeg 包版本也相同；更新时必须同时验证官方源、DaoCloud 映射、apk 镜像和媒体测试。

## 提交前检查清单

- [ ] 先生成 `web/dist`，确保 Go embed 可编译。
- [ ] `gofmt` 已执行。
- [ ] `go test -count=1 ./...` 通过。
- [ ] 与并发/状态相关修改通过 `go test -race -count=1 ./...`。
- [ ] `go vet ./...` 通过。
- [ ] `bun run test:run` 通过。
- [ ] `bun run build` 通过。
- [ ] `docker compose config --quiet` 通过。
- [ ] `docker compose -f docker-compose.build.yml config --quiet` 通过。
- [ ] `docker compose -f docker-compose.build-cn.yml config --quiet` 通过。
- [ ] 部署相关修改完成默认和大陆两套 Docker build。
- [ ] API、配置、UI 行为变化已更新 README/docs。
- [ ] `git diff --check` 无空白错误。
