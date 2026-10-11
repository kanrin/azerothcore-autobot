# azerothcore-autobot

Go 编写的**客户端侧（协议级）WoW 3.3.5a 自动玩家**。它不改服务端，而是像真实客户端一样登录、建号、说话、
打怪、接任务、排队副本，因此对任何 3.3.5a AzerothCore 服务器都成立。

协议实现基于 [AzerothGhost](https://github.com/azerothcore/AzerothGhost) **v1.0.9**（与 AC 官方 e2e 套件共用同一层）。
本仓库补齐它没有的部分：任务子系统、LFG 全流程、组队接受策略、目标调度器（agent）、账号 provisioning、结构化遥测。

> 设计规则（分层、依赖方向、协议真相来源、测试与实盘流程）以 `.clinerules/00`–`60` 为准；操作细节见 Cline 技能
> `.cline/skills/azerothcore-autobot/`（含 `references/` 下的协议表与上游能力地图）。两者是本机的开发资产、
> 按 `.gitignore` 不入库；本文件才是仓库里唯一面向读者的总览。

## 状态

**里程碑 1 的代码已完成；实盘验证卡在服务端的一个开关上（见「环境要求」第 2 条）。**

范围已按用户要求扩大（多 bot 编排、常驻 loop、任务/打怪/采集/组队副本/成就/AH/战场、阵营平衡）。
当前落地顺序与范围收敛见本文末尾的「路线图」；首批只做 **2–3 个 bot 的 loop / 任务 / 采集**，
AH 经济、战场、成就按配置默认关闭，设计保留。

| # | 里程碑 | 状态 |
|---|--------|------|
| 1 | 自动登录 + 建号 | ✅ 代码齐了：`internal/config`（profile + 环境变量 + flag 优先级）、`internal/wowdata`（幂等 provisioning + 清理）、`cmd/autobot`（`probe` / `login` / `state`）。实盘已跑到 auth 成功、拿到正确世界地址；**未完成**的是世界握手（被服务端 `AUTH_REJECT` 拒绝，原因已定位为 `Warden.Enabled`） |
| 1.5 | 自身状态模型 | ✅ `internal/wow/state`：从对象缓存读金钱/背包/装备/银行/技能，字段偏移逐个钉到 `UpdateFields.h` 与 `Player.h` 的行号；`autobot state` 是它的实盘证据（待世界握手打通后跑） |
| 1.6 | 目标调度器（agent） | ✅ `internal/agent`：200ms tick、黑板、行为注册表、优先级 + 抢占余量、失败指数退避、`Done`/`Stopper` 语义 |
| 1.7 | 走路式寻路 | ✅ `internal/feature/travel`：navmesh 规划 + 上游 `movement.MovementController` 每 tick 推进，含到达/换图/重规划/卡住判定（跨大陆飞行点未做） |
| 2 | 任务进行 | 仅发送侧：`CMSG_QUEST_*` 与 gossip 编码 + `Session` 封装；**缺** SMSG 解析、任务日志、状态机 |
| 3 | 组队接受 | 未开始（上游已有邀请/接受/转队长/解散接口，按规则复用而非重写） |
| 4 | 自动寻找地下城 | 仅发送侧：`CMSG_LFG_JOIN/LEAVE/SET_ROLES/PROPOSAL_RESULT/BOOT_VOTE/TELEPORT/GET_STATUS` + slot 打包；**缺** SMSG 解析与排队状态机。2–3 个 bot 凑不齐 5 人随机本，进本走"手工组队 + 走到副本门口"的回退路径并在日志标注 |
| 5 | 副本内推进 | 未开始 |
| 6 | 采集 / 卖垃圾 / 训练师 | 未开始（协议位置已定位：`CMSG_GAMEOBJ_USE`、剥皮法术、`CMSG_TRAINER_*`、`CMSG_LIST_INVENTORY`/`SELL_ITEM`） |
| 7 | AH 经济 / 战场 / 成就 | 延后（服务端侧证据：`acore_characters.auctionhouse` 0 行、无 AH-bot 模块、`StartPlayerMoney=0`；WSG 需 5v5 即 10 个 bot；`achievement_dbc` 仅 3 行、依赖 `data_dir/dbc`） |

### 现在能跑什么

```bash
go build ./... && go vet ./... && gofmt -l .      # 三者都必须无输出
go test ./...                                     # 离线单测（实盘用例按 build tag 跳过）

# 连真服务端（先 set -a; source .env.local; set +a）
go run ./cmd/autobot version
go run ./cmd/autobot probe --account autobotprobe                        # 认证 + 世界连接，报告停在哪一步
go run ./cmd/autobot login --account autobotprobe --char-name Autobotprobe
go run ./cmd/autobot state --account autobotprobe --char-name Autobotprobe   # 打印金钱/背包/技能（--json 可选）
go test -tags=e2e ./test/e2e/... -count=1 -v -timeout 10m                 # 实盘：登录冒烟 + 清理
```

协议正确性的证据在单测里：**编码用 golden bytes**（期望字节按 AC 源码手写，绝不从自己的编码器反推），
解码与状态机用表驱动。行为正确性的证据在 `test/e2e/`（`TestSmokeLogin`、`TestCleanupRemovesBotData`）。

覆盖率（`go test -cover ./internal/...`）：

| 包 | 覆盖率 | 说明 |
|----|--------|------|
| `internal/config` | 99.0% | 优先级、严格 YAML、秘密解析、校验 |
| `internal/wow/packet` | 94.7% | 字节级编解码，接近穷尽 |
| `internal/agent` | 90.5% | tick 选择、抢占余量、退避、`Done`/`Stopper`、黑板（`-race`） |
| `internal/wow/state` | 86.8% | 更新字段读背包/技能/金钱；偏移按 AC 源码钉死 |
| `internal/feature/travel` | 84.1% | 寻路状态机（到达/换图/重规划/卡住），planner 与 mover 都是接口 |
| `internal/wow` | 44.3% | 未覆盖的基本都是必须真连服务端的分支（`Dial`/`Authenticate`/`EnterWorld`/quest/LFG 发送 helper），由 `-tags=e2e` 用例覆盖 |
| `internal/wowdata` | 23.7% | 离线只够覆盖前缀守卫与结果投影；SQL 路径靠实盘用例 |

## 环境要求

实盘（连真服务端）前逐条确认。缺任何一项都会以误导性的形式失败 —— 例如"包发了没反应"或"登录失败"，
而根因在环境上。

1. **服务端在跑**。本机实测的拓扑（见下）是 bot 在 Mac `192.168.50.90`、服务端是另一台机器
   `192.168.50.11` 上的 Docker：auth `3724`、world `8085`、MySQL `3306`（`7878` SOAP 未监听）。
   ```bash
   for p in 3724 8085 3306; do nc -vz -w 3 192.168.50.11 $p; done
   ```
   数据库账号实测**只有 `root` / `password` 能从远端用**，`acore:acore` 返回 `ERROR 1045`。
2. **开发期必须 `Warden.Enabled = 0`**。AC 发行默认是 `1`（`worldserver.conf.dist:1167`），docker 资产不覆盖它，
   所以新起的服务端默认开着。打开时世界握手会被硬拒：`SendAuthResponseError(AUTH_REJECT)`，
   客户端表现为 `SMSG_AUTH_RESPONSE` 码 14 + `Auth response failed with code 14`，
   同时 `acore_auth.account.os` 是空串（AzerothGhost 的 OS FourCC 是前导 NUL 写法，AC 读到空串，
   而校验只接受 `Win`/`OSX`）。修法是改 0 并重启 worldserver；**不要**去 UPDATE `account.os` 规避检查。
   ```bash
   docker exec ac-worldserver sh -c "sed -i 's/^Warden.Enabled = 1/Warden.Enabled = 0/' /azerothcore/env/dist/etc/worldserver.conf"
   docker restart ac-worldserver
   ```
3. **`realmlist.localAddress` 要指向服务端真实 IP（跨主机时必须）**。authserver 发的是按客户端来源 IP 现算的
   世界地址，不是 `address` 列原样返回：本机实测原值 `localAddress = 127.0.0.1` 会让 authserver 把
   `127.0.0.1:8085` 发给客户端，世界连接必然打到自己。改成服务端 IP 后 ≤20s 生效，对同网段的真实客户端也正确。
   `probe` 会回显实际拿到的地址（`world_addr=`），一眼可辨。
4. **寻路数据可用**（里程碑 2 起；登录/建号不需要）：`data_dir` 下 `maps/`、`mmaps/`、`vmaps/` 存在且非空。
5. **凭据与配置**：凭据只进 `.env.local`（gitignore），模板见 `.env.example`；profile 模板见
   `profiles/example.yml`（profile 里只写环境变量名，不写密码）。CLI 与 `-tags=e2e` 测试共用同一份环境变量：
   `AUTOBOT_*` 为本项目命名，`E2E_*`（上游同名）作为回退，另有 `E2E_WORLD_LOG=info|debug|trace|warn|error|silent`
   控制 AzerothGhost 自己的日志级别。
6. **只用 bot 账号/角色**：统一前缀（profile 的 `account.prefix`），provisioning 与清理都只认带前缀的名字；
   绝不复用真实玩家数据。

工具链：Go `1.26.4`（见 `go.mod`），本机开发环境 darwin/arm64。

## 代码结构

```
cmd/autobot/             ✅  CLI：version / probe / login / state（run / fleet / cleanup 待补）
internal/
  agent/                 ✅  目标调度器：200ms tick + 黑板 + 行为注册表 + 抢占余量 + 失败退避
  feature/
    travel/              ✅  走路式寻路（navmesh 规划 + 运动引擎每 tick 推进）；quest/grind/gather/... 待补
  config/                ✅  profile(YAML) + 环境变量 + flag 优先级；密码与 DSN 只从环境变量取
  telemetry/             (空)  结构化事件（JSONL）、运行摘要
  wowdata/               ✅  三个库的读写层：幂等 provisioning + 清理 + 只读校验（带 bot 前缀守卫）
  wow/                   ✅  协议层：会话、等待器、opcode、服务端返回码、删角色 / 登出
    packet/              ✅  字节级编解码（quest/gossip/lfg + Reader/Writer）
    state/               ✅  自身状态模型：金钱 / 背包 / 装备 / 银行 / 技能（读对象缓存的更新字段）
test/e2e/                ✅  实盘用例（`//go:build e2e`）：TestSmokeLogin、TestCleanupRemovesBotData
profiles/                ✅  profile 模板（`*.local.yml` 不入库）
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
| 删角色 / 登出（`SMSG_CHAR_DELETE` 上游有常量但没解析，靠 `Do` 挂监听） | `(*Session).DeleteCharacter` / `(*Session).Logout` |
| authserver 实际给出的世界地址（排查 realmlist 用） | `(*Session).RealmAddress` |

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

`go.mod` 末尾的 `replace` 指向 `github.com/walkline/detour-go`：AzerothGhost 在自己的 go.mod 里把
`github.com/o0olele/detour-go` 重定向过去，而 `replace` 不跨模块传递，所以本仓库要重复一次。
**配对的 `require` 交给 `go mod tidy` 决定**：Go 1.17+ 按实际 import 剪枝模块图，只 import `client` +
`e2e/e2eharness` 时 tidy 会删掉那条 require 并正常返回；引入 navmesh/movement 这类包时它会被自动加回来
（手工塞回去只会被再删，造成 diff 抖动）。

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
9. **世界地址是 authserver 按客户端来源 IP 现算的**，不是 realmlist 表里的 `address` 原样返回。跨主机时
   `localAddress = 127.0.0.1` 会让客户端拿到 `127.0.0.1:8085`，"连接被拒"看起来像防火墙问题。见「环境要求」第 3 条；
   `probe` 会打印实际拿到的 `world_addr=`。
10. **`SMSG_CHAR_DELETE` 上游有常量却没有解析**，删角色必须自己挂监听（本项目走 `Session.Do`）。而且 GUID
    不在服务端角色缓存里时 AC **静默返回、一个包都不发**，所以只会表现为超时，没有失败码可看。
11. **AC 的 docker 镜像默认开着 Warden**（`worldserver.conf.dist` 里是 1，`entrypoint.sh` 只是把 `.dist` 复制成
    `.conf`）。所以"服务端刚起好、代码也没问题"时登录仍会被拒，先查这一条。

## 边界（明确不做）

- 不做服务端模块、不 fork / 不修改 azerothcore-wotlk（疑似服务端 bug 先收集证据再问用户）。
- 不支持 3.3.5a 之外的协议版本，不为非 AzerothCore 私服定制。
- 不做大规模压测舰队 —— 那是 AzerothGhost 的用途；本项目要的是"少数几个像真人的 bot"。
- 不写规避反作弊/反检测的代码；只在用户自有或本地服务器上运行，且只操作 bot 命名空间下的账号与角色。
- 不用 `time.Sleep` 代替等待条件；不用 GM 命令充当正常功能路径（仅限 setup/调试）。

## 路线图

范围收敛：先做 **2–3 个 bot 的 loop / 任务 / 采集**；AH 经济、战场、成就延后（配置默认关闭，设计保留）。

| 阶段 | 内容 | 验收 |
|------|------|------|
| **M0 前置（需要你操作）** | ① 服务端 `Warden.Enabled = 0` 并重启 worldserver；② 把客户端数据弄到本机：`dbc/`（小）+ `maps/`、`mmaps/`、`vmaps/`（约 3–6 GB），例如 `docker cp ac-worldserver:/azerothcore/env/dist/data/. ./ac-data/` | `probe` 打印 `phase=authed`；`login` 打印 `login: OK` + `login: database agrees`；`state` 打印的金钱/背包/技能与 `acore_characters`（`characters.money`、`item_instance`、`character_skills`）一致 |
| **M1 loop 骨架** | 剩余：`cmd/autobot run`（配置 → 登录 → agent 循环）、feature 与 config 的接线 | 单 bot 常驻：日志出现 `agent: running` 与行为切换；Ctrl-C 后 `agent: stopped` 且已登出 |
| **M2 多 bot** | `internal/fleet`（roster 载入、登录节流、阵营平衡、批量 provisioning）、`fleet.yml` + `roster/*.yml`、`autobot fleet/provision/retire/gc/status` | 3 个 bot 同时常驻；重跑幂等（账号/角色数不变）；`status --faction` 偏差在容差内；`gc` 后 DB 归零 |
| **M3 战斗 + 任务** | 职业战斗循环（目标选择/回血/复位）、任务子系统（gossip + `SMSG_QUESTGIVER_*`/`SMSG_QUESTUPDATE_*` 解析 + 任务日志状态机）、精选任务路径（如人类 1–20） | kill/collect/explore 三型任务各走完一条，`character_queststatus` 与预期一致 |
| **M4 采集 + 训练师 + 贩卖** | `CMSG_GAMEOBJ_USE`（矿/草）、剥皮法术（`SpellEffects.cpp:5138` 路径）、`CMSG_TRAINER_LIST`/`BUY_SPELL`、`CMSG_LIST_INVENTORY`/`SELL_ITEM`/`REPAIR_ITEM` | 学一门采集专业 → 采 5 个节点 → 回城卖垃圾：`Money()` 增加、`item_instance` 减行 |
| **M5 LFG + 副本（受限）** | LFG 协议到 `proposal` 一级；进本走回退路径（手工组队 + 走到副本门口）；副本内推进 | 队长 `CMSG_LFG_JOIN` 后有 `JOIN_RESULT`/`QUEUE_STATUS`/`PROPOSAL_UPDATE` 证据包；回退路径：3 bot 进 Deadmines 并清掉前两组怪（日志标注走了回退） |
| **backlog** | AH 出售/购买 + 邮件（2.7/2.8/2.9）、战场 + 简单 PvP（2.10，需 10 bot）、成就（2.4，依赖 `data_dir/dbc`）、飞行点（跨大陆）、`internal/telemetry` | 各自在满足前置后按同一 DoD 验收 |

### 需要你决定/操作的三件事

1. **服务端 `Warden.Enabled = 0` 并重启**（命令见「环境要求」第 2 条）。在此之前所有实盘功能都只能停在 auth 成功。
2. **客户端数据落到本机**（`data_dir`）。本机实测 `env/dist` 下只有 `etc/`、`logs/`，全盘没有 `mmaps` 目录；
   没有它，寻路/打怪/采集/副本全部无法验证。
3. 可选：授权我用 SSH（服务端 22 端口开着）做**只读**检查（`data_dir/dbc` 是否完整、conf、日志）——
   一条 `ls /azerothcore/env/dist/data/dbc | wc -l` 就能判定成就/AH/邮件是否可用。
