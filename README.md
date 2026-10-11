# azerothcore-autobot

Go 编写的**客户端侧（协议级）WoW 3.3.5a 自动玩家**。它不改服务端，而是像真实客户端一样登录、建号、说话、
打怪、接任务、排队副本，因此对任何 3.3.5a AzerothCore 服务器都成立。

协议实现基于 [AzerothGhost](https://github.com/azerothcore/AzerothGhost) **v1.0.9**（与 AC 官方 e2e 套件共用同一层）。
本仓库补齐它没有的部分：任务子系统、LFG 全流程、组队接受策略、目标调度器（agent）、账号 provisioning、结构化遥测。

> 设计规则（分层、依赖方向、协议真相来源、测试与实盘流程）以 `.clinerules/00`–`60` 为准；操作细节见 Cline 技能
> `.cline/skills/azerothcore-autobot/`（含 `references/` 下的协议表与上游能力地图）。两者是本机的开发资产、
> 按 `.gitignore` 不入库；本文件才是仓库里唯一面向读者的总览。

## 状态

**里程碑 1 进行中：协议层已落地，还没有可执行的 CLI。**

| # | 里程碑 | 状态 |
|---|--------|------|
| 1 | 自动登录 + 建号 | 协议层完成：`Dial`（SRP6 重试 + 世界握手）、`EnterWorld`（角色列表 → 按名匹配 → 必要时建号重试 → 登录）；**缺** CLI 与账号 provisioning；**未实盘验证** |
| 2 | 任务进行 | 仅发送侧：`CMSG_QUEST_*` 与 gossip 编码 + `Session` 封装；**缺** SMSG 解析、任务日志、状态机 |
| 3 | 组队接受 | 未开始（上游已有邀请/接受/转队长/解散接口，按规则复用而非重写） |
| 4 | 自动寻找地下城 | 仅发送侧：`CMSG_LFG_JOIN/LEAVE/SET_ROLES/PROPOSAL_RESULT/BOOT_VOTE/TELEPORT/GET_STATUS` + slot 打包；**缺** SMSG 解析与排队状态机 |
| 5 | 副本内推进 | 未开始 |

### 现在能跑什么

```bash
go build ./...
go vet ./... && gofmt -l .        # 两者都必须无输出
go test ./...                     # 离线单测（实盘用例按 build tag 跳过）
go test -race ./...
```

`cmd/autobot` 尚未实现，所以目前唯一的入口就是 `go test`。协议的正确性证据都在单测里：**编码用 golden bytes**
（期望字节按 AC 源码手写，绝不从自己的编码器反推），解码与状态机用表驱动。

当前覆盖率（`go test -cover ./internal/...`）：

| 包 | 覆盖率 | 说明 |
|----|--------|------|
| `internal/wow/packet` | 94.7% | 字节级编解码，接近穷尽 |
| `internal/wow` | 46.8% | 未覆盖部分是必须真连服务端的分支：`Dial`/`Authenticate` 与 quest/LFG 的发送 helper |

## 环境要求

实盘（连真服务端）前逐条确认。缺任何一项都会以误导性的形式失败 —— 例如"包发了没反应"或"登录失败"，
而根因在环境上。

1. **服务端在跑**：authserver `127.0.0.1:3724`、worldserver `127.0.0.1:8085`、MySQL `127.0.0.1:3306`
   （`acore:acore`，库 `acore_auth` / `acore_characters` / `acore_world`）。
   ```bash
   lsof -nP -iTCP -sTCP:LISTEN | grep -E '3724|8085|3306'
   ```
2. **开发期必须 `Warden.Enabled = 0`**。打开时每次登录都报
   `attempted to log in using invalid client OS ()`：AzerothGhost 发的 AUTH_LOGON_CHALLENGE 里 OS FourCC 是前导
   NUL 写法，AC 读到空串，于是只接受 `Win`/`OSX` 的校验必然失败。等上游修好再打开。
3. **寻路数据可用**：`data_dir` 下 `maps/`、`mmaps/`、`vmaps/` 存在且非空（mmaps 缺失 = 寻路直接失败）。
4. **环境变量沿用上游命名**（不自造）：`E2E_AUTH_ADDR`、`E2E_AUTH_DSN`、`E2E_CHAR_DSN`、`E2E_WORLD_DSN`；
   另加本项目读取的 `E2E_WORLD_LOG=info|debug|trace|warn|error|silent`（AzerothGhost 自己的日志级别开关；未设置
   时用 `info`，非法值被忽略、不会致命）。参考 `azerothcore-wotlk/e2e/.env.example`。
5. **只用 bot 账号/角色**：统一前缀，便于批量生成与清理。绝不复用真实玩家数据。

工具链：Go `1.26.4`（见 `go.mod`），本机开发环境 darwin/arm64。

## 代码结构

```
cmd/autobot/             (空)  CLI 入口：run / login / probe / quest / dungeon / party
internal/
  agent/                 (空)  目标调度器（tick 循环 + 黑板 + 行为注册表）
  config/                (空)  profile(YAML) + 环境变量 + flag，统一优先级
  telemetry/             (空)  结构化事件（JSONL）、运行摘要
  wowdata/               (空)  只读世界知识 + 校验（acore_world/characters/auth）
  wow/                   ✅  协议层：会话、等待器、opcode、服务端返回码
    packet/              ✅  字节级编解码（quest/gossip/lfg + Reader/Writer）
test/e2e/                (空)  实盘用例（`//go:build e2e`）
profiles/                (空)  profile 文件（`*.local.yml` 不入库）
```

依赖方向只能是 `cmd → agent → feature → wow → AzerothGhost`；`internal/wow` 之外的代码不直接碰 AzerothGhost。
`internal/wow` 不缓存自己的状态、也不含业务决策：决定**做什么**属于 `agent` / `feature`。

### `internal/wow` 已经能用的部分

| 能力 | 入口 |
|------|------|
| 认证 + 世界连接（SRP6 失败自动重试 3 次并退避） | `wow.Dial(ctx, ConnectOptions)` |
| 角色列表 → 建号 → 登录 → `PhaseInWorld` | `(*Session).EnterWorld(ctx, EnterWorldOptions)` |
| 角色命名（2–12 位纯字母、首字母大写、重名自动换后缀） | `wow.SanitizeName` / `wow.GenerateNames` |
| **Arm → Send → Wait** 命令模型（先挂监听再发包） | `Session.Do` / `Session.Arm` / `Session.AwaitReply` |
| 不需要回复的发包 | `Session.Send` |
| 阶段与状态快照（读 AzerothGhost 的 getter，不读未导出字段） | `WaitPhase` / `WaitInWorld` / `Position` / `Health` / `Object` / `InCombat` … |
| 任务与 gossip 发送 | `AcceptQuest` / `CompleteQuest` / `ChooseQuestReward` / `GreetQuestgiver` / `SelectGossipOption` / `QueryQuestDefinition` |
| LFG 发送 | `LfgJoin` / `LfgLeave` / `LfgSetRoles` / `LfgProposalResult` / `LfgSetBootVote` / `LfgTeleport` / `LfgGetStatus` / `SetLfgComment` |
| opcode 常量与可读名字 | `internal/wow/opcodes.go`、`OpcodeName(opcode)` |
| 服务端返回码（含 `CHAR_CREATE_*` 与 `CHAR_NAME_*` 并集） | `internal/wow/servercodes.go` |

并发约束：**一个 `Session` 一个 socket，且只有一个 goroutine 发包**。AzerothGhost 内部已有收发循环，
我们的代码只通过它的公开方法/`SendPacketRaw` 发送；命令由 `Session` 的命令锁串行化，避免并发写同一 socket。

### 库层用法

`internal/wow` 是 internal 包，只能被本 module 内的代码 import —— 这也是「先有 `cmd/autobot` 才能跑起来」的原因。
里程碑 1 跑通后 `cmd/autobot login` 的内部大致就是这段：

```go
ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
defer cancel()

sess, err := wow.Dial(ctx, wow.ConnectOptions{
	AuthAddr:   "127.0.0.1:3724",
	Username:   "BOTUSER",      // 世界连接要求全大写
	Password:   "botpass",
	RealmIndex: 0,
	Logf:       log.Printf,
})
if err != nil {
	return err // 认证/连接失败：启动期错误，直接给出阶段名，不重试掩盖
}
defer sess.Close() // 任何中途失败都必须关连接，否则旧 socket 用旧 session_key 会造成 AUTH_FAILED 连锁

// 角色不存在就按 Race/Class 建一个（重名会自动换名重试）。
char, err := sess.EnterWorld(ctx, wow.EnterWorldOptions{
	Name:  "ProbeBot",
	Race:  1, // AC ChrRaces: Human
	Class: 1, // AC ChrClasses: Warrior
	Logf:  log.Printf,
})
if err != nil {
	return err
}

// EnterWorld 返回时已在 PhaseInWorld；这一步是显式确认，也用于重连/传送后等待。
if err := sess.WaitInWorld(ctx, 30*time.Second); err != nil {
	return err
}
log.Printf("in world as %s (guid=%d)", char.Name, char.GUID)
```

登录序列（照抄上游 `e2eharness`，顺序不能变，缺步骤会表现为「包发了没反应」）：

```
Authenticate（失败重试 3 次）→ Connect(WorldClient) → go w.Run() → WaitForSessionPhase(PhaseAuthed)
  → SendReadyForAccountDataTimes → SendRealmSplit → RequestCharList
  → [CreateCharacter（必要时）] → LoginCharacter → WaitForLogin ⇒ PhaseInWorld
```

`EnterWorld` 已把上述序列封好，并处理两个易错点：**角色重名**（AC 的 `SMSG_CHAR_CREATE` 返回的是
`CHAR_CREATE_*` 与 `CHAR_NAME_*` 的并集，所以失败码既可能是 0x32 这类建号码，也可能是 0x5D 这类命名码，
两者都要按语义判定）与**同一账号多角色时按名字精确匹配**。

## 依赖

```go
module azerothcore-autobot

go 1.26.4

require (
	github.com/azerothcore/AzerothGhost v1.0.9   // 固定版本，不用 @latest
	github.com/go-sql-driver/mysql v1.10.0
	gopkg.in/yaml.v3 v3.0.1
)
```

`go.mod` 末尾那两行 `replace` **不能删**：AzerothGhost 依赖 `github.com/o0olele/detour-go`，并在自己的 go.mod 里把
它重定向到 `github.com/walkline/detour-go`，但 `replace` 不会跨模块传递 ——
缺了它 `go mod tidy` 会以 `invalid version: unknown revision 8056537019f8` 直接失败。

其它规则：新增第三方依赖前先问用户；上游接口不够用时先在本仓库写薄封装，不 fork AzerothGhost。

## 已经踩过的坑

写新代码前先看这几条，它们都属于「不报错但也不工作」，排查成本高：

1. **opcode 以 AC 源码为准，不要抄 AzerothGhost 的常量。** AzerothGhost v1.0.9 里
   `SmsgQuestupdateComplete = 0x01A8` 是错的，AC 的 `SMSG_QUESTUPDATE_COMPLETE` 是 **0x198**。opcode 错了不会
   报错，包只是永远匹配不上、任务进度像冻住一样。本项目 `opcodes.go` 从
   `azerothcore-wotlk/.../Opcodes.h` 抄写，并用单测钉住正确值。
2. **`Warden.Enabled = 0`**（开发期），否则登录必失败，见上文环境要求第 2 条。
3. **失败必须 `Close()`**：旧 socket 带着旧 `session_key` 继续认证会造成 `AUTH_FAILED` 连锁；重连先关旧连接再建新的。
4. **`SMSG_CHAR_CREATE` 的成功码只有 `CHAR_CREATE_SUCCESS`（0x2F）**，AC 没有 0x00 成功路径；
   同时它携带的是 `CHAR_CREATE_*` 与 `CHAR_NAME_*` 的并集，别只匹配建号那一组。
5. **GUID 与字符串的编码方式按包不同**：任务/gossip/组队类包里的 GUID 是**裸的小端 uint64**，
   不是移动包用的 packed 形式；AC 用 `>> std::string` 读出来的字符串是 **NUL 结尾**而非长度前缀。
6. **`CMSG_LFG_JOIN` 的 slot 列表为空会被服务端静默丢弃**（没有回复），表现为"排了队但永远没动静"：
   至少要选一个具体副本或随机副本，slot 用 `LfgSlot(dungeonID, dungeonType)` 打包。
7. **跨地图传送（`FarTransfer`）后对象缓存会清空**：不要复用旧 GUID 假设，必须重新等目标单位出现；
   只有 `PhaseInWorld` 才允许发玩法包。
8. **deserter debuff**（`LFG_SPELL_DUNGEON_DESERTER = 71041`）会挡住再次排队；实盘用例跑完要
   `CMSG_LFG_LEAVE`，别占住队列。

## 边界（明确不做）

- 不做服务端模块、不 fork / 不修改 azerothcore-wotlk（疑似服务端 bug 先收集证据再问用户）。
- 不支持 3.3.5a 之外的协议版本，不为非 AzerothCore 私服定制。
- 不做大规模压测舰队 —— 那是 AzerothGhost 的用途；本项目要的是"少数几个像真人的 bot"。
- 不写规避反作弊/反检测的代码；只在用户自有或本地服务器上运行，且只操作 bot 命名空间下的账号与角色。
- 不用 `time.Sleep` 代替等待条件；不用 GM 命令充当正常功能路径（仅限 setup/调试）。

## 下一步

1. `cmd/autobot`（登录命令）+ `internal/config`（profile YAML / 环境变量 / flag 优先级）+ `internal/wowdata`
   （复用上游 `e2eharness.ComputeSRP6` / `EnsureAccount` 做幂等 provisioning）→ 打通里程碑 1 的"一条命令"验收。
2. 起本地 AzerothCore 后实盘跑一次登录到 `PhaseInWorld`，并重复执行验证幂等。
3. 补任务与 LFG 的 SMSG 解析（`SMSG_QUESTGIVER_*`、`SMSG_QUESTUPDATE_*`、`SMSG_GOSSIP_*`、`SMSG_LFG_*`）
   与各自状态机；先在 `test/e2e/` 证明"发出去 → 收回来"，再写决策逻辑。
4. 补 `Dial`/`Authenticate` 的重试与失败路径单测（目前是未被覆盖的分支，需要真连接或引入接口 seam）。
