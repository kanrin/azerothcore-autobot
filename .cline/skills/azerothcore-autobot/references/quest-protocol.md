# 任务 / Gossip 协议笔记

来源：`/Users/orz/Projects/other/azerothcore-wotlk` 的 `Server/Protocol/Opcodes.cpp`、
`Server/Packets/QuestPackets.{h,cpp}`、`Server/Packets/MiscPackets.*`（gossip 部分）、
`Handlers/QuestHandler.cpp`、`Handlers/GossipHandler.cpp`。
AzerothGhost 只提供了下面标 ✅ 的发送函数，解析全部要自己写。

## Opcode 表（数值即 3.3.5a 客户端编号）

| opcode | 值 | 方向 | 上游状态 |
|--------|----|------|---------|
| `CMSG_QUEST_QUERY` | 0x05C | C→S | 需自建（拉取任务定义） |
| `SMSG_QUEST_QUERY_RESPONSE` | 0x05D | S→C | 需自建（任务目标/奖励全貌） |
| `CMSG_GOSSIP_HELLO` | 0x17B | C→S | 需自建 |
| `CMSG_GOSSIP_SELECT_OPTION` | 0x17C | C→S | 需自建 |
| `SMSG_GOSSIP_MESSAGE` | 0x17D | S→C | 需自建（菜单项/NPC 文本/任务列表） |
| `SMSG_QUESTGIVER_STATUS` | 0x183 | S→C | 需自建（单个 NPC 的任务标记） |
| `CMSG_QUESTGIVER_HELLO` | 0x184 | C→S | ✅ `QuestgiverHello(npcGUID)` |
| `SMSG_QUESTGIVER_QUEST_LIST` | 0x185 | S→C | 需自建 |
| `CMSG_QUESTGIVER_QUERY_QUEST` | 0x186 | C→S | 需自建 |
| `SMSG_QUESTGIVER_QUEST_DETAILS` | 0x188 | S→C | 需自建（接取前） |
| `CMSG_QUESTGIVER_ACCEPT_QUEST` | 0x189 | C→S | ✅ `QuestgiverAcceptQuest(npcGUID, questID)` |
| `CMSG_QUESTGIVER_COMPLETE_QUEST` | 0x18A | C→S | ✅ `QuestgiverCompleteQuest(npcGUID, questID)` |
| `SMSG_QUESTGIVER_REQUEST_ITEMS` | 0x18B | S→C | 需自建（要求上交物品/可完成） |
| `SMSG_QUESTGIVER_OFFER_REWARD` | 0x18D | S→C | 需自建（可领奖） |
| `CMSG_QUESTGIVER_CHOOSE_REWARD` | 0x18E | C→S | ✅ `QuestgiverChooseReward(npcGUID, questID, reward)` |
| `SMSG_QUESTGIVER_QUEST_COMPLETE` | 0x191 | S→C | 需自建（领奖确认） |
| `SMSG_QUESTUPDATE_COMPLETE` | 0x198 | S→C | 需自建（目标完成） |
| `SMSG_QUESTUPDATE_ADD_KILL` | 0x199 | S→C | 需自建（击杀计数） |
| `SMSG_QUESTUPDATE_ADD_ITEM` | 0x19A | S→C | 需自建（物品计数） |
| `CMSG_QUESTGIVER_STATUS_MULTIPLE_QUERY` | 0x417 | C→S | 需自建（批量查附近 NPC 任务标记） |
| `SMSG_QUESTGIVER_STATUS_MULTIPLE` | 0x418 | S→C | 需自建 |

注意：`SMSG_QUESTGIVER_QUEST_LIST` 只给"这个 NPC 能给/能交哪些任务"，任务的目标文本、物品需求
要另外用 `CMSG_QUEST_QUERY`（任务日志里的任务）或 `CMSG_QUESTGIVER_QUERY_QUEST`（NPC 处查询）拿
`SMSG_QUEST_QUERY_RESPONSE` / `QUEST_DETAILS`。别指望一个包搞定。

## 世界知识（`acore_world`，只读）

| 表 | 用途 |
|----|------|
| `quest_template` | 等级、前置、目标类型/数量、奖励、是否 AutoAccept |
| `creature_queststarter` / `creature_questender` | 谁发布 / 谁收取（配合 `creature` 取坐标） |
| `gameobject_queststarter` / `gameobject_questender` | 物体形式的发布/收取 |
| `creature`、`gameobject` | 坐标与地图，用于寻路目标点 |
| `quest_template_addon` | `RequiredSkill`、`PrevQuestID` 等附加前置 |

AC 里还有 `AutoAccept`、共享任务、`RequiredClasses/Races` 等条件；判定前先查表，不要硬编码。

## 建议的任务状态机

```
SELECT       从 DB 选一个"当前可做"的任务（满足前置/等级/职业种族限制）
TRAVEL_GIVER 导航到发布者（creature_queststarter → creature 坐标）
INTERACT     近身 → QuestgiverHello；若是 gossip 型 NPC 则 CMSG_GOSSIP_HELLO →
             SMSG_GOSSIP_MESSAGE 里选 quest 选项 → CMSG_GOSSIP_SELECT_OPTION
ACCEPT       等 SMSG_QUESTGIVER_QUEST_DETAILS / REQUEST_ITEMS → QuestgiverAcceptQuest
OBJECTIVES   杀怪：选怪→战斗→靠 SMSG_QUESTUPDATE_ADD_KILL 计数；
             收集：拾取/购买/交互；到达：寻路到坐标并等 SMSG_QUESTUPDATE_COMPLETE
TURN_IN      导航到收取者 → QuestgiverCompleteQuest → SMSG_QUESTGIVER_OFFER_REWARD →
             QuestgiverChooseReward（注意奖励下标）
VERIFY       用 acore_characters.character_queststatus / e2eharness.QuestStatus 校验
```

每个状态都要有超时与重试上限；失败按 AzerothGhost 的分类（P 协议 / R 可靠性）记录原因，然后换任务或退回安全点。

## 已知坑

- 击杀计数只在"你的角色参与击杀"时增加；队伍共享规则不同，先用 `SMSG_QUESTUPDATE_ADD_KILL` 观测再下结论。
- 有前置任务链时，`PrevQuestID` 不满足会导致 NPC 根本不列出该任务。
- 有些任务由物体（`gameobject`）发布，交互包不是 `CMSG_QUESTGIVER_HELLO`，要走 gossip/GO use 路径。
- 奖励物品可能因背包满而进邮箱（服务端 `LFG.MailItemOnFullInventory`）；判奖励要看邮件兜底。
- 领取奖励后任务从日志移除，别在移除后继续发 `Complete/ChooseReward`。
- 用 DB 校验状态时要在 `Save`（登出/存档）之后读，否则可能拿到旧值（上游 `QuestStatusAfterSave` 就是为这个存在的）。

## 最小验证

1. 单测：把 `CMSG_QUESTGIVER_ACCEPT_QUEST`、`CMSG_GOSSIP_SELECT_OPTION` 编码写成 golden bytes。
2. 实盘：接一个新手区任务 → 观察 `SMSG_QUESTGIVER_QUEST_LIST` → `ADD_KILL` 计数增长 → `QUESTUPDATE_COMPLETE`
   → 回交 → `acore_characters.character_queststatus` 状态变化。
