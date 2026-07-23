# HTTP API

ShuffleMuse 的 Web UI 与自动化客户端使用同一组 `/api` 路由。生产环境通常与 SPA 同源，不提供跨域 CORS 配置。

## 公共约定

### 基础地址与认证

- Compose 默认基础地址：`http://127.0.0.1:8080/api`。
- 密码模式使用 `shufflemuse-session` Cookie，不使用 Bearer Token。
- 认证白名单内的 TCP 直连对端无需 Cookie。
- 无密码模式下除登录/退出路由不存在外，其余 API 全部公开。

密码模式路由矩阵：

| 路由 | 未登录可访问 |
| --- | --- |
| `POST /api/auth/login` | 是 |
| `POST /api/auth/logout` | 是，用于清 Cookie；不要求已有有效 Session |
| `GET /api/status` | 是，但未认证响应最小化 |
| `GET /api/ready` | 是 |
| `POST /api/rescan` | 否 |
| 其他曲库、Browse、标签、媒体和 Graveyard 路由 | 否 |

认证检查先于曲库 readiness 检查。因此密码模式下未登录访问曲库接口，即使首次扫描仍在进行，也先得到 `401 UNAUTHORIZED`。

### 错误结构

JSON 错误统一使用：

```json
{
  "error": "human-readable message",
  "code": "STABLE_CODE"
}
```

`error` 适合展示或排障，客户端分支应优先使用 `code` 和 HTTP 状态。

### 请求安全边界

- Host 必须命中 `MUSIC_ALLOWED_HOSTS`，否则 `400 INVALID_HOST`。
- `POST` / `DELETE` 的跨站 `Sec-Fetch-Site` 或 Origin/Host 不一致会得到 `403 CSRF_BLOCKED`。
- CLI 可不发送 Origin。
- 登录、新增标签和队列 POST body 最大 8 KiB，并严格拒绝未知字段和第二个尾随 JSON 对象。
- 当前严格 JSON 解码器不强制 `Content-Type: application/json`，但客户端仍应正确设置。
- `q` 最大 200 UTF-8 字节；`dir`、`path` 最大 4096 字节。

### 分页

支持分页的端点接受：

| 参数 | 规则 |
| --- | --- |
| `page` | 默认 1，正整数，只能出现一次 |
| `limit` | 端点默认值，正整数，只能出现一次，最大 1000 |

非法分页返回 `400 INVALID_PAGINATION`。请求超过最后一页不是错误，会返回空数组和不变的 `total`。

### 文件条目

曲库、搜索和标签文件列表使用：

```json
{
  "id": "base64url-stable-id",
  "filepath": "Artist/Album/track.flac",
  "name": "track",
  "dir": "Artist/Album"
}
```

`filepath` 和 `dir` 相对 `MUSIC_DIR`。`id` 由清理后的相对路径派生；文件移动或重命名会得到新 ID。

## 认证与控制端点

### `POST /api/auth/login`

仅在密码模式注册。

请求：

```json
{
  "password": "secret",
  "remember": false
}
```

成功：`200 OK`，设置 Cookie。

```json
{"status":"logged in"}
```

失败：

- `400 INVALID_JSON`；
- `413 REQUEST_TOO_LARGE`；
- `401 UNAUTHORIZED`；
- `429 LOGIN_IP_BLOCKED`，并提供 `Retry-After` 秒数。

curl 示例：

```bash
curl -i -c shufflemuse.cookies \
  -H 'Content-Type: application/json' \
  --data '{"password":"secret","remember":false}' \
  http://127.0.0.1:8080/api/auth/login
```

### `POST /api/auth/logout`

密码模式下始终注册。撤销当前已知 Token、设置过期 Cookie，并返回 `204 No Content`。未知或缺失 Cookie 仍返回 204。

```bash
curl -i -b shufflemuse.cookies -X POST \
  http://127.0.0.1:8080/api/auth/logout
```

### `GET /api/status`

未认证的密码模式响应只包含：

```json
{
  "authRequired": true,
  "authenticated": false
}
```

已认证的密码模式响应示例：

```json
{
  "fileCount": 68,
  "libraryReady": true,
  "libraryGeneration": 3,
  "scanStatus": "idle",
  "opusBitrate": 160,
  "uptime": "1h2m3s",
  "lastScan": "2026-07-20T12:34:56Z",
  "scanError": null,
  "authRequired": true,
  "authenticated": true
}
```

无密码模式和 TCP 对端命中认证白名单时，同一完整响应中的 `authRequired` 为 `false`、`authenticated` 为 `true`。

字段：

| 字段 | 说明 |
| --- | --- |
| `fileCount` | 当前快照中的音频数 |
| `libraryReady` | 数据端点是否可用 |
| `libraryGeneration` | 路径集合版本；仅路径集合变化时增加 |
| `scanStatus` | `initializing`、`idle`、`scanning` 或 `error` |
| `opusBitrate` | 当前生效的 Opus 转码码率，单位 kbps |
| `lastScan` | 最近成功扫描的 RFC3339 时间；从未成功时为 `null` |
| `scanError` | 最近扫描错误；没有时为 `null` |

白名单客户端会看到 `authRequired:false`、`authenticated:true`，即使全局配置了密码。

### `GET /api/ready`

此端点在密码模式下也公开，适合容器健康检查。

就绪：

```http
HTTP/1.1 200 OK

{"ready":true,"generation":3}
```

未就绪：

```http
HTTP/1.1 503 Service Unavailable

{"ready":false,"generation":0,"code":"LIBRARY_SCANNING"}
```

首次扫描成功后，后续后台重扫期间继续返回 200；服务开始关机时立即返回 503。

### `POST /api/rescan`

接受一次异步全量扫描请求：

```http
HTTP/1.1 202 Accepted

{"status":"accepted"}
```

可能错误：

- `409 LIBRARY_INITIALIZING`：首次扫描尚未完成；
- `409 SCAN_IN_PROGRESS`：已有扫描；
- `409 RESCAN_UNAVAILABLE`：当前 API 没有 Rescanner；
- `500 RESCAN_ERROR`。

请求成功只表示已接受，不表示扫描完成。轮询 `/api/status` 观察 `scanStatus`、`scanError` 和 generation。

## 曲库与搜索

### `GET /api/files`

默认 `limit=50`。

```json
{
  "items": [],
  "total": 68,
  "page": 1,
  "generation": 3
}
```

## 服务端播放队列

队列 ID 是 32 字节随机值的 Base64URL 表示；服务端只保存 SHA-256 摘要。队列排列不可变，分页只是同一排列的切片，固定每页 200 条且不接受 `limit`。

队列描述：

```json
{
  "id": "opaque-base64url-token",
  "tag": "favorite",
  "createdGeneration": 3,
  "total": 68,
  "pageSize": 200
}
```

队列项在普通文件字段之外增加零基 `queueIndex` 与请求时的在线状态：

```json
{
  "id": "file-id",
  "filepath": "Artist/track.flac",
  "name": "track",
  "dir": "Artist",
  "queueIndex": 0,
  "available": true
}
```

### `POST /api/queues`

请求字段均可省略：

```json
{
  "tag": "favorite",
  "pinFileId": "file-id",
  "replaceQueueId": "old-queue-token"
}
```

`tag` 限定候选；`pinFileId` 只在该在线文件也属于标签候选时固定到第一位，
否则忽略并返回 `pinApplied:false`。这保证标签队列不会混入标签外曲目。
`replaceQueueId` 只在新队列完整创建后删除旧队列。初始队列和 Randomize
不发送 pin。

成功返回 `201 Created`，包含 `queue`、第一页 `items`、`page:1`、`libraryGeneration` 和 `pinApplied`。

### `GET /api/queues/{id}/items?page=N`

返回 `queue`、固定最多 200 个 `items`、`page` 和请求时的 `libraryGeneration`。正常重扫后 ID、顺序、位置和 total 不变；离线项只变为 `available:false`，新文件不会插入旧队列。

### `POST /api/queues/{id}/select`

```json
{"fileId":"file-id"}
```

文件已经在队列中时返回原队列、零基 `queueIndex`、所在页及该页。文件在线但不在队列中时，原子创建以该文件为首、保留原剩余顺序的替代队列，并在响应的 `queue.id` 返回新令牌。

### `DELETE /api/queues/{id}`

幂等返回 `204 No Content`，令牌不存在或格式无效也相同。

队列稳定错误：`404 QUEUE_NOT_FOUND`、`404 FILE_NOT_FOUND`、`503 QUEUE_BUSY`、`503 QUEUE_CAPACITY`。JSON、分页、认证和内部错误沿用公共错误码。

### `GET /api/search?q=<term>`

默认 `limit=50`。对文件的无扩展名 `name` 做 Unicode 字符串的大小写转换后子串匹配；不搜索目录、标签或媒体 metadata。

```json
{
  "items": [],
  "total": 7,
  "page": 1,
  "query": "summer",
  "generation": 3
}
```

错误：`400 MISSING_QUERY`、`400 QUERY_TOO_LONG`、分页错误。

### `GET /api/files/{id}/metadata`

成功：

```json
{
  "codec": "FLAC",
  "bitrateKbps": 986,
  "bitrateApproximate": false,
  "durationSeconds": 245.32
}
```

ffprobe 优先使用第一条音轨 bitrate，其次使用容器 bitrate；两者都缺失时按文件大小和时长估算，并设置 `bitrateApproximate:true`。

错误：`404 NOT_FOUND`、`503 MEDIA_BUSY`、`504 MEDIA_TIMEOUT`、`422 METADATA_ERROR`。

### `GET|HEAD /api/covers/directory?dir=...`

返回指定相对目录同级、大小写不敏感的 `cover.jpg` 或 `cover.png`，JPEG 优先。`dir` 必须恰好出现一次并使用干净的相对路径；路径穿越、绝对路径、反斜杠和任一目录 symlink 返回 `400 INVALID_DIR`。目录不存在或没有可用封面返回 `404 COVER_NOT_FOUND`。该端点不会递归查找子目录，也不会探测音频内嵌封面。

### `GET|HEAD /api/files/{id}/cover`

顺序：同目录 `cover.jpg` → `cover.png` → 音频内嵌第一视频流。外置封面不接受 symlink。文件严格超过 20 MiB、任一边严格超过 8192 或总像素严格超过 40 MP 时返回 `404 COVER_NOT_FOUND`。

外置封面任一边严格超过 1536 或文件严格超过 1 MiB 时，实时转换为最长边 1024、不放大的 JPEG q3；PNG 先合成白底。JPEG 转换结果不超过原文件 85% 才采用，否则本次请求发送原 JPEG。小型外置封面原样发送，因此未触发转换的 PNG 保留透明度。内嵌封面固定输出最长边 1024 的 JPEG q3。

响应头：

- `Content-Type`：图片类型；
- `Content-Disposition: inline`；转换输出文件名为 `cover.jpg`；
- `Cache-Control: private, max-age=3600`；
- `ETag`：由源路径、大小、mtime、阈值和编码规格生成；
- `X-Cover-Source: embedded|cover.jpg|cover.png`。

HEAD 和能得到 304 的条件请求只发现 descriptor，不启动 FFmpeg。可能转换的 JPEG/PNG 在 HEAD 中预先声明 `image/jpeg`，但不声明未知的转换后 `Content-Length`。每个非缓存 GET 都重新转换；仅并发中的相同转换 singleflight，不建立服务端图片结果缓存。错误：`404 NOT_FOUND`、`404 COVER_NOT_FOUND`、`503 MEDIA_BUSY`、`504 MEDIA_TIMEOUT`、`500 COVER_ERROR`。等待队列满或等待超时返回 `503 MEDIA_BUSY`；15 秒执行 deadline 返回 504，FFmpeg 失败不会降级发送超大原图。

## Browse

### `GET /api/browse`

参数：`dir` 默认为根目录 `.`，默认 `limit=100`。先对目录排序，再对文件排序，然后把两组拼成一个统一分页序列。为避免任意深页码迫使服务端在内存中保留整个超大目录，单次请求最多排序前 50,000 个条目；仍落在目录内容范围内的更深分页返回 `400 INVALID_PAGINATION`，已经超出目录末尾的分页仍返回空页。

```json
{
  "directories": [
    {"name":"Artist","path":"Artist"}
  ],
  "files": [
    {
      "id":"...",
      "name":"cover.jpg",
      "path":"cover.jpg",
      "dir":".",
      "kind":"image",
      "mimeType":"image/jpeg",
      "size":12345,
      "modified":"2026-07-20T12:34:56Z",
      "previewable":true,
      "playable":false
    }
  ],
  "total":2,
  "page":1,
  "generation":3
}
```

音频文件还包含 `playable:true`、`audioId` 和不含扩展名的 `trackName`。

错误：`400 INVALID_DIRECTORY`、`400 QUERY_TOO_LONG`、`404 NOT_FOUND`、`500 BROWSE_ERROR`、分页错误。

### `GET|HEAD /api/browse/content?path=...`

以内联方式返回允许预览的原文件：

- 图片：JPG/JPEG/PNG/GIF/WebP/AVIF/BMP；
- PDF；
- 不超过 2 MiB 的 TXT/CUE/LOG/M3U/M3U8/MD/NFO/LRC/JSON。

不允许的类型或超大文本返回 `415 PREVIEW_UNAVAILABLE`。路径不存在、不是普通文件或不在音乐根内返回 `404 NOT_FOUND`。

### `GET|HEAD /api/browse/download?path=...`

以 attachment 返回任意可浏览普通文件的原始内容，不会转码。音频为 `Cache-Control: private, no-store`，其他文件为 `private, max-age=3600`。两种文件服务均由 `http.ServeContent` 支持 Range 和条件请求。

Browse 路径必须位于音乐根，且不能包含隐藏组件或系统杂项名称。不要在音乐根内存放可被下载的敏感普通文件。

## 标签

### `GET /api/files/{id}/tags`

```json
{"tags":["favorite","rock"]}
```

### `POST /api/files/{id}/tags`

请求：

```json
{"tag":"favorite"}
```

成功：`201 Created`。

```json
{"tag":"favorite"}
```

服务端标签限制：1–50 个 ASCII 字节，只允许字母、数字、`.`、`-`、`_`。同一文件上的完全相同标签返回 `409 DUPLICATE_TAG`；服务端区分大小写，Web UI 额外执行大小写不敏感的重复检查。

其他错误：`400 MISSING_TAG`、`400 INVALID_TAG`、严格 JSON 错误、`404 NOT_FOUND`。

### `DELETE /api/files/{id}/tags/{tag}`

成功 `204 No Content`。文件或关联不存在时返回 `404 NOT_FOUND`。客户端应对路径段进行 URL 编码。

### `GET /api/tags`

只包含当前曲库快照中的在线文件：

```json
{
  "tags": [
    {"name":"favorite","count":12},
    {"name":"rock","count":8}
  ]
}
```

仅存在于 missing 路径的标签不会出现。

### `GET /api/tags/{tag}/files`

默认 `limit=50`，只返回当前在线文件；Web UI 固定请求 200 条一页。服务端在一个有取消检查的顺序遍历中同时计算在线总数并只收集目标页，不会为每一页先物化完整标签文件数组：

```json
{
  "items": [],
  "total": 12,
  "page": 1,
  "generation": 3
}
```

### `GET /api/tags/export`

下载 `shufflemuse-tags.csv`：

- `Content-Type: text/csv; charset=utf-8`；
- `Content-Disposition: attachment`；
- `Cache-Control: private, no-store`；
- UTF-8 BOM；
- 表头 `filepath,name,dir,tags,status`；
- 每个持久化路径一行，含 online 与 missing；
- 多标签按名称排序并以分号连接；
- 行按 filepath 排序；
- 危险公式起始字符前增加 `'`。

导出不接受标签筛选参数，也没有 CSV 导入 API。

## Graveyard

### `GET /api/graveyard`

默认 `limit=50`。

```json
{
  "items": [
    {
      "filepath":"Old/track.flac",
      "name":"track",
      "dir":"Old",
      "tags":["favorite","lost"]
    }
  ],
  "total":1,
  "page":1,
  "generation":3
}
```

### `DELETE /api/graveyard?path=...`

删除指定缺失路径的全部标签关联，不删除磁盘文件。不存在的路径也返回 `204 No Content`，因此操作是幂等的。

如果路径在当前快照中在线，返回：

```http
HTTP/1.1 409 Conflict

{"code":"FILE_ONLINE","error":"tagged file is online"}
```

缺失 `path` 返回 `400 MISSING_PATH`，超长返回 `400 QUERY_TOO_LONG`。

## 音频串流

### `GET|HEAD /api/stream/{id}`

| 查询参数 | 行为 |
| --- | --- |
| 无 `mode` | `.opus` 原样发送；其他音频实时转码。这是兼容模式，不是 Web UI 默认请求。 |
| `mode=original` | 原文件，支持 Range |
| `mode=opus` | 强制转码为 Ogg Opus |
| `mode=opus&start=12.5` | 从 12.5 秒开始转码 |

`start` 必须是有限、非负数字，且只能与 `mode=opus` 一起使用。其他 mode 返回 `400 INVALID_STREAM_OPTIONS`。

原文件响应：

- 扩展名对应音频 `Content-Type`；
- `Accept-Ranges: bytes`；
- `Cache-Control: private, no-store`。

转码响应：

- `Content-Type: audio/ogg; codecs=opus`；
- `Cache-Control: private, no-store`；
- `X-Accel-Buffering: no`；
- 不提供 Range。

HEAD 对需要转码的请求只返回头，不启动 FFmpeg。转码映射第一条音轨并移除视频/封面流。请求取消会终止 FFmpeg。

错误：`404 NOT_FOUND`、`400 INVALID_STREAM_OPTIONS`、`503 MEDIA_BUSY`、`504 MEDIA_TIMEOUT`、`500 STREAM_ERROR`。Opus 启动后 15 秒仍未成功写出首字节时，若响应尚未提交则返回 504；响应体已经开始传输后只能关闭流并记录日志。

## 稳定错误码索引

| HTTP | code | 常见来源 |
| ---: | --- | --- |
| 400 | `INVALID_HOST` | Host 不在允许列表 |
| 400 | `INVALID_JSON` | 畸形、未知字段或尾随 JSON |
| 400 | `INVALID_PAGINATION` | page/limit 非法或 limit > 1000 |
| 400 | `QUERY_TOO_LONG` | q/dir/path 超过字节限制 |
| 400 | `MISSING_QUERY` | Search 缺 q |
| 400 | `INVALID_DIRECTORY` | Browse dir 逃出音乐根或格式非法 |
| 400 | `INVALID_DIR` | 目录封面 dir 缺失、重复、逃逸或包含 symlink |
| 400 | `MISSING_TAG` / `INVALID_TAG` | 标签 body 非法 |
| 400 | `INVALID_STREAM_OPTIONS` | mode/start 非法 |
| 400 | `MISSING_PATH` | Graveyard 删除缺 path |
| 401 | `UNAUTHORIZED` | 未登录或密码错误 |
| 403 | `CSRF_BLOCKED` | 跨站请求或 Origin/Host 不一致 |
| 404 | `NOT_FOUND` | 文件、目录、ID 或标签关联不存在 |
| 404 | `COVER_NOT_FOUND` | 没有可用封面 |
| 409 | `DUPLICATE_TAG` | 重复添加完全相同标签 |
| 409 | `FILE_ONLINE` | 试图从 Graveyard 删除在线路径 |
| 409 | `LIBRARY_INITIALIZING` | 首次扫描中发起重扫 |
| 409 | `SCAN_IN_PROGRESS` | 已有重扫 |
| 413 | `REQUEST_TOO_LARGE` | JSON body 超过 8 KiB |
| 415 | `PREVIEW_UNAVAILABLE` | 文件类型或文本大小不可预览 |
| 422 | `METADATA_ERROR` | ffprobe 或 metadata 不可用 |
| 429 | `LOGIN_IP_BLOCKED` | 登录 IP 已封禁 |
| 500 | `BROWSE_ERROR` | 目录读取失败 |
| 500 | `TAG_ERROR` / `CSV_ERROR` | 标签库或 CSV 生成失败 |
| 500 | `COVER_ERROR` | 封面读取/提取失败 |
| 500 | `RESCAN_ERROR` | 无法接受重扫 |
| 500 | `STREAM_ERROR` | 流开始前发生内部错误 |
| 503 | `LIBRARY_SCANNING` | 曲库尚未有成功快照 |
| 503 | `MEDIA_BUSY` | 转码、metadata 或封面提取的媒体槽满或超时 |
| 503 | `QUEUE_BUSY` / `QUEUE_CAPACITY` | 队列构建等待或缓存预算不足 |
| 504 | `MEDIA_TIMEOUT` | 已启动的媒体任务或 Opus 首字节超过 deadline |

## 自动化建议

- 监控只使用 `/api/ready`，状态面板使用已认证 `/api/status`；
- 写请求不要伪造浏览器跨站头；经反向代理时保留外部 Host；
- 登录后把 Cookie jar 限制为私密文件；
- 处理列表时记录 generation；多页 generation 变化时从第一页重试；
- 不要把 CSV 当作可恢复数据库；
- 对 429 使用 `Retry-After`，对 503 使用有上限的退避，不要立即无限重试。
