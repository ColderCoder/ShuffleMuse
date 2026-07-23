# 部署与运维

本指南以默认 `docker-compose.yml` 为主，它拉取固定版本的公开 GHCR
镜像。`docker-compose.build.yml` 和 `docker-compose.build-cn.yml`
分别用于官方上游及中国大陆下载端点的源码构建。三份配置的运行参数
等价。直接运行二进制时，仍应复用相同的安全、持久化和健康检查原则。

## Compose 部署模型

当前 Compose 服务具备：

- 宿主机只绑定 `127.0.0.1:8080`；
- `./music:/music:ro`；
- 命名卷 `shufflemuse-data:/data`；
- `restart: unless-stopped`；
- 40 秒停止宽限期；
- 只读根文件系统；
- 64 MiB `/tmp` tmpfs；
- 丢弃全部 Linux capabilities；
- `no-new-privileges`；
- PID 上限 256；
- `/api/ready` healthcheck。

镜像只保证以非 root `shufflemuse` 用户运行。只读根、cap drop、PID
限制和 healthcheck 都来自 Compose；改用裸 `docker run` 时不会自动继承。

Compose 创建的实际卷名通常带项目名前缀，例如 `shufflemuse_shufflemuse-data`。用 `docker volume ls` 或容器 mount 信息确认，不要仅凭 YAML 中的逻辑名猜测。

## 首次部署

1. 准备音乐目录：

   ```bash
   mkdir -p music
   ```

2. 根据访问范围编辑 `docker-compose.yml`：

   - 仅本机：保持 loopback，可保留空密码；
   - 局域网：绑定 `0.0.0.0`，设置密码和允许 Host；
   - 公网：再增加 HTTPS 反向代理、Secure Cookie 和正确代理 IP 信任。

3. 验证并启动：

   ```bash
   docker compose config --quiet
   docker compose pull
   docker compose up -d
   docker compose ps
   docker compose logs -f shufflemuse
   ```

4. 等待首次扫描：

   ```bash
   curl -i http://127.0.0.1:8080/api/ready
   ```

首次扫描成功前容器 health 状态可能为 `starting` 或 `unhealthy`，数据 API 返回 503。大型曲库扫描只读取目录项，不运行 ffprobe，但文件系统速度、权限错误和损坏符号链接仍会影响结果。

## 官方上游源码构建

需要从当前检出构建时，所有命令都使用
`docker-compose.build.yml`：

```bash
docker compose -f docker-compose.build.yml config --quiet
docker compose -f docker-compose.build.yml up -d --build
docker compose -f docker-compose.build.yml logs -f shufflemuse
```

## 中国大陆网络替代构建

大陆网络使用独立文件，不修改默认国际构建：

```bash
docker compose -f docker-compose.build-cn.yml config --quiet
docker compose -f docker-compose.build-cn.yml up -d --build
docker compose -f docker-compose.build-cn.yml logs -f shufflemuse
```

三份 Compose 的服务名、端口、volume、环境变量、healthcheck、只读根和
权限限制一致；两份源码构建配置的区别只在下载端点：

| build arg | 默认值 | 用途 |
| --- | --- | --- |
| `DOCKERHUB_MIRROR` | `m.daocloud.io/docker.io` | DaoCloud 前缀代理三个 Docker Hub 基础镜像 |
| `BUN_REGISTRY` | `https://registry.npmmirror.com` | Bun/npm registry 与 lockfile tarball 前缀映射 |
| `GOPROXY` | `https://goproxy.cn,direct` | Go modules；代理缺少模块时允许直接回源 |
| `GOSUMDB` | `sum.golang.google.cn` | Go 官方提供给中国大陆访问的 checksum database alias，保留模块校验 |
| `ALPINE_MIRROR` | `https://mirrors.aliyun.com/alpine` | Alpine apk repository |

工作树中的 `web/bun.lock` 使用官方 `registry.npmjs.org`。`Dockerfile.cn` 复制 lockfile 后，只在该镜像层内把精确官方前缀映射为 `BUN_REGISTRY`，再执行 frozen install；版本和 integrity 不变，工作树也不会被改写。

Dockerfile frontend 由文件首行 `# syntax=` 独立固定到 DaoCloud 地址和 digest，不能通过 `DOCKERHUB_MIRROR` build arg 覆盖。若改用组织自建的 Dockerfile frontend 镜像，必须直接修改该行并重新核对 digest。

三个基础镜像继续固定与默认 Dockerfile 相同的 tag 和 manifest digest。DaoCloud 当前返回的 manifest digest 已逐一核对相同，但它、npmmirror、Goproxy.cn 和阿里云仍是额外第三方可用性与信任边界。若组织有自建镜像，可编辑大陆 Compose 中的 build args；不要移除 digest、frozen lock、Go checksum 或 apk 签名校验。

部署后应始终使用同一份 Compose 文件执行 stop、backup、upgrade 和 logs。例如大陆版本的停止命令是：

```bash
docker compose -f docker-compose.build-cn.yml stop shufflemuse
```

不要同时用多份 Compose 启动同一个项目；它们显式使用相同项目名、服务名
和命名卷。

## 配置联动

有些环境变量不能孤立修改：

| 修改 | 还必须检查 |
| --- | --- |
| `MUSIC_PORT` | Compose 端口映射的容器 target、healthcheck URL |
| `MUSIC_DIR` | volume target、只读权限和符号链接最终目标 |
| `MUSIC_BOLTDB_PATH` | 新路径必须位于可写 mount；只读根文件系统其他位置不可写 |
| 对外主机名/IP | `MUSIC_ALLOWED_HOSTS`、反向代理 Host 保留、浏览器 Origin |
| HTTP 改 HTTPS | `MUSIC_COOKIE_SECURE=true`、代理超时和流式转发 |
| 代理连接来源 | `MUSIC_TRUSTED_PROXY_SUBNETS` 和真实 IP header 模式 |

`MUSIC_ALLOWED_HOSTS` 只是 HTTP Host-header 校验，不是防火墙。真正限制默认网络暴露的是 `127.0.0.1` 端口绑定。

## 反向代理

反向代理应：

- 保留浏览器访问使用的 Host/port；
- 覆盖并正确构造真实 IP header；
- 对长音频流使用足够长的 read timeout；
- 禁止或减少转码响应缓冲；
- 终止 HTTPS；
- 不把代理地址放入认证免登录白名单。

宿主机 nginx 代理到 Compose loopback 的示例：

```nginx
server {
    listen 443 ssl;
    server_name music.example.com;

    # ssl_certificate /path/fullchain.pem;
    # ssl_certificate_key /path/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_read_timeout 3600s;
        proxy_send_timeout 60s;
        proxy_buffering off;
    }
}
```

对应配置通常类似：

```yaml
environment:
  MUSIC_PASSWORD: "replace-with-a-long-random-password"
  MUSIC_ALLOWED_HOSTS: "music.example.com"
  MUSIC_TRUSTED_PROXY_SUBNETS: "ACTUAL_PROXY_PEER_CIDR"
  MUSIC_REAL_IP_HEADER: "x-forwarded-for"
  MUSIC_COOKIE_SECURE: "true"
```

`ACTUAL_PROXY_PEER_CIDR` 必须替换为 ShuffleMuse 在 TCP 连接上实际看到的 nginx 来源地址或网段。即使 nginx 监听在宿主机，经过 Docker 端口映射后，容器看到的也未必是 `127.0.0.1`；代理在另一个容器中时通常是 Docker 网络地址。先从实际连接或日志确认，再配置最窄网段，不要直接照抄示例值。

Origin 校验比较浏览器 Origin authority 与转发后的 Host。代理若把 Host 改为内部 `127.0.0.1:8080`，浏览器写请求会得到 `403 CSRF_BLOCKED`。

## 健康、状态和扫描

### Readiness

`GET /api/ready`：

- 首次扫描成功前 503；
- 后续重扫期间 200；
- 已有快照的后续扫描失败后仍为 200；
- 关机开始后 503。

Compose 每 30 秒检查一次，timeout 10 秒，连续 3 次失败判定 unhealthy，首次启动有 30 秒 start period。

### 详细状态

已认证 `GET /api/status` 提供文件数、generation、扫描状态、最后成功时间和错误。密码模式未登录时只返回认证字段，不能作为完整监控源。

```bash
curl -b shufflemuse.cookies http://127.0.0.1:8080/api/status
```

### 扫描恢复

首次扫描失败后周期定时器不会自动重试。修复目录、权限或符号链接后：

```bash
curl -b shufflemuse.cookies -X POST \
  http://127.0.0.1:8080/api/rescan
```

也可在 Browse 点击 Rescan。请求返回 202 后继续轮询状态。后续重扫失败会保留旧快照。默认 `MUSIC_RESCAN_INTERVAL=0` 不会自动再试，应再次手动 Rescan；配置正数周期时才会在下一周期重试。

generation 只反映音频路径集合，不反映文件内容、mtime、标签或 metadata 变化。不要把 generation 当作整个数据库版本。

## 日志与诊断

```bash
docker compose ps -a
docker compose logs --tail=200 shufflemuse
docker compose logs -f shufflemuse
docker compose exec shufflemuse sh -c 'id; test -r /music; test -w /data'
docker compose exec shufflemuse ffmpeg -version
docker compose exec shufflemuse ffprobe -version
```

服务日志包括：

- 配置和认证警告；
- 客户端 IP 模式及可信代理数；
- 媒体并发上限；
- 每个 API 的方法、路径、状态和耗时；
- 扫描发布/失败；
- FFmpeg 错误；
- 关机步骤。

日志不提供结构化 JSON、rotation 或 request ID。Compose 默认使用 Docker logging driver，保留策略应在 Docker daemon 或 Compose 扩展配置中管理。

## 备份

应用需要备份的唯一内部持久化数据是 `/data/tags.db`。音乐目录、源代码和 Compose 配置应由各自的备份策略管理。

### 创建一致性备份

```bash
docker compose stop shufflemuse
docker compose run --rm --no-deps --entrypoint tar shufflemuse \
  -C /data -czf - . > shufflemuse-data.tar.gz
docker compose start shufflemuse
tar -tzf shufflemuse-data.tar.gz
```

为什么先停止：复制正在写入的 bbolt 文件不是应用级一致性快照。停止服务同时阻止标签写入和路径迁移。

归档不包含：

- `./music`；
- Session 和登录封禁；
- 内存 Index、generation 和 metadata cache；
- 浏览器队列；
- Compose 配置或密码。

Tags CSV 是人类可读导出，没有 import API，不能作为灾难恢复备份。

### 恢复

恢复命令会删除 `/data` 中现有内容。先验证文件存在且归档可读：

```bash
test -s shufflemuse-data.tar.gz
tar -tzf shufflemuse-data.tar.gz
```

然后：

```bash
docker compose down
docker compose run --rm --no-deps --entrypoint sh \
  -v "$PWD/shufflemuse-data.tar.gz:/backup.tar.gz:ro" shufflemuse \
  -c 'find /data -mindepth 1 -delete && tar -xzf /backup.tar.gz -C /data'
docker compose up -d
docker compose ps
docker compose logs --tail=200 shufflemuse
curl -i http://127.0.0.1:8080/api/ready
```

`docker compose down` 默认不删除命名卷；不要为普通恢复加入 `-v`。

恢复命令故意使用镜像默认的非 root `shufflemuse` 用户：Compose 已丢弃全部 capabilities，切到 UID 0 后再依赖 `chown` 并不可靠；以卷的正常 owner 写入时，解压文件会直接属于应用用户。若 `/data` 本身已经被外部操作改成不可写，应先单独诊断并修复卷权限，不要在恢复脚本里长期加入 root 权限。

## 从旧 `./data` bind mount 迁移到命名卷

如果旧部署使用 `./data:/data`：

1. 停止旧服务并单独备份 `./data`；
2. 确认当前 Compose 已改为 `shufflemuse-data:/data`；
3. 把旧目录复制到新命名卷：

```bash
docker compose down
docker compose run --rm --no-deps --entrypoint sh \
  -v "$PWD/data:/legacy:ro" shufflemuse \
  -c 'find /data -mindepth 1 -delete && cp -R /legacy/. /data/'
docker compose up -d
```

若旧标签 key 保存绝对音乐路径，再临时设置 `MUSIC_LEGACY_MUSIC_ROOT` 为旧部署的精确绝对音乐根。迁移只在一次成功扫描发布前执行；确认标签恢复后清空该变量。

## 升级与回滚

### 应用升级

1. 备份标签库和 Compose 配置。
2. 查看 [CHANGELOG](../CHANGELOG.md) 和配置差异，尤其是环境变量、volume
   和数据格式。
3. 将 Compose 的固定镜像标签或 digest 改为目标版本。
4. 拉取并观察启动：

   ```bash
   docker compose pull
   docker compose up -d
   docker compose logs -f shufflemuse
   ```

源码构建部署应在上述命令中持续加入
`-f docker-compose.build.yml` 或 `-f docker-compose.build-cn.yml`，并执行
`up -d --build`。Dockerfile 固定 Bun、Go、Alpine 的 tag 和 digest，也固定
Alpine FFmpeg 包版本；`--pull` 不会自动升级这些固定值。

### 回滚

项目没有数据库 schema 版本、迁移历史或自动回滚工具。回滚前：

- 保存当前 `tags.db`；
- 恢复到明确的源码/镜像版本；
- 如果新版本曾改变标签数据语义，恢复升级前数据库备份；
- 启动后核对 Tags、Graveyard 和 CSV。

回滚版本可通过 `shufflemuse --version`、镜像 OCI labels 和 GHCR digest
确认。运维记录应保存不可变 digest，而不是只记录 `latest`。

## 构建网络

默认 Dockerfile 使用 Docker Hub、官方 npm registry、`proxy.golang.org` 和 Alpine CDN。只需替换默认构建的 Go proxy 时可传：

```bash
docker compose -f docker-compose.build.yml build \
  --build-arg GOPROXY=https://your-go-proxy.example,direct
```

需要替换全部构建工具链时使用[中国大陆网络替代构建](#中国大陆网络替代构建)，不要直接改主 lockfile 的 URL。两套构建的依赖更新和验证规则见[开发指南](DEVELOPMENT.md)。

## 安全运维清单

- [ ] 非本机访问已设置长随机密码。
- [ ] 端口绑定与实际访问范围一致。
- [ ] `MUSIC_ALLOWED_HOSTS` 只含需要的主机名/IP。
- [ ] 公网由 HTTPS 反向代理保护。
- [ ] HTTPS 部署启用了 Secure Cookie。
- [ ] 可信代理仅包含实际直连代理来源。
- [ ] 代理不在认证白名单内。
- [ ] 代理保留外部 Host 并覆盖真实 IP header。
- [ ] 音乐目录中没有可下载的密钥、备份或配置。
- [ ] `/data` 位于持久化、可写 volume。
- [ ] 定期执行停止服务的一致性备份并验证归档。
- [ ] Docker 日志和磁盘有容量策略。
- [ ] `ffmpeg` 与 `ffprobe` 都可执行。

## 故障排查

| 现象 | 可能原因 | 检查与处理 |
| --- | --- | --- |
| 启动时报 `invalid config` | 值越界、CIDR 非法、legacy root 非绝对路径 | 查看完整日志和[配置表](CONFIGURATION.md) |
| `open tag store` / permission denied | `/data` 不可写或 owner 不正确 | 检查 mount、`id`、`test -w /data` |
| healthcheck 长期失败 | 首次扫描失败、端口联动错误、服务退出 | 看 `/api/ready` 和容器日志 |
| `503 LIBRARY_SCANNING` | 尚无成功快照 | 等待；初始失败修复后手动 Rescan |
| `400 INVALID_HOST` | 请求 Host 未允许 | 补 `MUSIC_ALLOWED_HOSTS`，不是关闭校验 |
| `403 CSRF_BLOCKED` | 反代未保留 Host，或 Origin/Host 不一致 | 检查代理的 Host header |
| 登录后仍 401 | Cookie 未保存/回传，或服务已重启 | HTTPS 检查 Secure；纯 HTTP 不应设 Secure=true |
| `429 LOGIN_IP_BLOCKED` | 该解析 IP 达到失败阈值 | 等待 `Retry-After`；不要持续重试 |
| 所有用户共享同一封禁 | 真实 IP header/可信代理配置错误 | 核对 ShuffleMuse 看到的 TCP peer 和链路 |
| `503 MEDIA_BUSY` | 运行槽和等待队列已满或超时 | 降低并发请求，或按资源调优限制 |
| metadata 不可用 | ffprobe 缺失、媒体损坏、busy | 执行 `ffprobe -version`，看日志 |
| 封面错误 | 无封面、超 20 MiB/8192 单边/40 MP、FFmpeg 失败或 busy | 区分 `COVER_NOT_FOUND`、`COVER_ERROR` 与 `MEDIA_BUSY`；服务端不会保留转换图或降级发送超大原图 |
| 标签进入 Graveyard | 路径移动、根目录变化或旧路径未迁移 | 核对路径；恢复同路径或清理孤立标签 |
| 改端口后 unhealthy | 容器 target/healthcheck 未同步 | 同步三处端口配置 |
| 默认 Docker 前端安装失败 | 无法访问官方 npm registry | 检查网络，或使用独立大陆 Compose |
| 大陆 Docker 构建访问镜像失败 | 第三方服务不可达或 build arg 已过期 | 检查五个 build args；必要时切换到组织自建镜像并保留校验 |

## 数据删除警告

以下命令会删除标签数据，日常停止/升级不要使用：

```bash
docker compose down -v
```

Graveyard 的删除按钮只删指定路径的标签记录；`down -v` 会删除整个 Compose 命名卷，影响所有标签和收藏。
