# BENZHI 评测项目说明

本项目提供一个无状态的 INI 配置解析、按顺序合并和冲突检测 HTTP 服务。它支持严格语法校验、全局段、行内注释、引号转义，以及 `last-wins` 和 `fail-on-conflict` 两种合并策略。

## 标准构建、运行和测试

在本目录执行：

```bash
GOTOOLCHAIN=local go build ./...
GOTOOLCHAIN=local go test ./...
GOTOOLCHAIN=local go vet ./...
GOTOOLCHAIN=local go run . --smoke-test
GOTOOLCHAIN=local go run . server --addr :8080
```

`--smoke-test` 会启动进程内 HTTP 服务，执行完整自检并自行退出；正常服务使用 `server --addr` 启动 HTTP API。

## BENZHI Docker 构建

`build_benzhi_docker.sh` 只使用本目录的 `benzhi.Dockerfile`，参数依次为镜像名和平台，默认值为 `my-project` 与 `linux/amd64`：

```bash
chmod +x build_benzhi_docker.sh
./build_benzhi_docker.sh go-task043 linux/amd64
./build_benzhi_docker.sh go-task043 linux/arm64
docker run --rm go-task043 --smoke-test
```

容器启动后默认进入 bash，便于在容器内执行构建、测试和自检命令。
