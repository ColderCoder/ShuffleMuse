# ShuffleMuse

ShuffleMuse 是一个面向个人和小型自托管场景的轻量音乐库播放器。它使用 Go 扫描本地音频目录，以单个 HTTP 服务同时提供 Vue 3 Web UI、原文件串流、FFmpeg Opus 转码、标签管理和缺失文件清理。

[![CI](https://github.com/ColderCoder/ShuffleMuse/actions/workflows/ci.yml/badge.svg)](https://github.com/ColderCoder/ShuffleMuse/actions/workflows/ci.yml)
[![GHCR](https://img.shields.io/badge/GHCR-ghcr.io%2Fcoldercoder%2Fshufflemuse-blue)](https://github.com/ColderCoder/ShuffleMuse/pkgs/container/shufflemuse)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

> [!IMPORTANT]
> 默认 Compose 配置不启用密码，并且只绑定宿主机 `127.0.0.1:8080`。这个默认值只适合本机访问。开放到局域网、反向代理或公网前，必须设置强密码、允许的 Host，并根据实际部署启用 HTTPS Cookie 和可信代理配置。

## 主要能力

- 递归扫描 FLAC、MP3、OGG、Opus、WAV、AAC、M4A 和 WMA；索引只保存在内存中，标签持久化到 bbolt。
- Web UI 默认播放原文件并支持 HTTP Range，也可按需实时转码为 Ogg Opus。
- 服务端有界随机播放队列、上一首/下一首、进度跳转、音量、静音、收藏和严格标签过滤；选择 `favorite` 后只循环收藏曲目；浏览器只缓存最多 5 个 200 首页面。
- 搜索、目录分页浏览、图片/文本/PDF 预览及原文件下载。
- Tags 功能区管理标签并导出 UTF-8 CSV；其 Graveyard 子页管理已经离线的已标记路径。
- 后台原子重扫：后续扫描期间继续使用最后成功的曲库快照，不中断播放。
- 单密码 Session、IP 登录封禁、可信代理真实 IP、Host/Origin 校验和安全响应头。
- 严格分舱的 FFmpeg/ffprobe 并发、独立等待队列、元数据与封面描述缓存，以及统一优雅关机。
- 多阶段、非 root、只读根文件系统的 Docker Compose 部署。

## 快速部署

要求 Docker Engine 和 Docker Compose 插件。默认配置拉取公开的
`ghcr.io/coldercoder/shufflemuse:0.1.0`。把音乐文件放入项目根目录的
`music/` 后执行：

```bash
mkdir -p music
docker compose pull
docker compose up -d
```

访问 <http://localhost:8080>。首次启动始终会在后台扫描曲库；扫描完成前 `/api/ready` 返回 503，Web UI 会显示初始化状态。默认不执行后续定时重扫，需要时在 Browse 点击 Rescan。

```bash
docker compose ps
docker compose logs -f shufflemuse
curl http://localhost:8080/api/ready
```

Compose 默认挂载：

| 宿主机/卷 | 容器路径 | 用途 |
| --- | --- | --- |
| `./music` | `/music`，只读 | 音乐和同目录辅助文件 |
| `shufflemuse-data` 命名卷 | `/data` | bbolt 标签数据库 |

### 从源码构建

默认 Compose 不在部署主机上构建。若要从当前检出构建相同服务，使用
[docker-compose.build.yml](docker-compose.build.yml)：

```bash
mkdir -p music
docker compose -f docker-compose.build.yml up -d --build
```

[Dockerfile](Dockerfile) 使用 Docker Hub、npm、Go 和 Alpine 的官方上游。
中国大陆网络可改用独立的 [Dockerfile.cn](Dockerfile.cn) 与
[docker-compose.build-cn.yml](docker-compose.build-cn.yml)：

```bash
mkdir -p music
docker compose -f docker-compose.build-cn.yml up -d --build
```

大陆构建只改变 build 阶段的下载端点，运行时端口、卷、环境变量、healthcheck 和安全加固与默认 Compose 一致：

| 工具链 | 大陆构建默认端点 |
| --- | --- |
| Docker Hub 镜像和 Dockerfile frontend | [DaoCloud 镜像加速](https://docs.daocloud.io/en/community/mirror/index.html) |
| Bun/npm 包 | [npmmirror](https://npmmirror.com/) |
| Go modules 与 checksum database | [Goproxy.cn](https://goproxy.cn/) 与 Go 官方 `sum.golang.google.cn` alias |
| Alpine apk | [阿里云 Alpine 镜像](https://developer.aliyun.com/mirror/alpine/) |

主 `web/bun.lock` 保持官方 `registry.npmjs.org` URL；`Dockerfile.cn`
只在镜像构建层内把该前缀映射到 npmmirror，仍使用 frozen lock 和原
integrity。基础镜像版本及 digest 与默认 Dockerfile 相同。第三方镜像的
可用性和信任边界由部署者自行评估；三份 Compose 只能选择一份作为当前
部署配置，端口、密码等修改也应写入实际使用的那一份。它们共享项目名和
`shufflemuse-data` 标签卷，`docker compose down -v` 会删除这份共同数据。
完整说明见[部署与运维](docs/OPERATIONS.md#中国大陆网络替代构建)。

### 开放到本机以外

至少修改 [docker-compose.yml](docker-compose.yml) 中的端口、密码和允许 Host：

```yaml
ports:
  - "0.0.0.0:8080:8080"
environment:
  MUSIC_PASSWORD: "replace-with-a-long-random-password"
  MUSIC_ALLOWED_HOSTS: "music.lan,192.168.1.20"
```

公网部署还应由反向代理终止 HTTPS，并设置 `MUSIC_COOKIE_SECURE: "true"`。不要为了让代理请求通过而把代理或 Docker 网桥加入认证白名单；真实 IP 解析和免认证白名单是两个不同的信任边界。完整示例和解释见[配置与安全](docs/CONFIGURATION.md)及[部署与运维](docs/OPERATIONS.md)。

## 数据与备份

| 数据 | 生命周期 | 是否需要备份 |
| --- | --- | --- |
| `/data/tags.db` | 持久化 | 是，包含标签和收藏 |
| 音乐索引 | 进程内存 | 否，启动和重扫时重建 |
| 登录 Session | 进程内存 | 否，重启后全部失效 |
| 元数据、封面描述和播放队列缓存 | 服务端内存 | 否 |
| 转换后的封面图片 | 仅浏览器私有缓存 1 小时；服务端不保留图片结果 | 否 |

一致性备份必须先停止服务：

```bash
docker compose stop shufflemuse
docker compose run --rm --no-deps --entrypoint tar shufflemuse \
  -C /data -czf - . > shufflemuse-data.tar.gz
docker compose start shufflemuse
```

Tags 页的 CSV 用于查看和外部处理，没有对应的导入功能，不能替代 `tags.db` 备份。恢复、升级和故障排查步骤见[部署与运维](docs/OPERATIONS.md)。

使用源码构建 Compose 时，上述所有 Compose 命令都应保持加入对应的
`-f docker-compose.build.yml` 或 `-f docker-compose.build-cn.yml`。

## 版本与镜像

- 稳定版本由对应 Git 标签发布；`v0.1.0` 对应镜像标签 `0.1.0`、`0.1`、
  `0` 和 `latest`。
- 支持 `linux/amd64` 与 `linux/arm64`。
- `shufflemuse --version` 输出版本、Git commit 与构建时间。
- 生产环境可将 Compose 的 `image` 改为不可变 digest：

  ```yaml
  image: ghcr.io/coldercoder/shufflemuse@sha256:REPLACE_WITH_RELEASE_DIGEST
  ```

镜像只发布到 GHCR；项目不发布独立二进制或 Docker Hub 镜像。

## 配置速查

Compose 在 `environment` 中显式列出了全部配置，不依赖 `.env` 文件：

| 变量 | Compose 值 | 作用 |
| --- | --- | --- |
| `MUSIC_PASSWORD` | 空 | 单密码认证；空值关闭认证 |
| `MUSIC_ALLOWED_HOSTS` | `localhost,127.0.0.1,::1` | 接受的 HTTP Host |
| `MUSIC_REAL_IP_HEADER` | `remote` | 登录封禁使用的客户端 IP 来源 |
| `MUSIC_TRUSTED_PROXY_SUBNETS` | 空 | 有权提供真实 IP 的代理网段 |
| `MUSIC_FFMPEG_MAX_SESSIONS` | `2` | FFmpeg 与 ffprobe 总并发 |
| `MUSIC_MEDIA_AUX_RESERVED_SESSIONS` | `1` | 专供 metadata/封面的辅助进程槽；必须小于总并发 |
| `MUSIC_MEDIA_QUEUE_LIMIT` | `8` | Opus 转码等待队列上限 |
| `MUSIC_MEDIA_AUX_QUEUE_LIMIT` | `8` | metadata/封面等待队列上限 |
| `MUSIC_MEDIA_WAIT_SECONDS` | `15` | 等待媒体槽位的上限秒数 |
| `MUSIC_MEDIA_TASK_SECONDS` | `15` | metadata、封面及 Opus 首字节 deadline 秒数 |
| `MUSIC_STREAM_WRITE_IDLE_SECONDS` | `60` | Opus 每次成功写入后滚动写空闲 deadline |
| `MUSIC_MEDIA_NEGATIVE_CACHE_SECONDS` | `30` | 确定性 metadata/封面未找到负缓存秒数 |
| `MUSIC_COVER_CACHE_ENTRIES` | `128` | 兼容变量；仅限制小型封面 descriptor LRU 条目数，不保存图片字节 |
| `MUSIC_COVER_CACHE_BYTES` | `67108864` | 兼容变量；仅限制 descriptor 估算内存，不保存图片字节 |
| `MUSIC_QUEUE_CACHE_MAX_QUEUES` | `64` | 服务端随机队列最大数量 |
| `MUSIC_QUEUE_CACHE_BYTES` | `134217728` | 队列快照、顺序和管理数据预算（128 MiB） |
| `MUSIC_QUEUE_IDLE_SECONDS` | `86400` | 队列无访问 TTL（24 小时） |
| `MUSIC_RESCAN_INTERVAL` | `0` | `0` 关闭定时重扫；正数 duration 重新启用 |
| `MUSIC_OPUS_BITRATE` | `160` | Opus 转码码率，单位 kbps |
| `MUSIC_BOLTDB_PATH` | `/data/tags.db` | 标签数据库路径 |

所有变量、默认值、校验规则和代理配置见[配置与安全](docs/CONFIGURATION.md)。

## 本地开发

要求 Go 1.24.4 或更高版本、Bun、FFmpeg 和 ffprobe。仓库中的 `web/dist/.gitkeep` 允许干净检出直接编译和运行 Go 测试，但要获得可用的 Web UI，启动服务前仍须生成前端 `web/dist`：

```bash
cd web
bun install --frozen-lockfile
bun run build
cd ..
```

终端一启动后端：

```bash
MUSIC_DIR="$PWD/music" \
MUSIC_BOLTDB_PATH="$PWD/data/tags.db" \
go run ./cmd/server
```

终端二启动 Vite；`/api` 会代理到 `http://localhost:8080`：

```bash
cd web
bun run dev
```

访问 Vite 输出的开发地址。应用不会自动读取项目根目录的 `.env`，本地配置必须通过当前 shell 显式传入。完整环境准备、目录说明和测试策略见[开发指南](docs/DEVELOPMENT.md)。

## 验证

```bash
go test ./...
go test -race ./...
go vet ./...

cd web
bun run test:run
bun run build

cd ..
docker compose config --quiet
docker compose -f docker-compose.build.yml config --quiet
docker compose -f docker-compose.build-cn.yml config --quiet
```

流媒体集成测试要求 `ffmpeg` 位于 `PATH` 中。

## 文档

| 文档 | 内容 |
| --- | --- |
| [文档索引](docs/README.md) | 文档入口和阅读顺序 |
| [用户指南](docs/USER_GUIDE.md) | 登录、播放、搜索、Browse、Tags、CSV 和 Graveyard |
| [配置与安全](docs/CONFIGURATION.md) | 全部环境变量、代理、Cookie、Host 和资源限制 |
| [HTTP API](docs/API.md) | 认证、请求约束、端点、响应字段和错误码 |
| [架构说明](docs/ARCHITECTURE.md) | 启动生命周期、曲库快照、标签存储、媒体管线和前端状态 |
| [部署与运维](docs/OPERATIONS.md) | Compose、反向代理、健康检查、备份恢复、升级和排障 |
| [开发指南](docs/DEVELOPMENT.md) | 环境准备、目录结构、构建、测试和修改约束 |
| [项目审计](docs/PROJECT_AUDIT.md) | 当前实现核查、验证证据、已知风险和后续优先级 |

贡献方式见 [CONTRIBUTING.md](CONTRIBUTING.md)，安全问题请按
[SECURITY.md](SECURITY.md) 私下报告，版本变化见
[CHANGELOG.md](CHANGELOG.md)。
