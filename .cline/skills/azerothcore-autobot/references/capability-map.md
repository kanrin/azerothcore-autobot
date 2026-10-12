# 上游能力地图（AzerothGhost v1.0.9）

用途：动手前先确认"这块上游有没有"，避免重写；下方路径相对
`github.com/azerothcore/AzerothGhost`（本机模块缓存：`~/go/pkg/mod/github.com/azerothcore/!azeroth!ghost@v1.0.9`）。

## 已有（直接用）

| 能力 | 位置 / 接口 |
|------|-------------|
| SRP6 认证 + realm 列表 | `client/auth.go`：`NewAuthClient(user,pass)`、`Authenticate(addr) ([]RealmInfo, error)`、`SessionKey()` |
| 世界会话 + 阶段机 | `client/world.go`：`NewWorldClient(user, sessionKey, logf)`、`Connect`、`Run`、`WaitForSessionPhase`、`WaitForLogin`、`WaitForLogout`、`Close` |
| 角色列表 / 建角 / 删角 / 登录角色 | `RequestCharList`、`CreateCharacter(name,race,class,gender,skin,face,hair,hairColor,facialHair,outfitID)`、`DeleteCharacter`、`LoginCharacter`；回调 `OnCharList`、`OnCharCreateResult` |
| 会话前置包 | `SendReadyForAccountDataTimes`、`SendRealmSplit`（进角色列表前必须发） |
| 移动编码 / 心跳 / 朝向 / 跳 | `MoveForward*`、`MoveStop*`、`SetFacing*`、`SendHeartbeat*`、`SendMovementHeartbeat*`、`UpdatePosition`、`Position` |
| 对象缓存与查询 | `GetObject`、`GetNearbyUnits`、`FindUnitByEntry`、`FindGameObjectByEntry`、`GetNearbyPlayers`、`NameQuery`、`CreatureQuery` |
| 战斗 | `AttackSwing`、`AttackStop`、`SetTarget`、`InCombat`、`TargetGUID`、`Health`、`Power` |
| 法术 / 光环 | `CastSpell`、`CastSpellAtPosition`、`CancelCast`、`CancelAura`、`SelfAuras`、`SelfHasAura`、`KnowsSpell`、`ChannelSpell` |
| 战利品 / 掷骰 | `Loot`、`LootItem`、`LootMoney`、`LootRelease`；掷骰事件与 hook |
| 组队 | `GroupInvite`、`GroupAccept`、`GroupDecline`、`GroupLeave`、`GroupDisband`、`GroupSetLeader`、`GroupUninvite`、`SetLootMethod`、`GroupState`、`GroupMembers`、`InGroup`、`IsGroupLeader` |
| 交易 / 公会银行 / 请愿书 / 载具 | `client/trade.go`、`guildbank.go`、`petition.go`、`vehicle.go` |
| 竞技场 / 战场排队 | `client/battleground.go` + `JoinRatedArena` 等（注意：**不含** LFG 地下城查找器） |
| 召唤 / 副本重置 | `client/summon.go`；`instance reset` opcode 已处理 |
| 原始发包 | `SendPacketRaw(opcode uint16, data []byte) error` —— 新协议包（LFG/任务）靠它 |
| 包 hook | `AddPacketHook(fn func(opcode uint16, data []byte)) (cancel func())`；另有 trade/loot/group/spellcast/battlefield 专用 hook |
| 日志分级 | `SetLogLevel(client.Log*)`、`ParseLogLevel`、`E2E_WORLD_LOG` |
| 寻路 / 导航 / 移动控制 | `pathfinding/`（mmaps/vmaps 加载与寻路）、`navigation/`（`Navigator`）、`movement/`（`NewMovementController`、`NewWorldMovementSender`） |
| 游戏数据 | `gamedata/` |
| 可选复用 | `ai/behaviortree`、`ai/luaengine`（Lua 写 AI；上游 `scripts/ai/*.lua` 含各职业循环思路）、`bot/`（tick 循环 + Lua 驱动的完整 bot，含 grind/dungeon 模式） |
| 实盘测试设施 | `e2e/e2eharness/`（无 build tag，可直接 import）：`NewSolo`、`NewScenario`、`PackagePad`、`WaitInWorld`、`Relog`、组队/交易/战利品等待器、`EnsureAccount`、`ComputeSRP6`、`QuestStatus`、`CleanupPlayer`、`AddPacketHook` 等 |

## 缺失（本项目要写）

| 缺口 | 现状 | 需要做什么 |
|------|------|-----------|
| 任务系统 | `world.go` 只定义了少量 quest opcode 常量 + 4 个发送函数（`QuestgiverHello/AcceptQuest/CompleteQuest/ChooseReward`）；**没有**任何 `SMSG_QUEST*` / `SMSG_QUESTGIVER_*` 解析，没有任务日志 | 解析任务包、维护任务日志、驱动任务流程 → `references/quest-protocol.md` |
| Gossip / NPC 文本 | 完全没有 | 解析 `SMSG_GOSSIP_MESSAGE`，发送 `CMSG_GOSSIP_HELLO` / `CMSG_GOSSIP_SELECT_OPTION` |
| LFG（地下城查找器） | 完全没有（只有竞技场/BG 排队） | 全流程客户端实现 → `references/lfg-protocol.md` |
| 组队接受策略 / 队员行为 | 只有 `GroupAccept` 与 `AddGroupInviteHook` | 策略层（白名单、人数、阵营）+ 跟随/协助行为 |
| 目标调度器 | 上游是 `bot` + Lua 脚本 | Go 原生 agent：tick、黑板、行为优先级、失败退避 |
| 账号 provisioning CLI | 只有测试用 `EnsureAccount` | 幂等的账号/角色准备与清理命令 |
| 结构化遥测 | 上游 `ValidationMode` 面向压测 | 本项目自己的 JSONL 事件与决策追踪 |

## 明确不在上游、也不建议复用

- 上游 `bot` 的 Lua AI 可以作为**原型**（快速试职业循环），但正式功能走 Go；
  引入 Lua 作为核心依赖前先问用户。
- `scripts/dungeon.lua` 只是占位实现（light pull + CC 提示），不要当成现成的副本推进逻辑。
