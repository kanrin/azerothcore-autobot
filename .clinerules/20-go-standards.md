# 20 · Go 工程基线与依赖规则

## 基线

- 模块 `azerothcore-autobot`，`go 1.26`（本机 go1.26.4 / darwin-arm64）。
- 必须通过：`gofmt -l .`（无输出）、`go vet ./...`、`go build ./...`、`go test ./...`（离线单元测试）。
- 建议通过：`staticcheck ./...`（若已安装）；提交前再跑一次 `go test -race ./...`。
- UTF-8 + LF，120 列，行尾不留空格（沿用 AzerothCore 的 `.editorconfig` 风格）。
- 标识符、日志、错误文本用英文；注释可用中文；CLI 帮助文案可用中文。

## 依赖规则（重要）

1. 首选标准库；其次用已有依赖：`github.com/azerothcore/AzerothGhost`、
   `github.com/go-sql-driver/mysql`、`gopkg.in/yaml.v3`。新增依赖前先问用户。
2. 固定 `github.com/azerothcore/AzerothGhost v1.0.9`，不要用 `@latest`。
3. **保留上游 detour 的 `replace`；配对的 `require` 让 `go mod tidy` 决定**。AzerothGhost 的 go.mod 把
   `github.com/o0olele/detour-go` 指到 `github.com/walkline/detour-go`，而 `replace` 不跨模块传递，所以本仓库
   的 go.mod 保留同一行（行上方已写清原因）。
   实测修正（2026-10-11）：**只有 import 到依赖 detour 的包时才需要那条 require**。Go 1.17+ 按实际 import
   剪枝模块图，所以仅 import `client` + `e2e/e2eharness` 时 `go mod tidy` 会删掉 require 并成功返回；
   等引入 navmesh/movement 之类包时 tidy 会自动加回来。别手工塞回去 —— 会被 tidy 再删，造成 diff 抖动。
4. 上游接口不够用时先在本仓库写薄封装，不要 fork。确需上游改动时说明理由并问用户，优先向上游提 PR。
5. 与上游联调（本地 checkout）用 `go.work` 或临时 `replace` 指向本地路径；`go.work` 必须 gitignore。
6. 本机 `GOPROXY` 是 `https://goproxy.cn`；拉不到包先确认网络/代理，别急着换依赖。

## 代码组织

- 包名短小写无下划线；目录即职责（见 `10-architecture.md`）。
- 一个文件一个主题，单文件 **< 500 行**，超过就拆。
- 导出面最小：默认不导出，跨包才导出，并写清用途。
- 不建 `utils`/`common` 垃圾包；通用件放进它服务的层。
- 测试与被测代码同目录：同包测试或 `_test` 外部测试包。
- 从上游或其它项目拷贝的代码，文件头注明来源与许可（AzerothGhost 为 MIT，AzerothCore 为 GPL-2.0）。

## 错误处理

- 错误包装上下文：`fmt.Errorf("lfg: join queue: %w", err)`；判定用 `errors.Is/As`，不比较字符串。
- 预期内的"前置未满足"用哨兵错误（`ErrXxx`），与基础设施错误区分。
- 库代码不 `panic`、不 `os.Exit`；启动期配置错误可以 fatal。
- 需要取消或超时的长流程一律带 `context.Context`。
- 资源必须关闭；重连先关旧连接再建新连接（否则会踩服务端 `session_key` 被覆盖的坑）。
- 数值类型贴合协议：GUID `uint64`、opcode `uint16`、坐标 `float32`。

## 并发

- 不用 `time.Sleep` 做同步；用 channel / ticker / 等待器（见 `50-testing-live-stack.md`）。
- 共享状态归属明确：属于 `WorldClient` 的用它的 getter，属于本组件的自带 mutex。
- 每个 goroutine 有退出信号并纳入 `WaitGroup`，不允许泄漏。
- 不用会在 `-race` 下报错的设计。

## 测试守则

- 纯逻辑（包编解码、状态机迁移、名称生成、任务选择）必须有不依赖网络的单元测试，优先表驱动。
- 包编解码用 **golden bytes**：期望字节来自 AC 源码或抓包，写死对比。
- 实盘测试见 `50-testing-live-stack.md`，带 `//go:build e2e`，离线 `go test ./...` 必须跳过。
- 不为通过测试放宽断言；宁可报 precondition 失败。

## 提交

- 一个逻辑改动一个 commit，信息格式 `type(scope): 祈使句摘要`（例：`feat(lfg): join random dungeon queue`）。
- body 写"做了什么 + 怎么验证的"（命令、关键日志、DB 查询结果）。
- 不提交密钥、`.local.yml` profile、`go.work`、抓包文件、临时脚本。
