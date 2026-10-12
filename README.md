# azerothcore-autobot

Go 编写的**客户端侧（协议级）WoW 3.3.5a 自动玩家**。它不改服务端，而是像真实客户端一样登录、建号、说话、
打怪、接任务、排队副本，因此对任何 3.3.5a AzerothCore 服务器都成立。

协议实现基于 [AzerothGhost](https://github.com/azerothcore/AzerothGhost) **v1.0.9**（与 AC 官方 e2e 套件共用同一层）。
本仓库补齐它没有的部分：任务子系统、LFG 全流程、组队接受策略、目标调度器（agent）、账号 provisioning、结构化遥测。

> 设计规则（分层、依赖方向、协议真相来源、测试与实盘流程）以 `.clinerules/00`–`60` 为准；操作细节见 Cline 技能
> `.cline/skills/azerothcore-autobot/`（含 `references/` 下的协议表与上游能力地图）。两者是本机的开发资产、
> 按 `.gitignore` 不入库；本文件才是仓库里唯一面向读者的总览。

## 状态

**里程碑 1（登录 + 建号）与 1.5（自身状态模型）已在真实服务端通过验收。**
环境侧的前置已全部满足：服务端 `Warden.Enabled = 0`、`realmlist.localAddress = 192.168.50.11`、
客户端数据（`dbc` + `maps` + `mmaps` + `vmaps`，3.1 GB）已在本机 `ac-data/`（gitignore）。

范围已按用户要求扩大（多 bot 编排、常驻 loop、任务/打怪/采集/组队副本/成就/AH/战场、阵营平衡）。
当前落地顺序与范围收敛见本文末尾的「路线图」；首批只做 **2–3 个 bot 的 loop / 任务 / 采集**，
AH 经济、战场、成就按配置默认关闭，设计保留。

| # | 里程碑 | 状态 |
|---|--------|------|
| 1 | 自动登录 + 建号 | ✅ **实盘通过**：`probe` → `phase=authed`；`login` → `in world`（`health=55/55`）+ `database agrees` + `login: OK`；`TestSmokeLogin` 两次登录复用同一 guid；`TestCleanupRemovesBotData` 把角色与账号都清干净（DB 回到 0 行） |
| 1.5 | 自身状态模型 | ✅ **实盘通过**：`autobot state` 与 DB 逐字段一致；`TestStateMatchesDatabase` 自动比对金钱、每个物品槽（entry/堆叠/耐久）与每个技能（人类战士：22 条技能、含 `backpack[0] ↔ bag0[23]` 的背包映射） |
| 1.6 | 目标调度器（agent） | ✅ `internal/agent`：200ms tick、黑板、行为注册表、优先级 + 抢占余量、失败指数退避、`Done`/`Stopper` 语义（单测 `-race` 90.5%） |
| 1.7 | 走路式寻路 | ✅ `internal/feature/travel`：navmesh 规划 + 上游 `movement.MovementController` 每 tick 推进，含到达/换图/重规划/卡住判定（单测 84.1%）。**实盘走动被一个 mmap 版本差异挡住**，见下方坑 16 |
| 1.8 | 常驻 loop（`autobot run`） | ✅ **实盘通过**：`internal/bot` 运行时（登录 → agent 每 200ms tick → 优雅登出）+ `autobot run`；实跑 119 tick 后 SIGINT → `agent: stopped` → `reason=signal` → `Logout complete`（约 19.7s 后 `SMSG_LOGOUT_COMPLETE`，正是 AC 的 20s 倒计时）→ DB `online=0`，无 ghost session；`TestRunResidentLoop` 自动复现同一结论 |
| 2 | 多 bot 编排（fleet） | ✅ **实盘通过**：`internal/fleet`（fleet.yml + roster/*.yml、登录节流、阵营平衡、provision/status/gc、常驻监督器）+ `autobot fleet ...`；实测 **3 个 bot 同时在线 59s、`clean=3`、0 错误**，随后 3 个角色全部 `online=0` 并优雅登出；`provision` 重跑幂等（账号不重复创建）、`gc` 幂等（第二次全部 `absent`）；阵营 2 联盟/1 部落、偏差 0.33 在容差 0.34 内；`TestFleetLifecycle` 自动复现整条链 |
| 2 | 任务进行 | 仅发送侧：`CMSG_QUEST_*` 与 gossip 编码 + `Session` 封装；**缺** SMSG 解析、任务日志、状态机 |
| 3 | 组队接受 | 未开始（上游已有邀请/接受/转队长/解散接口，按规则复用而非重写） |
| 4 | 自动寻找地下城 | 仅发送侧：`CMSG_LFG_JOIN/LEAVE/SET_ROLES/PROPOSAL_RESULT/BOOT_VOTE/TELEPORT/GET_STATUS` + slot 打包；**缺** SMSG 解析与排队状态机。2–3 个 bot 凑不齐 5 人随机本，进本走"手工组队 + 走到副本门口"的回退路径并在日志标注 |
| 5 | 副本内推进 | 未开始 |
| 6 | 采集 / 卖垃圾 / 训练师 | 未开始（协议位置已定位：`CMSG_GAMEOBJ_USE`、剥皮法术、`CMSG_TRAINER_*`、`CMSG_LIST_INVENTORY`/`SELL_ITEM`） |
| 7 | AH 经济 / 战场 / 成就 | 延后，但**数据侧已确认可行**：服务端 `data/dbc` 有 247 个文件，含 `Achievement.dbc`、`AuctionHouse.dbc`、`MailTemplate.dbc`（此前担心的 dbc 缺失不成立）。仍缺的是 10 个 bot 才能开打的战场人数与 AH-bot 经济来源（`StartPlayerMoney=0`） |

### 现在能跑什么

```bash
# 构建：Makefile 是入口（括号里是等价的裸命令）
make build             # 编译到 bin/autobot，版本戳取自 git describe（= go build -o bin/autobot ./cmd/autobot）
make                   # 同上：build 是默认目标
make check             # 门禁三连：gofmt + go vet + go test ./...（提交前跑这个）
make e2e               # 实盘用例（自动 source .env.local；需要服务端在跑）
make release-linux     # 静态 linux/amd64 单文件，scp 到服务端即可跑
make help              # 全部目标

# 下面用 bin/autobot（make build 的产物）；等价写法是 go run ./cmd/autobot ...
# 注意：go run / go build 不注入版本戳，`autobot version` 会打印默认值 0.1.0；
#       make build 会打印 git describe 的结果（当前是 commit 短 sha）。
bin/autobot version
bin/autobot probe --account autobotprobe                        # 认证 + 世界连接，报告停在哪一步
bin/autobot login --account autobotprobe --char-name Autobotprobe --race 1 --class 1
bin/autobot state --account autobotprobe --char-name Autobotprobe --race 1 --class 1   # --json 可选

# 常驻 loop（M1 收尾）：Ctrl-C 优雅退出，日志 agent: running / agent: stopped / bot: logged out
bin/autobot run --account autobotprobe --char-name Autobotprobe --race 1 --class 1
#   --features ""        不启用任何行为（只在线，用来验证 loop 与保活）
#   --for 60s            跑满时长后自行优雅退出
#   --goto 0:-8902.59,-162.61,82.02 --until-arrival   走到该点后退出（需要 mmap，见坑 16）

# 多 bot 编排（M2）：roster 在 fleet.yml + roster/*.yml，模板见 fleet.example.yml
cp fleet.example.yml fleet.yml && cp roster/example.yml roster/bots.yml
bin/autobot fleet status   --fleet fleet.example.yml            # 账户/角色/阵营/在线 + --json
bin/autobot fleet provision --fleet fleet.example.yml --characters   # 账号 + 协议建号（幂等）
bin/autobot fleet run      --fleet fleet.example.yml            # 全 roster 常驻，Ctrl-C 全部优雅登出
bin/autobot fleet gc       --fleet fleet.example.yml            # 删角色（协议）+ 删账号（幂等）

# 账号 provisioning（只写 bot 命名空间；前缀是硬守卫）
bin/autobot account create --account autobotprobe --prefix autobot   # 密码取自 AUTOBOT_PASSWORD
bin/autobot account list --prefix autobot                          # 需要时 --json
bin/autobot account delete --account autobotprobe --prefix autobot  # 仍拥有角色时会拒绝

# 别的命名空间：用 --prefix 显式声明，例如测试账号 zhl001 / 密码 zhl001
bin/autobot account create --account zhl001 --password zhl001 --prefix zhl
bin/autobot account list --prefix zhl

# 实盘套件：顺序有意义，zz_cleanup_test.go 必须最后跑（文件名决定顺序），跑完 DB 归零
make e2e                    # 自动 source .env.local，等价于下面这行
go test -tags=e2e ./test/e2e/... -count=1 -v -timeout 60m -parallel 1 -p 1
```

### 账号 provisioning 与 bot 命名空间

`autobot account create|list|delete` 是唯一会写 `acore_auth` 的命令，三条性质是刻意的：

- **前缀就是边界**：账号与角色名都必须以 `account.prefix`（默认 `autobot`）开头，否则**拒绝**而不是警告；
  `list` 也按前缀过滤，所以这条命令永远看不到真实玩家的账号。前缀可用 `--prefix` 覆盖，
  但"用别的前缀"不是"绕过检查"——它是在声明那批名字也归本项目所有。
  正因如此，前缀必须是 ≥2 位的字母数字（`%` 或 `z` 这类会被拒绝：一个字符的前缀等于把半个服务器算成自己的）。
- **幂等**：已存在则刷新密码（`password refreshed`），不会插出第二行；`delete` 删两次也不报错。
- **只碰自己创建的**：`delete` 在账号仍拥有角色时拒绝执行（先删角色，而删角色走协议、不直接改库）。

密码来源优先级：`--password` > `account.password_env` 指定的变量 > `AUTOBOT_PASSWORD`。
写在命令行会留在 shell history 里，长期使用建议放环境变量或 profile。

协议正确性的证据在单测里：**编码用 golden bytes**（期望字节按 AC 源码手写，绝不从自己的编码器反推），
解码与状态机用表驱动。行为正确性的证据在 `test/e2e/`（`TestSmokeLogin`、`TestCleanupRemovesBotData`）。

覆盖率（`go test -cover ./internal/...`）：

| 包 | 覆盖率 | 说明 |
|----|--------|------|
| `internal/config` | 99.1% | 优先级、严格 YAML、秘密解析（密码与 DSN 可分别解析）、校验 |
| `internal/wow/packet` | 95.5% | 字节级编解码，接近穷尽 |
| `internal/agent` | 90.5% | tick 选择、抢占余量、退避、`Done`/`Stopper`、黑板（`-race`） |
| `internal/wow/state` | 86.8% | 更新字段读背包/技能/金钱；偏移按 AC 源码钉死 |
| `internal/feature/travel` | 84.1% | 寻路状态机（到达/换图/重规划/卡住），planner 与 mover 都是接口 |
| `internal/wow` | 42.3% | 未覆盖的基本都是必须真连服务端的分支（`Dial`/`Authenticate`/`EnterWorld`/`Logout` 的收发/quest/LFG 发送 helper），由 `-tags=e2e` 用例覆盖 |
| `internal/wowdata` | 22.1% | 离线只覆盖前缀守卫、LIKE 转义与结果投影；SQL 路径（provisioning/列表/校验）靠实盘用例 |
| `internal/bot` | 51.0% | 离线覆盖行为适配（计划/到达/换图/放弃/抢占）、点解析、spec 校验；登录与循环靠实盘 |
| `internal/fleet` | 39.1% | 离线覆盖 roster 加载/校验/默认值、阵营与偏差、环境覆盖；provision/status/gc/监督靠实盘 |
| `test/e2e` | — | 5 条实盘用例：`TestAccountLifecycle`、`TestFleetLifecycle`、`TestRunResidentLoop`、`TestSmokeLogin`、`TestStateMatchesDatabase`、`TestCleanupRemovesBotData` |

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
4. **寻路数据在服务端的 docker 卷里，本机需要一份副本**（里程碑 1 不需要，走路/打怪/采集需要）：
   卷是 `azerothcore_ac-client-data`，容器内挂到 `/azerothcore/env/dist/data`（只读）。本机已拉好一份：
   ```bash
   ssh orz@192.168.50.11 'docker cp ac-worldserver:/azerothcore/env/dist/data /home/orz/ac-data-export'
   rsync -a orz@192.168.50.11:/home/orz/ac-data-export/ ./ac-data/   # 3.1 GB / 22280 文件
   ```
   `ac-data/` 已在 `.gitignore` 里；`AUTOBOT_DATA_DIR` 指向它即可。
5. **凭据与配置**：凭据只进 `.env.local`（gitignore），模板见 `.env.example`；profile 模板见
   `profiles/example.yml`（profile 里只写环境变量名，不写密码）。CLI 与 `-tags=e2e` 测试共用同一份环境变量：
   `AUTOBOT_*` 为本项目命名，`E2E_*`（上游同名）作为回退，另有 `E2E_WORLD_LOG=info|debug|trace|warn|error|silent`
   控制 AzerothGhost 自己的日志级别。
6. **只用 bot 账号/角色**：统一前缀（profile 的 `account.prefix`），provisioning 与清理都只认带前缀的名字；
   绝不复用真实玩家数据。

工具链：Go `1.26.4`（见 `go.mod`），本机开发环境 darwin/arm64。

## 代码结构

```
cmd/autobot/             ✅  CLI：version / probe / login / state / account / run / fleet
internal/
  agent/                 ✅  目标调度器：200ms tick + 黑板 + 行为注册表 + 抢占余量 + 失败退避 + panic 隔离
  bot/                   ✅  单 bot 运行时：登录 → agent 循环 → 保活（ping 30s/心跳 5s）→ 优雅登出；行为适配层
  fleet/                 ✅  多 bot 编排：roster(YAML) + 登录节流 + 阵营平衡 + provision/status/gc + 常驻监督（失败重连）
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
    `.conf`）。所以"服务端刚起好、代码也没问题"时登录仍会被拒，先查这一条。**本机已于 2026-10-12 关掉并重启验证**。
12. **非 instant logout 有 20 秒倒计时**：AC 只在休息区/坐骑上/有权限时立即登出，否则
    `WorldSession.h:547` 是 `curr_time >= _logout_time + 20`。所以等 `SMSG_LOGOUT_COMPLETE` 的超时必须 ≥30 秒
    （上游自己的 harness 也用 30 秒）；15 秒会得到"看起来像服务端不响应"的超时。而且被拒时
    （战斗中/下落中/决斗）服务端**只发** `SMSG_LOGOUT_RESPONSE` 且 reason≠0、**不发** COMPLETE ——
    只等 COMPLETE 会把"被拒"误报成超时，所以两个包都要 Arm 并检查 reason。
13. **`SMSG_LOGIN_VERIFY_WORLD` 不携带角色自身更新字段**：紧跟其后还有一条自身的
    `SMSG_UPDATE_OBJECT`。在这一刻读 `Health()` 会得到 `0`（实盘见过 `health=0 max_health=0`），
    `state` 快照也会是空的。`EnterWorld` 现在会等自身数据进缓存（`MaxHealth() > 0`）再返回。
14. **race/class 组合必须合法**：人类猎人（1/3）在 3.3.5a 根本不存在（Cata 才加），服务端会给
    `CHAR_CREATE_ERROR` 并在日志写 `invalid race/class pair (1/3)`；范围检查拦不住它。
    `internal/config` 现在按服务端 `acore_world.playercreateinfo` 的矩阵做启动期守卫，
    矩阵来源与重新生成方式写在 `config.go` 的注释里。
15. **实盘用例的执行顺序是有意义的**：清理用例删的是别人建的角色与账号，所以它的文件名必须是
    `zz_cleanup_test.go`（`go test` 按文件名排序执行），这样 `go test -tags=e2e ./test/e2e/...`
    跑完整套后服务端 DB 回到 0 行。
16. **mmap 版本：AC master 是 20，AzerothGhost v1.0.9 只认 19** —— 本机 `ac-data/mmaps/*.mmtile`
    是 v20，所以任何 `FindPath` 都会失败：`version mismatch in '...mmtile': got 20, expected 19`
    （`pathfinding/mmap_loader.go:17` 硬编码 `mmapVersion = 19`）。证据表明这**只是版本号**：AC 的
    bump 来自 PR #25720（修 Blade's Edge Arena 寻路的**生成器**改动），`MapDefines.h` 只改了常量一行，
    `MmapTileRecastConfig` 仍是 `static_assert(sizeof == 36)`，瓦片头 56 字节布局未变；而 v1.0.9 已是
    上游最新发布版本（`go list -m -versions` 到 v1.0.9 为止）。三条出路（需用户决定）：
    就地改写 mmtile 头版本号（可逆，4 字节/文件）／向上游提 PR 让期望版本可配置／用 #25720 之前的
    生成器重建 v19 mmaps。**在解决之前 `run --goto` 与 M3/M5 的移动都不可用**，但登录/常驻/采集以外的
    只读功能不受影响。
17. **AC 会因"过快的 ping"踢人**：`WorldSocket.cpp:753-767` 把间隔 **< 27 秒**的 ping 记为
    `_OverSpeedPings`，超过 `CONFIG_MAX_OVERSPEED_PINGS`（默认 2）就 `kicked for over-speed pings`。
    所以保活 ping 必须 ≥27s（本项目用 30s），且登录握手后不能立刻再 ping（第一次 ping 用来起算）。
18. **`WorldClient.Run()` 的返回值必须被接住**：上游 `go w.Run()` 丢弃了它，本项目在 `wow.Session`
    里补了 `Done()`/`RunErr()`；否则一个长跑的 bot 无法发现连接已断，会对着死 socket 一直 tick。
19. **一个行为 panic 不能带走整个进程**：`agent.Tick` 现在把行为里的 panic 转成错误并让该行为退避
    （同时打完整栈），因为 fleet 模式下多个 bot 共用一个进程。实盘第一次跑 `run` 时正是这里索引 `-1`
    崩掉了进程（见 `agent.go` 的 `callRun` 与 `TestTick_NoCandidateStopsTheRunningBehavior`）。
20. **`SMSG_CHAR_DELETE` 的回答早于行的提交**：收到成功码后立刻 `DELETE FROM account` 会被
    `wowdata` 以 `ErrAccountNotEmpty` 拒绝（它查得到刚"删掉"的角色）。实盘 `fleet gc` 的**第一个** bot
    就是这么失败的，后面两个因为多了 2s 间隔反而成功——所以这是间歇性 bug 的典型形态。
    正确做法是等真实信号：`fleet.GC` 现在轮询 `characters` 行直到消失（上限 timeout）再删账号。
21. **角色名不能含数字**：AC 的角色名只允许字母（`ObjectMgr::CheckPlayerName`），所以"角色名带前缀"
    这条约定在**前缀含数字**时无法字面成立（`autoe2e` + `alpha` → 数字被 `SanitizeName` 丢掉）。
    fleet 的规则是：账号名带**完整前缀**，角色名带**前缀的字母部分**（`namePrefix()`，两种检查分开做）。
22. **fleet 的策略也要认 `E2E_*` 回退**：`fleet.yml` 里的 `auth_addr` 只是占位，`AUTOBOT_AUTH_ADDR`
    优先、`E2E_AUTH_ADDR` 兜底（与 profile 的优先级一致）。实盘第一次 `fleet provision --characters`
    因为只认 `AUTOBOT_AUTH_ADDR` 而连 `127.0.0.1:3724` 失败——账号写成功了、建号全失败，正好把
    "每一步都要有真实前置"这件事演示了一遍。

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
| **M0 前置** | ✅ 已完成：服务端 `Warden.Enabled = 0` + 重启、`realmlist.localAddress = 192.168.50.11`、客户端数据 3.1 GB 拉到本机 `ac-data/`、SSH 只读核查（`data/dbc` 247 个文件齐全） | `probe` → `phase=authed`；`login` → `login: OK` + `login: database agrees`；`state` 与 `acore_characters`（`money`、`item_instance`、`character_skills`）一致 —— **全部达成，见上方状态表** |
| **M1 loop 骨架** | ✅ 已完成：`internal/bot`（可复用的单 bot 运行时）+ `autobot run`（常驻、保活、SIGINT/`--for`/`--until-arrival` 优雅退出）、travel 行为适配、`TestRunResidentLoop`。**遗留**：`--goto` 的实盘走动被 mmap 版本差异挡住（坑 16），解决后即可闭环 | 单 bot 常驻：日志出现 `agent: running`；Ctrl-C 后 `agent: stopped` + `bot: logged out` + DB `online=0` —— **已达成** |
| **M2 多 bot** | ✅ 已完成：`internal/fleet`（`fleet.yml` + `roster/*.yml`、登录节流 `stagger`/`max_concurrent`、阵营平衡与容差、`Provision`/`Status`/`GC`、常驻监督器含失败重连）+ `autobot fleet status/provision/run/gc` + `fleet.example.yml`/`roster/example.yml` | 3 个 bot 同时常驻；重跑幂等（账号/角色数不变）；`status` 阵营偏差在容差内；`gc` 后 DB 归零 —— **全部达成**（见上方状态表第 2 行） |
| **M3 战斗 + 任务** | 职业战斗循环（目标选择/回血/复位）、任务子系统（gossip + `SMSG_QUESTGIVER_*`/`SMSG_QUESTUPDATE_*` 解析 + 任务日志状态机）、精选任务路径（如人类 1–20） | kill/collect/explore 三型任务各走完一条，`character_queststatus` 与预期一致 |
| **M4 采集 + 训练师 + 贩卖** | `CMSG_GAMEOBJ_USE`（矿/草）、剥皮法术（`SpellEffects.cpp:5138` 路径）、`CMSG_TRAINER_LIST`/`BUY_SPELL`、`CMSG_LIST_INVENTORY`/`SELL_ITEM`/`REPAIR_ITEM` | 学一门采集专业 → 采 5 个节点 → 回城卖垃圾：`Money()` 增加、`item_instance` 减行 |
| **M5 LFG + 副本（受限）** | LFG 协议到 `proposal` 一级；进本走回退路径（手工组队 + 走到副本门口）；副本内推进 | 队长 `CMSG_LFG_JOIN` 后有 `JOIN_RESULT`/`QUEUE_STATUS`/`PROPOSAL_UPDATE` 证据包；回退路径：3 bot 进 Deadmines 并清掉前两组怪（日志标注走了回退） |
| **backlog** | AH 出售/购买 + 邮件（2.7/2.8/2.9）、战场 + 简单 PvP（2.10，需 10 bot）、成就（2.4，依赖 `data_dir/dbc`）、飞行点（跨大陆）、`internal/telemetry` | 各自在满足前置后按同一 DoD 验收 |

### 环境侧已就绪（无需再操作）

1. ✅ 服务端 `Warden.Enabled = 0` 并已重启（`docker exec ac-worldserver grep -n Warden.Enabled ...` → `0`）。
2. ✅ 客户端数据在本机 `ac-data/`（`dbc` 87 M、`maps` 295 M、`mmaps` 2.1 G、`vmaps` 657 M）。
3. ✅ SSH 只读核查已完成；服务端导出目录（`/home/orz/ac-data-export`）已删除，未留残余。
4. ✅ bot 账号与角色：实盘套件跑完自行清理，DB 现在是 0 账号 / 0 角色。

下一步（不需要你做任何事）：把 `autobot run` 接起来，在 Elwynn 做第一次真实行走 + 采集。
