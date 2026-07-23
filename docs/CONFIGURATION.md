# 配置与安全

ShuffleMuse 只从进程环境读取配置，不读取 `.env`。配置在启动时加载并校验，修改后必须重启进程或重新创建 Compose 服务。

## 全部环境变量

下表的“程序默认值”来自 `internal/config/config.go`；“Compose 值”来自当前 `docker-compose.yml`。

| 变量 | 程序默认值 | Compose 值 | 规则与作用 |
| --- | --- | --- | --- |
| `MUSIC_DIR` | `/music` | `/music` | 音乐根目录，不能为空且运行时必须是可访问目录 |
| `MUSIC_PORT` | `8080` | `8080` | HTTP 监听端口，范围 1–65535 |
| `MUSIC_RESCAN_INTERVAL` | `0` | `0` | Go duration，不得小于 0；`0` 关闭定时重扫，正数启用对应周期 |
| `MUSIC_OPUS_BITRATE` | `160` | `160` | FFmpeg Opus 目标码率，单位 kbps，必须大于 0 |
| `MUSIC_PASSWORD` | 空 | 空 | 空值完全关闭认证；非空启用单密码 Session |
| `MUSIC_AUTH_WHITELIST_SUBNETS` | 空 | 空 | 免登录的直连 IP/CIDR，逗号分隔 |
| `MUSIC_LOGIN_MAX_FAILURES` | `3` | `3` | 单客户端连续密码错误阈值，必须大于 0 |
| `MUSIC_LOGIN_BAN_SECONDS` | `3600` | `3600` | 达到阈值后的固定封禁秒数，必须大于 0 |
| `MUSIC_TRUSTED_PROXY_SUBNETS` | 空 | 空 | 有权提供真实客户端 IP 的代理 IP/CIDR，逗号分隔 |
| `MUSIC_REAL_IP_HEADER` | `remote` | `remote` | `remote`、`x-forwarded-for`、`cf-connecting-ip` 三选一 |
| `MUSIC_COOKIE_SECURE` | `false` | `false` | 为 Session Cookie 添加 `Secure`；HTTPS 部署应设为 `true` |
| `MUSIC_ALLOWED_HOSTS` | `localhost,127.0.0.1,::1` | 同默认 | 允许的 Host 名/IP/authority，逗号分隔且至少一项 |
| `MUSIC_FFMPEG_MAX_SESSIONS` | `2` | `2` | 所有 FFmpeg 与 ffprobe 子进程的硬总上限，必须大于 0 |
| `MUSIC_MEDIA_AUX_RESERVED_SESSIONS` | 总数为 1 时 `0`，否则 `1` | `1` | 专供 metadata/封面的槽位；必须非负且小于总数。未设置且总数为 1 时兼容旧共享模式 |
| `MUSIC_MEDIA_QUEUE_LIMIT` | `8` | `8` | Opus 转码最多等待数；可为 0 |
| `MUSIC_MEDIA_AUX_QUEUE_LIMIT` | `8` | `8` | metadata/封面最多等待数；可为 0 |
| `MUSIC_MEDIA_WAIT_SECONDS` | `15` | `15` | 等待媒体槽位的最长秒数，必须大于 0 |
| `MUSIC_MEDIA_TASK_SECONDS` | `15` | `15` | metadata、封面底层任务和 Opus 首字节最长秒数，必须大于 0 |
| `MUSIC_STREAM_WRITE_IDLE_SECONDS` | `60` | `60` | Opus 首字节后滚动写空闲 deadline，必须大于 0 |
| `MUSIC_MEDIA_NEGATIVE_CACHE_SECONDS` | `30` | `30` | 确定性 metadata 失败和封面未找到的负缓存 TTL |
| `MUSIC_METADATA_CACHE_ENTRIES` | `4096` | `4096` | 元数据 LRU 最大条目数，必须大于 0 |
| `MUSIC_COVER_CACHE_ENTRIES` | `128` | `128` | 兼容变量；小型封面 descriptor LRU 条目上限，不缓存图片字节 |
| `MUSIC_COVER_CACHE_BYTES` | `67108864` | `67108864` | 兼容变量；descriptor 估算内存上限，不缓存图片字节 |
| `MUSIC_QUEUE_CACHE_MAX_QUEUES` | `64` | `64` | 服务端随机队列数量上限 |
| `MUSIC_QUEUE_CACHE_BYTES` | `134217728` | `134217728` | 共享快照、`uint32` 顺序和队列管理数据的总预算 |
| `MUSIC_QUEUE_IDLE_SECONDS` | `86400` | `86400` | 队列最后访问后的过期秒数 |
| `MUSIC_LEGACY_MUSIC_ROOT` | 空 | 空 | 旧版绝对标签路径的精确根目录；非空时必须是绝对路径 |
| `MUSIC_BOLTDB_PATH` | `./data/tags.db` | `/data/tags.db` | bbolt 数据库文件；父目录必须可写 |

列表值会去除两端空白，单个 IP 会转换为对应的单地址 CIDR，重复网段会去重。显式设置的整数、布尔和 duration 若无法解析，服务会拒绝启动，并在错误中指出变量名和值；已成功解析但超出规则的值同样会使启动失败。只有变量未设置或为空时才使用默认值。

`MUSIC_RESCAN_INTERVAL=0` 只关闭首次索引建立之后的定时重扫，不会关闭冷启动首次扫描，也不会关闭 Browse 的手动 Rescan。设置 `5m`、`1h` 等正数 Go duration 可重新启用周期重扫。

## 认证模型

### 无密码模式

`MUSIC_PASSWORD` 为空时：

- 所有曲库 API 均无需登录；
- `/api/auth/login` 和 `/api/auth/logout` 不注册；
- 认证白名单被忽略；
- 服务启动日志会打印认证关闭警告。

Compose 同时使用空密码和 `127.0.0.1` 端口绑定，以便默认只从宿主机访问。不要只改成 `0.0.0.0` 而继续使用空密码。

### 密码和 Session

启用密码后，登录成功会设置 `shufflemuse-session` Cookie：

- Token 使用 32 字节密码学随机数；服务端只保存 SHA-256 摘要；
- `HttpOnly`、`SameSite=Lax`、`Path=/`；
- 普通 Session 固定有效 1 小时；“Remember me”固定有效 30 天；
- `MUSIC_COOKIE_SECURE=true` 时增加 `Secure`；
- 最多保存 1024 个 Session，过期项和最久未访问项会被清理；
- Session 不持久化，服务重启后全部失效；
- Logout 只撤销已知 Token，伪造 Cookie 不会扩大 Session 存储。

ShuffleMuse 不提供用户名、角色或细粒度权限。知道同一个密码的访问者拥有相同权限。

### 登录封禁

错误密码按解析后的客户端 IP 计数。达到 `MUSIC_LOGIN_MAX_FAILURES` 时立即返回：

```http
HTTP/1.1 429 Too Many Requests
Retry-After: 3600
Content-Type: application/json

{"code":"LOGIN_IP_BLOCKED","error":"too many failed login attempts"}
```

封禁截止时间在触发时固定，封禁期间继续请求不会延长；成功登录或封禁到期会清除状态。畸形 JSON、未知字段和超大登录请求不会计入密码失败。状态只在内存中，重启后清空。

登录失败状态有固定的 4096 个客户端全局上限，不提供配置开关。达到上限时先清理已到期封禁，再淘汰最久未使用的未封禁失败计数；有效封禁不会为了接纳新来源而被取消。若 4096 项全部是有效封禁，新来源的错误密码本次直接得到 429，但不会新增状态键；它之后提交正确密码时仍能进入正常验证流程。

## 两个独立的 IP 信任边界

`MUSIC_AUTH_WHITELIST_SUBNETS` 与 `MUSIC_TRUSTED_PROXY_SUBNETS` 不能互换：

| 配置 | 影响 | 地址来源 |
| --- | --- | --- |
| 认证白名单 | 是否完全免登录 | 始终只看 TCP 直连对端 |
| 可信代理 | 登录失败归属于哪个客户端 IP | 仅为真实 IP 头授信 |

把反向代理、Docker 网桥或 Tunnel 对端加入认证白名单，会使所有经过它的用户免登录。正确做法通常是：代理网段只加入 `MUSIC_TRUSTED_PROXY_SUBNETS`，仍要求最终用户输入密码。

## `MUSIC_REAL_IP_HEADER`

### `remote`（默认）

始终使用 TCP 对端，忽略所有转发头。没有经过可信代理，或者不能确认代理行为时使用此模式。

### `x-forwarded-for`

只有 TCP 对端命中可信代理网段时才解析 `X-Forwarded-For`。ShuffleMuse 从右向左剥离可信代理地址，取第一个不可信地址。下列情况回退到 TCP 对端：

- 对端不可信或可信代理列表为空；
- 头缺失；
- 任意链元素不是合法 IP；
- 整条链都属于可信代理。

代理必须覆盖而不是简单追加来自外部客户端的伪造头，并应按实际链路把所有可信跳点配置完整。

### `cf-connecting-ip`

只有 TCP 对端可信时读取唯一的 `CF-Connecting-IP`。头缺失、重复、包含逗号或不是合法单一 IP 时回退到 TCP 对端。这是 Cloudflare Tunnel 常用模式，但可信网段必须填写 ShuffleMuse 实际看到的 Tunnel 对端地址，不能照抄与本机网络不符的示例。

## Host、Origin 与浏览器安全

每个 API 和 SPA 请求都会先执行 Host 校验。`MUSIC_ALLOWED_HOSTS` 可填写：

- 主机名：`music.example.com`；
- IP：`192.168.1.20`；
- 明确 authority：`music.example.com:8443`。

只填写主机名时会接受该主机的任意端口；填写带端口 authority 时可精确匹配该 authority。未命中返回 `400 INVALID_HOST`。

对 `POST` 和 `DELETE`：

- `Sec-Fetch-Site: cross-site` 返回 `403 CSRF_BLOCKED`；
- 如果存在 `Origin`，其 authority 必须与请求 Host 一致；
- 没有 `Origin` 的 CLI 请求保持兼容。

全局响应头包含 CSP、`X-Frame-Options: SAMEORIGIN`、`X-Content-Type-Options: nosniff`、`Referrer-Policy: same-origin` 和限制权限的 `Permissions-Policy`。ShuffleMuse 不终止 TLS，也不设置 HSTS；公网 HTTPS 应由反向代理负责。

## 部署示例

### 仅宿主机访问

当前 Compose 默认值已经适合：

```yaml
ports:
  - "127.0.0.1:8080:8080"
environment:
  MUSIC_PASSWORD: ""
  MUSIC_ALLOWED_HOSTS: "localhost,127.0.0.1,::1"
  MUSIC_REAL_IP_HEADER: "remote"
```

### 局域网直连

```yaml
ports:
  - "0.0.0.0:8080:8080"
environment:
  MUSIC_PASSWORD: "replace-with-a-long-random-password"
  MUSIC_ALLOWED_HOSTS: "music.lan,192.168.1.20"
  MUSIC_REAL_IP_HEADER: "remote"
  MUSIC_COOKIE_SECURE: "false"
```

局域网也可以由 HTTPS 反向代理接入；此时应启用 Secure Cookie。

### 反向代理与 XFF

```yaml
environment:
  MUSIC_PASSWORD: "replace-with-a-long-random-password"
  MUSIC_ALLOWED_HOSTS: "music.example.com"
  MUSIC_TRUSTED_PROXY_SUBNETS: "TRUSTED_PROXY_CIDR"
  MUSIC_REAL_IP_HEADER: "x-forwarded-for"
  MUSIC_COOKIE_SECURE: "true"
```

`TRUSTED_PROXY_CIDR` 必须替换为容器实际看到的代理来源网段。若代理转发的 Host 与浏览器访问 authority 不一致，Origin 校验会拒绝写请求。

### Cloudflare Tunnel

```yaml
environment:
  MUSIC_PASSWORD: "replace-with-a-long-random-password"
  MUSIC_ALLOWED_HOSTS: "music.example.com"
  MUSIC_TRUSTED_PROXY_SUBNETS: "ACTUAL_TUNNEL_PEER_CIDR"
  MUSIC_REAL_IP_HEADER: "cf-connecting-ip"
  MUSIC_COOKIE_SECURE: "true"
```

先从 ShuffleMuse 请求日志、容器网络或反向代理配置确认 `ACTUAL_TUNNEL_PEER_CIDR`，不要把 Cloudflare 公网访客网段误当作本地 Tunnel 对端。

## 媒体资源调优

`MUSIC_FFMPEG_MAX_SESSIONS` 是所有子进程的硬总上限。默认总数 2、辅助保留 1，形成两个不互借的 lane：最多一个长 Opus 转码，同时保留一个 metadata/封面任务。原文件串流和普通文件下载不占用这个配额。

- CPU 足够且同时用户较多时，可谨慎增加并发；
- 内存或 CPU 较小时，保持 `2` 或降低；
- `MUSIC_MEDIA_QUEUE_LIMIT=0` 或 `MUSIC_MEDIA_AUX_QUEUE_LIMIT=0` 分别关闭对应 lane 的等待队列；
- 队列满或等待超时会使转码、元数据和需要内嵌提取的封面请求返回 `503 MEDIA_BUSY`；
- 已取得槽位但执行超过 deadline 返回 `504 MEDIA_TIMEOUT`；
- 客户端断开会取消等待并释放队列位置；
- metadata 和封面相同身份的并发请求会在取得槽位前合并；封面转换只共享仍在进行的工作，完成后不保留图片结果；单个等待者取消不影响其他等待者；
- 辅助 lane 内 metadata 与 cover descriptor 优先于封面 JPEG 转换，但连续 4 个高优先级任务后会让等待中的转换执行一次；
- 元数据缓存键包含路径、大小和 mtime；确定性失败短暂负缓存，busy、超时和取消不缓存。

封面 descriptor 和 30 秒负缓存仍由上述兼容变量约束。转换结果只存在于当前请求或同一 in-flight 请求组中，响应结束后释放；浏览器凭 `private, max-age=3600` 和 ETag 复用结果。服务端不创建派生图片、磁盘缓存或图片字节 LRU。

## 服务端播放队列调优

每次随机化用独立系统熵播种 ChaCha8，再执行均匀 Fisher–Yates 洗牌。队列只保存 `uint32` 索引，并让同 generation 的队列共享只读曲库快照。浏览器固定每页读取 200 首且最多缓存 5 页。

- 新队列创建固定最多并发 2 个、等待 4 个、等待 5 秒；这些边界不可配置；
- 创建时清理 TTL 项，并在数量或字节预算不足时按最后访问时间淘汰；
- 单个队列连同其首次引用快照超过字节预算时返回 `503 QUEUE_CAPACITY`；
- 正常重扫不重排现有队列：删除项变为 `available:false`，新增项等下一次新建/Randomize；
- 默认 128 MiB 预算面向数万到约十万首曲库；极大曲库应按实际 `FileEntry` 字符串和 4 字节/候选顺序数组评估。

已认证的 `/api/status` 会返回当前生效的 `opusBitrate`，Web UI 的模式按钮和播放详情会据此显示实际转码码率。

## 旧标签路径迁移

标签数据库以相对 `MUSIC_DIR` 的路径为当前格式。设置 `MUSIC_LEGACY_MUSIC_ROOT` 后，每次成功扫描发布前会尝试迁移旧绝对路径，但只在同时满足以下条件时转换：

1. 旧路径确实位于配置的精确 legacy root 内；
2. 转换后的相对路径存在于本次成功扫描；
3. legacy root 是绝对路径。

无法明确转换的记录不会按任意后缀猜测，而是保留并出现在 Graveyard。完成迁移并确认数据后可清空该配置。
