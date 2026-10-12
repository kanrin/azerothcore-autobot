# 00 · 项目定位与边界

`azerothcore-autobot` 是一个 Go 编写的 **客户端侧（协议级）WoW 3.3.5a 自动玩家**。
它不改服务端，像真实客户端一样登录、建号、说话、打怪、接任务、排队副本，因此对任何 3.3.5a
AzerothCore 服务器都成立。默认目标服务端：`/Users/orz/Projects/other/azerothcore-wotlk`。

## 能力范围（五大里程碑）

| # | 能力 | 验收标准（一句话） |
|---|------|-------------------|
| 1 | 自动登录 | 账号缺失时自动 provisioning，角色缺失时自动创建，最终稳定停在 `PhaseInWorld` |
| 2 | 创建角色 | 用 race/class/gender 组合确定性建角，重名自动换名，按 AC 返回码判定成败 |
| 3 | 任务进行 | 自己去任务发布者处接任务 → 完成目标（杀/收集/到达）→ 回交领奖，状态机可恢复 |
| 4 | 组队接受 | 收到 `SMSG_GROUP_INVITE` 后按策略自动接受/拒绝，入队后跟随队伍 |
| 5 | 自动寻找地下城 | 通过 LFG 排队 → role check → proposal → 传送进本 → 推进 → 出本 |

## 两个外部仓库，各自角色

- `/Users/orz/Projects/other/azerothcore-wotlk` —— **规范与运行环境**。
  包格式、错误码、机制行为的最终依据是它的 C++ 源码（参考文件清单见 `30-wow-protocol.md`）。
  本工程把它当作**只读**资源：不要为让测试通过而改服务端；确有必要时先问用户。
- `github.com/azerothcore/AzerothGhost` —— **协议与寻路基础库**（Go，客户端侧）。
  AC 官方 e2e 套件也用同一个库，所以 bot 与自动化测试共享一层协议实现。
  固定版本 **v1.0.9**；依赖与 `replace` 规则见 `20-go-standards.md`。

## 上游已有 vs 本项目自建

上游 AzerothGhost 已提供：SRP6 认证、世界会话阶段机、对象缓存、移动编码与心跳、mmaps/vmaps 寻路与
导航控制器、战斗/光环/召唤/宠物/载具、组队（邀请/接受/离开/队长/拾取方式）、战利品与掷骰、交易、
公会银行、请愿书、竞技场与 BG 排队、原始包发送（`SendPacketRaw`）与包 hook（`AddPacketHook`）。

上游**没有**、需要本项目实现：任务子系统（任务包解析 + 任务日志 + gossip/NPC 文本）、LFG 全流程、
组队接受策略、目标调度器（agent）、账号 provisioning CLI、结构化遥测。
完整清单（含函数名与文件路径）见 skill 的 `references/capability-map.md`。

## 非目标

- 不做服务端模块（服务端 bot 是另一路线，如 `mod-playerbots`），不 fork 服务端。
- 不实现 3.3.5a 之外的协议版本，不为非 AzerothCore 私服定制。
- 不做大规模压测舰队 —— 那是 AzerothGhost 的用途；本项目要的是"少数几个像真人的 bot"。
- 不写规避反作弊/反检测的代码；只在用户自有或本地服务器上运行。

## 铁律（违反即返工）

1. 只操作 bot 命名空间下的账号与角色（前缀见 profile），**绝不碰真实玩家数据**。
2. 任何数据库写入都必须幂等且带清理路径，并在运行/测试结束时执行。
3. 行为优先用游戏内包实现；数据库只用于"世界知识"（查询）与结果校验；GM 命令仅限 setup/调试。
4. 不用 `time.Sleep` 代替等待条件（见 `50-testing-live-stack.md`）。
5. 新增第三方依赖、改动 azerothcore-wotlk、疑似服务端 bug —— 都先给证据再问用户（见 `60-workflow.md`）。

## 假设与待确认（按此执行，若不符请纠正）

- 运行环境：可达的 authserver `127.0.0.1:3724`、worldserver `127.0.0.1:8085`、
  MySQL `127.0.0.1:3306`（`acore:acore`），且寻路数据目录含 `maps/`、`mmaps/`、`vmaps/`。
  截至撰写时本机这三个端口都无监听，所以实盘验证前必须先起服务端（见 skill `references/session-and-env.md`）。
- 开发期设置 `Warden.Enabled = 0`（原因见 `30-wow-protocol.md`）。
- LFG 走服务端默认 `DungeonFinder.OptionsMask = 5` 的随机副本路径；5 人队伍由多个 bot（或 bot + 真人）凑齐。
- 组队接受默认策略：仅接受白名单前缀/同账号邀请，其余拒绝；策略可配置。
