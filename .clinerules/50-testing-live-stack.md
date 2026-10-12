# 50 · 测试与实盘验证

## 分层

| 层 | 位置 | 需要服务端？ | 说明 |
|----|------|--------------|------|
| 单元 | 与代码同目录 | 否 | 包编解码（golden bytes）、状态机迁移、选择逻辑 |
| 契约 | `internal/*/` + `testdata/` | 否 | 用 AC 源码推出的期望字节/字段，回归保护 |
| 实盘 e2e | `test/e2e/`（`//go:build e2e`） | **是** | 真连 auth+world，复用 `e2eharness` |

离线 `go test ./...` 必须全部跳过实盘用例；跑实盘要显式 `-tags=e2e`。

## 环境准备（实盘前逐条确认）

1. 服务端在跑：authserver `127.0.0.1:3724`、worldserver `127.0.0.1:8085`、MySQL `127.0.0.1:3306`
   （`acore:acore`，库 `acore_auth` / `acore_characters` / `acore_world`）。
2. `Warden.Enabled = 0`（原因见 `30-wow-protocol.md`）；`DungeonFinder.OptionsMask` 含你要排的副本类型。
3. 寻路数据可用：`data_dir/maps`、`data_dir/mmaps`、`data_dir/vmaps` 存在且非空（mmaps 缺失 = 寻路直接失败）。
4. 环境变量（沿用上游命名，避免自造）：
   `E2E_AUTH_ADDR`、`E2E_AUTH_DSN`、`E2E_CHAR_DSN`、`E2E_WORLD_DSN`，
   可选 `E2E_WORLD_LOG=info|debug|trace|warn|error|silent`。
   参考文件：`azerothcore-wotlk/e2e/.env.example`。
5. 账号只用作 bot：不要复用真实玩家账号；实盘账号统一前缀，便于清理。

## 复用上游测试设施（不要重造）

`github.com/azerothcore/AzerothGhost/e2e/e2eharness`（无 build tag，可被本模块直接 import）：

- 会话/夹具：`NewSolo`、`NewScenario`、`ScenarioOpts{Prefix,Race,Class,Level,LearnAllClass,StartPad,SkipGM}`、`ByRole`。
- 登录/落点：`PackagePad(t)`（按目录粘滞的隔离点）、`Teleport`、`TeleNamed`、`WaitInWorld`、`Relog`、`HardDisconnect`。
- 等待器：`WaitGroupList`、`WaitIsGroupLeader`、`WaitUnitLootable`、`WaitTradeOpen`、`WaitBattlefieldStatus`、`WaitDead/WaitAlive`。
- 断言/严重级别：`Assertf`、`Preconditionf`、`HarnessFailf`、`ConfirmedBugf(t, issue, …)`。
- 账号与 DB：`OpenAuthDB/OpenCharDB/OpenWorldDB`、`EnsureAccount`、`ComputeSRP6`、`SetGM`、
  `QuestStatus`/`WaitQuestStatus`、`CleanupPlayer`。
- 包观察：`AddPacketHook`（多订阅者、可取消）——**抓我们自己新包的证据就用它**。

## 等待器优先（硬性）

模式固定为 **Arm → Send → Wait**，等真实信号（包 / 对象缓存 / 阶段 / 组状态 / HP）。
禁止在动作后用 `time.Sleep` / `WaitMS` "等一会儿"；等待器内部 20–50ms 轮询是允许的。

| 动作后 | 等什么 |
|--------|--------|
| 登录 / `Relog` / 跨图传送 | `WaitForSessionPhase(PhaseInWorld)` 或 `WaitInWorld` |
| 建角 | `OnCharCreateResult`（结果码） |
| 邀请/接受/转队长 | `WaitGroupList` / `WaitIsGroupLeader` / `WaitNotInGroup` |
| 到点后要交互的 NPC | `WaitUnit` / `WaitUnitGUID`（对象缓存） |
| 击杀 → 拾取 | `WaitUnitDead` → `WaitUnitLootable` → `OpenLoot` |
| 施法/光环/死亡 | `WaitSpell*` / `WaitAura` / `WaitDead` / `WaitAlive` |
| LFG 排队/进本（本项目新码） | `SMSG_LFG_JOIN_RESULT` / `SMSG_LFG_QUEUE_STATUS` / `SMSG_LFG_PROPOSAL_UPDATE` / `SMSG_NEW_WORLD` |

## 清理（无例外）

- 生成的持久刷怪/物体用带清理的助手（`Spawn`、`SpawnKillLootable`、`SpawnGameObject`），
  或记录 DB spawn id 后用 `DespawnCreatureSpawn`/`DespawnGameObjectSpawn` 清理。**禁止裸用 `.npc add` / `.gobject add`。**
- bot 账号与角色在 `t.Cleanup` 里删（`CleanupPlayer` + 删账号）；保留前缀便于扫描残留。
- 队伍要解散/离开（`DisbandParty`/`LeaveGroup`），LFG 要 `CMSG_LFG_LEAVE`，避免占住队列与 deserter。
- 每个实盘用例跑完要能回答："我改了世界的什么，是否已还原？"

## 隔离

- 实盘用例间不要共用同一格世界坐标：用 `PackagePad(t)`（27 个隔离点），需要固定位置的用例用完回到 pad。
- 推荐命令（包并行、包内串行）：
  `go test -tags=e2e ./test/e2e/... -count=1 -v -timeout 60m -parallel 1 -p 1`
- 单测：`go test -tags=e2e ./test/e2e/lfg -run TestLfg_JoinRandomQueue -count=1 -v -timeout 10m`。

## 失败分类（先分类再修）

沿用上游 DEBUG_PLAYBOOK 的两分法：

- **P**（协议/服务端前置）：阶段不对、字段错、缺 ACK、前置缺失、被静默忽略 → 对照 AC 源码修正编码或前置。
- **R**（我们自己的可靠性）：缓存过期当成本地真相、AI 抖动、阻塞 sleep、状态不同步 → 修状态机/等待器。

证据手段：

- `E2E_WORLD_LOG=debug|trace` 打开更细的会话/施法日志；`AddPacketHook` 记录我们关心的 opcode。
- 服务端侧临时提高 `Logger.network=4,Console Server`、`Logger.network.opcode=4,Console Server`，按时间 + 角色名对齐。
- 校验最终状态优先查 DB（任务状态、坐标、物品数）而不是只看客户端缓存。

## 红线

- 不要在服务端没起时把实盘用例当单元测试跑（会得到误导性红）。
- 不要用 `SoftPass` 掩盖无法判定的 setup —— 用 `Preconditionf`。
- 不要为了绿测试去改 azerothcore-wotlk 或放宽断言；疑似服务端 bug 先给证据再问用户。
- 不要断言时序（"应该 2 秒内"），断言状态与包；时间只用于超时上限。
