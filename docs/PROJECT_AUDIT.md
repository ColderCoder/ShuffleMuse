# 项目审计

审计日期：2026-07-23

审计对象：当前工作树，包括尚未提交的后端、前端、Compose 和测试变更。

审计性质：代码、配置、测试与文档的一致性检查；不是渗透测试，也不代表第三方安全认证。

## 结论摘要

当前实现已经具备一套相对完整的单机自托管闭环：扫描使用不可变快照，标签由 bbolt 持久化，认证 Session 和媒体执行器都有明确边界，前端对大列表、异步竞态和 Session 失效做了专门处理，Compose 也采用本机端口绑定、只读根文件系统和最小 Linux 权限。

现有 Go 测试、race detector、`go vet`、前端测试、前端生产构建和 Compose 配置校验均可通过。2026-07-20 审计发现的三项高优先级问题已在 2026-07-21 关闭：

1. 封面 queue full/wait timeout 现在统一返回 `503 MEDIA_BUSY`，并覆盖真实 Loader 错误穿透和 API 映射测试。
2. 登录失败状态固定最多 4096 个客户端，并使用受锁保护的 pending/blocked LRU 和满载保护。
3. 主 Bun lockfile 的 216 个 tarball URL 已恢复为官方 npm registry；大陆镜像只存在于独立 Dockerfile/Compose 的构建层。

2026-07-22 的复核关闭了此前剩余的六项中优先级问题：配置语法现在 fail fast，启动分别预检 ffmpeg/ffprobe，状态接口下发实际 Opus 码率，Tags 键盘焦点可见，logout 网络失败不再传播，并且干净检出可以直接编译 Go embed。详细证据见下文。

2026-07-23 的全项目逻辑与性能复核又关闭了八项问题：默认 Compose 实际暴露范围与文档重新一致；Tags 不再预取完整标签集合；Search/Browse/Preview/Rescan 的过期工作可取消；Browse 的任意深页码不再扩大内存上界；队列选曲不再持有全局锁完成线性扫描；静态哈希资源获得长期缓存且缺失 asset 不再回退 HTML；损坏的本地音量值会被安全归一化；favorite 筛选也不再夹带当前非收藏曲目。

## 审计范围与方法

本次逐项检查了：

- `cmd/server` 的启动、首次扫描、信号和关机顺序；
- `internal/api` 的路由、认证 gate、readiness gate、请求体和查询参数边界；
- `internal/auth` 的 Session、白名单、真实 IP 和登录封禁；
- `internal/index` 的扫描、符号链接、快照、generation 和重扫生命周期；
- `internal/tags` 的双向索引、CSV、Graveyard 和 legacy path 迁移；
- `internal/stream`、`internal/cover`、`internal/mediaexec` 的媒体并发、缓存和取消；
- `web/src` 的路由、Pinia 状态、分页、键盘操作、Modal 和认证失效；
- 两套 Dockerfile、默认 GHCR/官方源码/大陆源码三份 Compose、
  `.dockerignore`、依赖锁文件和持久化模型；
- 后端与前端测试的覆盖范围，以及现有文档与实现的一致性。

检查以当前文件内容为准，没有把历史提交或旧 README 当作事实来源。工作树本身已有大量功能变更，因此审计结论只描述这组文件共同构成的当前版本。

## 验证证据

审计期间执行或复核了以下命令：

| 验证 | 结果 | 说明 |
| --- | --- | --- |
| `go test -count=1 ./...` | 通过 | 不使用测试缓存 |
| `go test -race -count=1 ./...` | 通过 | 覆盖当前 Go 测试触达的并发路径 |
| `go vet ./...` | 通过 | 无 vet 报告 |
| `go test -cover -count=1 ./internal/...` | 通过 | 包级覆盖率见下表 |
| `cd web && bun run test:run` | 通过 | 17 个测试文件、61 个用例 |
| `cd web && bun run build` | 通过 | 包含 `vue-tsc -b` 与 Vite production build |
| 三份 `docker compose ... config --quiet` | 通过 | GHCR、官方源码构建和大陆源码构建配置均能完成解析和插值 |
| `docker compose build --no-cache` | 外部网络失败 | 官方 npm 冷安装与前端构建通过；`proxy.golang.org` 下载 bbolt module 时连接超时 |
| `docker compose build --no-cache --build-arg GOPROXY=https://goproxy.cn,direct` | 外部网络未完成 | Go 下载、官方 npm 冷安装及前后端编译通过；官方 Alpine 仓库停在 FFmpeg 依赖 38/107，连续 3 分钟无进展后人工取消 |
| `docker compose -f docker-compose.build-cn.yml build --no-cache` | 通过 | DaoCloud、npmmirror、Goproxy.cn、`sum.golang.google.cn` 与阿里云 APK 镜像的完整无缓存构建通过 |
| 默认 Dockerfile + `GOPROXY=https://goproxy.cn,direct` | 通过 | 完整构建、版本信息、OCI labels 与非 root 用户均已核对 |
| 非 root 临时卷 tar 备份/恢复 smoke test | 通过 | 在只读根、`cap_drop ALL`、`no-new-privileges` 下完成跨卷 round trip，恢复文件属于 `shufflemuse` |
| 隔离 Chromium 实机冒烟 | 通过 | favorite 只循环收藏曲目；音频请求早于延迟封面；同目录换曲复用 URL/DOM/请求；刷新后封面 `transferSize=0`；无 console/page error |
| `git diff --check` | 通过 | 当前 diff 无空白错误 |

`internal` 包级语句覆盖率快照：

| 包 | 覆盖率 |
| --- | ---: |
| `internal/api` | 81.9% |
| `internal/auth` | 92.2% |
| `internal/config` | 84.3% |
| `internal/cover` | 78.5% |
| `internal/index` | 76.0% |
| `internal/mediaexec` | 78.2% |
| `internal/playqueue` | 80.9% |
| `internal/stream` | 84.7% |
| `internal/tags` | 76.9% |

覆盖率只能说明哪些语句被执行，不能代替端到端行为验证。尤其是进程启动/关机、真实反向代理链、浏览器可访问性、Docker 网络和长期内存增长，不能从这些数字推导为安全。

## 已确认的实现优势

### 安全默认值

- Compose 默认发布 `127.0.0.1:8080:8080`，没有直接暴露到局域网或公网。
- 密码模式的 Session token 使用密码学随机数，服务端仅保存 SHA-256 摘要；Cookie 为 HttpOnly、SameSite=Lax，Secure 可配置。
- 真实 IP 默认使用 TCP 对端。只有显式选择转发头且 TCP 对端属于可信网段时，才读取代理头。
- 认证白名单始终匹配 TCP 对端，没有被真实 IP 解析改变语义。
- JSON body 有 8 KiB 上限、未知字段拒绝、尾随内容拒绝；查询参数有长度和分页上限。
- Host、Origin 和 `Sec-Fetch-Site` 形成浏览器写请求的同源边界；全局响应包含 CSP 等安全头。
- 原始与转码音频都使用 `Cache-Control: private, no-store`。

### 状态和并发

- 扫描完成后一次性发布不可变快照；后续扫描失败不会覆盖最后成功结果。
- request 在开始时捕获一次快照，避免同一响应混用两个 generation。
- rescanner 的 Start/Stop 使用明确锁和 cancel 生命周期，HTTP 关机有统一 deadline。
- FFmpeg 与 ffprobe 共享有界执行器，限制并发、等待数量和等待时长，并响应 request cancellation。
- metadata 使用固定容量 LRU，并以路径、大小和修改时间共同判断命中。
- 前端对 library、playlist、播放意图和分页请求分别使用 epoch、request ID 或 AbortController，减少迟到响应回写。

### 数据完整性

- bbolt 在一个事务内维护 file-to-tags 和 tag-to-files 两个方向。
- Graveyard 只表示数据库里仍有标签、但当前曲库快照已离线的路径；删除只删除标签关系，不操作磁盘文件。
- legacy absolute path 只按明确配置的旧根路径迁移，不做任意后缀猜测。
- CSV 导出覆盖整个标签数据库，并标记 online/missing；字段进行了标准 CSV 转义和表格公式注入防护。

## 发现项

### A-01：封面 busy error 被映射成 500（已关闭）

状态：2026-07-21 已修复

位置：`internal/api/cover.go`、`internal/cover/loader.go`、`internal/mediaexec/limiter.go`

旧实现把不可能产生 busy 的路径解析错误拿去判断，却把 `Covers.Load` 的真实 busy 落入 `500 COVER_ERROR`。

当前 handler 在 `Covers.Load` 返回后使用 `mediaexec.IsBusy` 检查 `ErrQueueFull`、`ErrWaitTimeout` 及包装错误，并返回 HTTP 503 与 `MEDIA_BUSY`。`COVER_NOT_FOUND` 和普通 `COVER_ERROR` 分支保持不变。

新增测试包括：

- 真实 Limiter queue full 不启动封面提取器并保留错误；
- 真实等待超时不启动提取器并保留错误；
- API 对直接和包装后的 busy error 均返回稳定 503。

### A-02：登录失败 IP map 没有全局容量（已关闭）

状态：2026-07-21 已修复

位置：`internal/auth/login_guard.go`

`LoginGuard` 现在固定最多保留 4096 个客户端，不新增运行时配置。map 与 pending/blocked 两条 LRU 在同一互斥锁内更新：

- 满载时先清除全部到期封禁；
- 仍满载时淘汰最久未使用的未封禁失败计数；
- 全部槽位均为有效封禁时不取消 ban，也不插入新 key；该新来源的本次错误密码直接返回封禁结果；
- 未被记录的新来源下一次仍可提交正确密码，避免全局容量饱和演变为所有新用户的持久拒绝服务；
- 阈值、客户端隔离、成功清零和固定封禁 deadline 保持原语义。

测试覆盖默认容量、两种满载路径、到期封禁优先清理、有效 ban 不被淘汰，以及 32 个 goroutine 并发下 map/LRU 一致性和硬上限。

### A-03：Bun lockfile 绑定第三方镜像（已关闭）

状态：2026-07-21 已修复

位置：`web/bun.lock`、`Dockerfile`、`Dockerfile.cn`、三份 Compose

主 `web/bun.lock` 的 216 个 URL 已精确恢复为 `registry.npmjs.org`，版本、依赖图和 integrity 未改变。默认 Dockerfile 因此只使用官方 npm registry。

大陆网络使用独立 `Dockerfile.cn` 和 `docker-compose.build-cn.yml`：DaoCloud 代理固定 digest 的 Dockerfile frontend/基础镜像，Bun 使用 npmmirror，Go 使用 Goproxy.cn 与官方 `sum.golang.google.cn` checksum alias，Alpine apk 使用阿里云。Bun URL 映射只发生在镜像内复制的 lockfile 上，并断言替换前确有官方 URL、替换后不再残留，随后仍 frozen install；工作树 lockfile 不变。

无缓存大陆镜像构建已通过，三个 DaoCloud 基础镜像 manifest digest 与默认源逐一相同。第三方服务的未来可用性仍不是项目可保证事项，因此默认官方链路继续保留。

### B-01：非法配置语法静默回退默认值（已关闭）

状态：2026-07-22 已修复

位置：`internal/config/config.go`

整数、布尔、duration 和特殊媒体预留配置的语法错误现在由 `Validate` 拒绝，并包含变量名与错误值；只有变量缺失或为空时才使用默认值。测试覆盖各解析类型。

### B-02：启动只预检 ffmpeg，没有预检 ffprobe（已关闭）

状态：2026-07-22 已修复

位置：`cmd/server/main.go`、`internal/stream/metadata.go`

启动现在分别检查 `ffmpeg` 与 `ffprobe`，缺失时记录受影响功能。两者缺失仍不阻止原文件播放，readiness 定义保持为曲库快照是否可用。

### B-03：前端 Opus 码率标签写死为 160k（已关闭）

状态：2026-07-22 已修复

位置：`web/src/components/NowPlayingBar.vue`、`MUSIC_OPUS_BITRATE`

完整状态响应现在包含 `opusBitrate`，未认证最小响应不包含运行配置；播放器模式按钮和详情使用该值，并覆盖非 160 配置测试。

### B-04：Tags 行内播放按钮键盘聚焦时可能不可见（已关闭）

状态：2026-07-22 已修复

位置：`web/src/views/TagsView.vue`

播放按钮现在在行 hover、行 `:focus-within` 和按钮自身 `:focus-visible` 时显示，全局焦点轮廓规则保持生效。

### B-05：Logout 网络 rejection 可能传播到点击处理器（已关闭）

状态：2026-07-22 已修复

位置：`web/src/App.vue`、`web/src/stores/auth.ts`

认证 store 现在把远端 logout 定义为 best effort，并始终完成本地退出且 resolve；页面对队列释放也采用 best effort。网络失败回归测试保持本地清理语义。

### B-06：干净检出不能直接编译 Go embed（已关闭）

状态：2026-07-22 已修复

位置：`web/embed.go`、`.gitignore`

仓库现在保留 `web/dist/.gitkeep`，embed 使用 `all:dist`，Vite 不清空占位文件。干净检出可以直接编译和运行 Go 测试；可用 Web UI 与发布镜像仍要求先执行前端构建。

### B-07：默认 Compose 端口暴露与文档不一致（已关闭）

状态：2026-07-23 已修复

位置：`docker-compose.yml`

默认配置曾使用 `8080:8080`，会监听宿主机全部接口，但 README、运维文档和安全审计均声称只监听本机。当前已恢复为 `127.0.0.1:8080:8080`，大陆 Compose 保持相同安全默认值；两份 `docker compose ... config --quiet` 均通过。需要局域网或代理入口的部署仍必须显式修改绑定、密码和 Host。

### B-08：Tags 详情会预取全部文件并放大服务端遍历（已关闭）

状态：2026-07-23 已修复

位置：`internal/tags/store.go`、`internal/api/handler.go`、`web/src/stores/tags.ts`、`web/src/views/TagsView.vue`

旧前端以 1000 条为一页循环请求直到取完，后端每一页又先反序列化完整路径数组并多次遍历；大标签会造成浏览器常驻全集和近似二次工作。当前 UI 固定 200 条服务端分页，只保留当前页。Store 用顺序 JSON decoder 遍历 bbolt value，handler 在同一次可取消遍历中计算在线总数并只收集目标页；没有新增跨请求缓存。

### B-09：Browse 极端页码可解除内存边界（已关闭）

状态：2026-07-23 已修复

位置：`internal/api/browse.go`、`internal/api/browse_test.go`

原有有界 heap 的容量是 `page*limit`，`page=MaxInt` 会把上界饱和到 `MaxInt`，在超大目录里退化为保留全部条目。当前目录和文件使用同一个“目录优先”max-heap，单次最多保留并排序前 50,000 项。请求页已超过目录末尾时仍返回兼容空页；仍有数据但超过窗口时稳定返回 `400 INVALID_PAGINATION`。扫描和条目复核同时响应 request cancellation。

### B-10：队列选曲在线性扫描期间持有全局锁（已关闭）

状态：2026-07-23 已修复

位置：`internal/playqueue/manager.go`、`internal/playqueue/manager_test.go`

百万曲目队列的 `Select` 曾在 Manager 全局互斥锁内搜索位置，使无关队列的 Page/Create/Delete 同时等待。队列内容发布后本来就是不可变的，当前实现先取得只读引用，在锁外可取消扫描，再加锁核对 token 仍指向同一对象后返回或原子替换；并发替换/删除会使旧操作返回 not-found，而不是提交到错误队列。package race 测试及全量 race 均通过。

### B-11：过期前端工作仅防回写但不停止执行（已关闭）

状态：2026-07-23 已修复

位置：`web/src/components/SearchBar.vue`、`web/src/views/BrowseView.vue`、`web/src/components/FilePreviewModal.vue`、`web/src/stores/library.ts`、对应 API handler

Search/Browse 原先只用 request ID 忽略迟到结果，文本 Preview 和重扫在卸载/退出后也可能继续。当前这些请求均绑定 AbortController；后端 Search/Browse/Tag 遍历定期检查 context。关闭 Preview、切换查询/目录、logout 或 stop 会停止仍在进行的工作，竞态测试覆盖取消后不回写。

### B-12：内容哈希静态资源没有利用浏览器缓存（已关闭）

状态：2026-07-23 已修复

位置：`cmd/server/main.go`、`cmd/server/main_test.go`

Vite 的 JS/CSS 文件名包含内容哈希，但旧文件服务没有 Cache-Control；而缺失 `/assets/*` 会错误回退成 200 HTML。当前 `/assets/` 使用一年 `public, immutable`，HTML 和 history 导航使用 `no-cache`，缺失 asset 或带扩展名文件返回 404。单元测试和真实 HTTP header 冒烟均覆盖该行为。

### B-13：本地音量数据可把 NaN 写入 Audio（已关闭）

状态：2026-07-23 已修复

位置：`web/src/stores/player.ts`、`web/src/stores/player.test.ts`

损坏或手工修改的 localStorage 过去可产生 NaN 或超出 0..1 的值，赋给 `HTMLAudioElement.volume` 时可能抛错。加载现在只接受 finite number 并钳制到 0..1，否则回退 0.8；测试覆盖无效值和上下界。

### B-14：favorite 筛选会夹带当前非收藏曲目（已关闭）

状态：2026-07-23 已修复

位置：`internal/playqueue/manager.go`、`web/src/stores/player.ts`

旧行为会无条件把当前曲目固定到新队列顶部，因此从 All tags 切换到
`favorite (1)` 时，当前非收藏曲目仍可能出现在队列中。现在只有当前曲目
本身属于所选标签时才应用 pin；否则服务端返回 `pinApplied=false`，前端切换
到该标签中的第一首。Manager、HTTP API 和 Pinia player 测试均覆盖此分支。
隔离 Chromium 复测确认 favorite 队列只有收藏曲目，单曲队列按 Next 后仍在
该曲目内循环。

### C-01：浏览器级自动化测试不足

优先级：改进项

仓库已经加入 CI workflow，PR 和 `main` 会运行 Go test/race/vet、前端
test/build、三份 Compose 校验、Docker build、版本检查和加固容器 smoke
test。2026-07-23 已用隔离 Chromium 手工验证 Original Range、封面延迟/
同目录复用/刷新缓存和控制台错误，但仓库仍没有可重复运行的浏览器 E2E，
下列行为仍缺少自动化或完整直接证据：

- `cmd/server` 的真实监听、信号、shutdown deadline 与强制关连接；
- nginx/Cloudflare/Docker 网络中的真实代理链；
- Axios interceptor、App 全局 401 清理、扫描 gate 和全局快捷键组合；
- 手机布局、PDF/图片/文本 preview 的完整浏览器交互；
- 键盘全流程、axe 扫描和自动对比度回归；
- CSV 下载失败提示和非常大标签库的浏览器内存表现。

E2E 优先覆盖登录失效、播放、Tags CSV、Graveyard 与 Modal 焦点。

### C-02：发布与治理元数据不完整（已关闭）

状态：2026-07-23 已修复

Go module 已改为 `github.com/ColderCoder/ShuffleMuse`，前后端版本统一为
`0.1.0`，构建注入 commit/build time 并提供 `shufflemuse --version`。
仓库加入 MIT LICENSE、CHANGELOG、CONTRIBUTING、SECURITY policy、完整 CI
和仅向 GHCR 发布 amd64/arm64 镜像的标签 workflow。`.dockerignore` 也排除
本地二进制、测试产物和 TypeScript build info。

## 测试与实现的边界

以下“已通过”结论需要准确解读：

- race detector 只观察测试实际执行的 goroutine 交互，不证明所有部署时序都无 race；
- Vitest 使用 happy-dom，不包含真实媒体元素、浏览器下载、PDF 插件和 CSS 渲染；
- `docker compose config` 只验证配置语法，不启动容器、不验证挂载路径和健康检查；
- 单元测试中的模拟代理地址不证明生产网络中可信网段填写正确；
- CSV 导出可被单元测试验证字段，但大数据量下仍是一次性生成响应，没有独立流式或容量压力测试。

## 文档覆盖矩阵

| 主题 | 主要文档 |
| --- | --- |
| 快速启动、安全默认值、常用配置 | [README](../README.md) |
| 页面操作、键盘、Tags CSV、Graveyard | [用户指南](USER_GUIDE.md) |
| 所有环境变量、解析规则、三种真实 IP 模式 | [配置与安全](CONFIGURATION.md) |
| 全部 HTTP 路由、字段、边界和错误码 | [HTTP API](API.md) |
| 快照、generation、bbolt、媒体管线和前端状态 | [架构说明](ARCHITECTURE.md) |
| Compose、代理、健康、备份、升级和排障 | [部署与运维](OPERATIONS.md) |
| 干净检出、构建、测试、修改约束和缺口 | [开发指南](DEVELOPMENT.md) |

文档刻意区分“设计意图”和“当前可观察行为”。已关闭发现保留旧行为摘要、修复方式和验证证据，不再让 API 或架构文档宣称过时结果。

## 建议处理顺序

1. 补真实浏览器 E2E 与可访问性检查。
2. 后续版本按 CHANGELOG、语义版本标签和 GHCR digest 发布。

每次处理一项发现后，应同时更新本报告、对应主题文档和回归测试；不要只删除风险描述而不留下验证证据。
