# ShuffleMuse 文档

本目录描述当前工作树中的实际行为。配置、API 或生命周期发生变化时，应同时更新对应文档和根目录 [README](../README.md)。

## 按角色阅读

### 使用者

1. [用户指南](USER_GUIDE.md)：了解页面、播放模式、搜索、标签、CSV 和 Graveyard。
2. [配置与安全](CONFIGURATION.md)：在开放局域网、反向代理或公网访问前完成安全配置。

### 运维者

1. [部署与运维](OPERATIONS.md)：部署、健康检查、备份恢复、升级和故障排查。
2. [HTTP API](API.md)：监控、自动化和错误码。
3. [架构说明](ARCHITECTURE.md)：理解扫描快照、generation、持久化边界和关机顺序。

### 开发者

1. [开发指南](DEVELOPMENT.md)：干净检出后的构建顺序、开发服务器和完整验证命令。
2. [架构说明](ARCHITECTURE.md)：包职责、后端并发模型和前端状态一致性。
3. [HTTP API](API.md)：前后端契约和边界限制。
4. [项目审计](PROJECT_AUDIT.md)：当前实现风险、测试证据和建议处理顺序。
5. [贡献指南](../CONTRIBUTING.md)：提交要求和发布前检查。

版本变化见 [CHANGELOG](../CHANGELOG.md)，安全漏洞按
[SECURITY policy](../SECURITY.md) 私下报告。

## 文档职责

| 文件 | 负责回答的问题 |
| --- | --- |
| [README.md](../README.md) | 这是什么、怎样安全启动、下一步读哪里？ |
| [USER_GUIDE.md](USER_GUIDE.md) | 在 Web UI 中怎样完成日常操作？ |
| [CONFIGURATION.md](CONFIGURATION.md) | 每个配置项怎样解析，安全边界是什么？ |
| [API.md](API.md) | HTTP 请求、响应和稳定错误码是什么？ |
| [ARCHITECTURE.md](ARCHITECTURE.md) | 系统为何这样工作，状态如何流动？ |
| [OPERATIONS.md](OPERATIONS.md) | 怎样用默认或大陆构建部署、监控、备份、恢复和排障？ |
| [DEVELOPMENT.md](DEVELOPMENT.md) | 怎样修改、构建和验证项目？ |
| [PROJECT_AUDIT.md](PROJECT_AUDIT.md) | 当前实现经过了哪些检查，还存在哪些风险？ |

## 事实来源

- 服务启动与关机：`cmd/server/main.go`
- 配置：`internal/config/config.go`、`docker-compose.yml`
- API：`internal/api/`
- 扫描和曲库快照：`internal/index/`
- 认证：`internal/auth/`
- 标签数据库：`internal/tags/`
- 媒体处理：`internal/stream/`、`internal/cover/`、`internal/mediaexec/`
- Web UI：`web/src/`
- 构建：`Dockerfile`、`go.mod`、`web/package.json`、`web/bun.lock`
