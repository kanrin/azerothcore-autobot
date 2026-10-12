# 10 · 架构与分层

## 分层

```
cmd/autobot/                 CLI 入口：run / login / probe / quest / dungeon / party
  └── internal/agent         目标调度器（tick 循环 + 黑板 + 行为注册表）
        ├── internal/feature/quest    任务子系统（世界知识 → 计划 → 驱动 → 校验）
        ├── internal/feature/party    组队接受策略 / 跟随 / 辅助
        ├── internal/feature/lfg      LFG 客户端协议（join/rolecheck/proposal/teleport）
        ├── internal/feature/dungeon  副本内路线与推进
        ├── internal/feature/travel   寻路导航、飞行点、必要时的传送
        └── internal/feature/combat   目标选择、职业循环、回血/进食
  └── internal/wow           对 AzerothGhost client 的薄封装：会话、等待器、包编解码
  └── internal/wowdata       数据库只读世界知识 + 校验（acore_world / acore_characters / acore_auth）
  └── internal/config        profile(YAML) + 环境变量 + flag，统一优先级
  └── internal/telemetry     结构化事件（JSONL）、决策追踪、运行摘要
```

依赖方向只能自外向内：`cmd → agent → feature → wow → AzerothGhost`。
`feature` 之间不要互相 import；需要协作时通过 `agent` 的黑板（blackboard）与显式接口。

## 分层职责（不要越界）

| 层 | 只做 | 不做 |
|----|------|------|
| `wow` | 连接、阶段等待、包编解码、状态缓存快照 | 业务决策、循环、重试策略 |
| `feature/*` | 单一玩法的状态机与判定 | 直接建连、直接读 DB（经 `wowdata` 接口） |
| `agent` | 选目标、切行为、优先级与打断、失败退避 | 具体包格式、具体 SQL |
| `wowdata` | 查询与校验（quest/creature/GO/LFG 表） | 写业务数据（仅 provisioning/cleanup 例外） |

## 并发模型（必须遵守）

- **单写者**：只有一个 goroutine 发世界包。AzerothGhost 的 `WorldClient` 内部已有收发循环
  （`go w.Run()`），我们的组件只通过它的公开方法/`SendPacketRaw` 发送，不要自己开 socket。
- **命令串行化**：`wow.Session` 暴露一个命令通道（或 mutex 包裹的调用层），避免并发写同一 socket。
- **等待用等待器**：任何"发出去 → 期望有结果"的动作走 Arm → Send → Wait（详见 `50-testing-live-stack.md`）。
- **agent tick**：默认 200ms（与上游 bot 一致），tick 内只做"读状态快照 → 决策 → 提交动作"，
  不在 tick 里阻塞等待；需要等待时返回并登记 pending 状态。
- **状态快照**：跨 goroutine 读 `WorldClient` 状态时用其已导出的 getter，不要读未导出字段；
  自己维护的状态由 mutex 保护。
- **退出路径**：每个 goroutine 都要能从 `context.Context` 取消退出；退出顺序 = 停止 agent → 停止行为 →
  `WaitForLogout()` 或 `Close()`（优雅登出优先，避免服务端残留会话）。

## 会话状态机

复用上游 `client.SessionPhase`：`Connected → Authed → Loading → InWorld`，外加
`FarTransfer` / `NearTeleport` / `Logout` 两个传送态。规则：

- 只有 `InWorld` 才允许发玩法包（移动/施法/攻击）；在别的阶段发会被服务端丢弃或踢下线。
- 跨地图传送（`FarTransfer`）后**对象缓存会清空**，必须重新等待目标单位出现，不要复用旧 GUID 假设。
- agent 层的阶段另建一层语义（`Idle / Logging in / Preparing / Playing / Dungeon / Recovering / Stopped`），
  不要和协议阶段混用同一枚举。

## 配置

- 优先级：命令行 flag > 环境变量 > profile 文件 > 内置默认值。默认 profile 路径 `~/.config/autobot/profiles/<name>.yml`。
- profile 字段至少含：`auth_addr`、`realm_index`、`account.username/password` 或 `account.prefix`
  （批量生成时）、`character.name/race/class/gender`、`data_dir`、DB DSN 三个。
- 密码、DSN 口令**不入库**：profile 里只放占位符，实际值从环境变量读取；`.gitignore` 排除 `profiles/*.local.yml`。
- 所有可调参数都要能在 profile 里覆盖，方便同一份代码跑不同 bot。

## 日志与可观测

- `WorldClient` 自带分级日志：`client.SetLogLevel(client.LogInfo|LogDebug|LogTrace|...)`，也可用
  `E2E_WORLD_LOG=debug|trace|warn|error|silent` 控制。默认 Info。
- 本项目自身输出用 `log/slog`（JSON handler 可选），事件落 JSONL，字段建议
  `ts, bot, phase, feature, action, outcome, detail, proto`。
- 关键动作都要留"证据链"：做了什么 → 等到了什么包/状态 → 结论。排查时按
  AzerothGhost 的失败分类（**P** 协议/服务端前置 vs **R** 我们自己的可靠性）先分类再动手。
- 不要在热路径里打日志（每 tick 打印会淹没问题）：决策变化才打，周期性摘要按秒级节流。

## 错误与失败处理

| 类别 | 例子 | 处理 |
|------|------|------|
| 环境/前置 | 连不上 auth、DSN 错、`data_dir` 缺 mmaps | 启动即失败并给出明确提示，不重试掩盖 |
| 协议 | 阶段不对、包被丢弃、`SMSG_CAST_FAILED` 反复出现 | 记证据（opcode/阶段/时间），对照 AC 源码修正编码或前置条件 |
| 逻辑/可靠性 | 卡住、寻路失败、目标丢失、状态不同步 | 退回上一个安全状态重试；有上限的指数退避 |
| 服务端疑似 bug | 行为与 AC 源码/快照不符 | 先收集最小复现与证据，再问用户（不要顺手改服务端） |

所有重试必须有上限与退避；无限重试是 bug。任何"卡死"都要能通过日志在 1 分钟内定位到卡在哪一步。
