# 40 · 五大功能规格

每个功能 = 目标 → 状态机 → 依赖 → 验收（可观测）→ 风险。
线格式细节放 skill 的 `references/`，本文件只管"做什么、怎么算做完"。

## 1. 自动登录 + 建号（milestone 1，其它功能的前置）

- 目标：一条命令从零到 `PhaseInWorld`，可重复执行且幂等。
- 状态机：`EnsureAccount → Auth(重试3) → ConnectWorld → WaitAuthed → ReadyForAccountDataTimes →
  RealmSplit → CharEnum → [CreateCharacter 必要时] → LoginCharacter → WaitLogin → InWorld`。
- 依赖：`client.AuthClient`、`client.WorldClient`、`wowdata`（auth/characters 库）、`e2eharness.ComputeSRP6`/`EnsureAccount`（或等价自研）。
- 验收：`--char-name X` 反复运行都成功；重名时报出并自动换名重试；DB 里只多出 bot 前缀账号/角色；
  失败时进程退出码非 0 且日志给出具体阶段。
- 风险：auth 并发超时（要重试）、`session_key` 被旧连接覆盖（要先关旧连接）、建号返回码要按 AC 语义判定
  （成功 `0x2F`，重名 `0x32`，其余按 `CHAR_CREATE_*` 处理）。

## 2. 任务进行（milestone 2）

- 目标：给一个角色（或"任意可做任务"），能自动接取→完成→回交，链式推进若干任务。
- 状态机：`PickQuest（选任务）→ TravelToGiver → OpenGiver（gossip/questgiver）→ Accept →
  DoObjectives（kill/collect/explore 并行推进）→ TravelToEnder → Complete → ChooseReward → 下一个`。
  任一环节失败回到 `PickQuest` 并记录原因（最多 N 次，然后换任务）。
- 需要自建（上游没有）：`SMSG_QUESTGIVER_QUEST_LIST / QUEST_DETAILS / REQUEST_ITEMS / OFFER_REWARD /
  QUEST_COMPLETE` 的解析，`CMSG_QUEST_QUERY` 拉取任务定义，`SMSG_QUESTUPDATE_*` 维护任务日志，
  `SMSG_GOSSIP_MESSAGE` + `CMSG_GOSSIP_SELECT_OPTION` 走 gossip 型发布者/NPC 文本。
  精确 opcode 表与数据结构见 skill `references/quest-protocol.md`。
- 世界知识来自 `acore_world`：`quest_template`（等级/前置/目标）、`creature_queststarter/ender`（谁发/谁收）、
  `creature`（坐标）、`gameobject`（探索/收集目标）。
- 验收：单条任务链从头到尾成功，任务状态在 `acore_characters.character_queststatus` 里与预期一致（用 DB 校验）；
  目标怪被他人抢走/自己死亡/超时后仍能恢复推进。
- 风险：目标怪在视野外（要寻路 + 轮询等待）、共享击杀不计数（确认是否组队）、依赖前置任务（按 DB 依赖排序）。

## 3. 组队接受（milestone 3）

- 目标：被邀请时按策略自动入队，并作为队员跟随/协助。
- 实现：`AddGroupInviteHook`（或 `OnGroupInvite`）→ 策略判定 → `GroupAccept()` / 忽略；
  入队状态用 `GroupState()`/`AddGroupListHook` 维护，已有队伍时按策略拒绝或先离开。
- 策略（可配置）：允许的前缀/姓名白名单、最大队伍人数、是否允许跨阵营、是否只接受队友（LFG 组）邀请。
- 队员行为：跟随队长（保持距离，用导航）、协助队长目标、按 `SetLootMethod` 的当前设置处理拾取。
- 验收：被白名单邀请时进队并出现在 `GroupState()` 里；被非白名单邀请时拒绝且不卡状态；
  队长变更/被踢/队伍解散时回到安全状态。
- 风险：刚入队时 `GroupState` 未到（要等 `SMSG_GROUP_LIST`）、重复邀请、邀请时自己在传送中。

## 4. 自动寻找地下城副本（LFG，milestone 4）

- 目标：能作为 5 人之一（通常由多个 bot 组成预组队）排随机/指定副本，进本推进后出本。
- 用户可见流程：`准备队伍(1坦1治3DD 或随机) → 队长 CMSG_LFG_JOIN → role check →
  SMSG_LFG_PROPOSAL_UPDATE → CMSG_LFG_PROPOSAL_RESULT(accept) → 传送进本 → 清怪推进 →
  SMSG_LFG_OFFER_CONTINUE / 完成 → 出本（CMSG_LFG_LEAVE 视需要）`。
- 需要自建（上游没有）：LFG 全流程客户端。已知事实（来自 AC 源码实测）：
  `CMSG_LFG_JOIN=0x35C`、`CMSG_LFG_LEAVE=0x35D`、`CMSG_LFG_SET_ROLES=0x36A`、
  `CMSG_LFG_PROPOSAL_RESULT=0x362`、`CMSG_LFG_TELEPORT=0x370`、`CMSG_LFG_GET_STATUS=0x296`；
  角色位 `LEADER=0x01, TANK=0x02, HEALER=0x04, DAMAGE=0x08`；需求为 1 坦 / 1 治 / 3 DD；
  副本槽位 = `dungeonID | (type<<24)`（服务端只取低 24 位），`LFG_TYPE_RANDOM=6`。
  `CMSG_LFG_JOIN` 的精确字节布局、服务端发送函数与数据表见 skill `references/lfg-protocol.md`。
- 回退路径（LFG 不可用或人数不足时）：手动 `GroupInvite/GroupAccept` 组队 → 走到副本入口
  （坐标来自 `lfg_dungeon_template`）或集合石 → 进本。回退必须在日志里明确标注，不要静默降级。
- 验收：队列状态机从 joined → rolecheck → queued → proposal → dungeon 逐级有可观测证据；
  5 人（多 bot）能真正进本；超时/被拒/有人拒绝 proposal 时能退出队列并回到安全状态。
- 风险：预组队全员角色位要匹配需求；服务端 `DungeonFinder.OptionsMask` 关掉对应类型会静默忽略；
  进本后坐标/地图切换要等 `NewWorld` + 对象缓存重建；deserter debuff（`LFG_SPELL_DUNGEON_DESERTER=71041`）会挡再次排队。

## 5. 副本内推进（milestone 5，可与 4 一起做）

- 目标：进本后按简单策略清怪前进、必要时拾取、卡住能自救，最终到达终点或完成条件。
- 实现：导航到目标点（mmaps）→ 选目标 → 战斗循环（职业策略，可参考上游 `scripts/ai/class/*.lua` 的思路，
  以 Go 实现）→ 回血/复活（必要时 `.revive` 仅限调试）→ 无目标时沿路线推进。
- 验收：至少一条副本路线能从入口走到第一个 boss 并击杀；卡住/死亡后可恢复。
- 风险：拉怪过多、地形寻路失败、副本内需要机制（门/传送/载具）——按需扩展。

## 后续（不在当前范围）

训练技能/买装备、修理与补给、拍卖行与银行、飞行点、聊天应答、与真人玩家协同。新增前先问用户。
