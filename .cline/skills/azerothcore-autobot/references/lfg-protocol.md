# LFG（地下城查找器）协议笔记

来源：`/Users/orz/Projects/other/azerothcore-wotlk` 的 `src/server/game/Server/Protocol/Opcodes.cpp`、`src/server/game/Server/Packets/LFGPackets.{h,cpp}`、`src/server/game/Handlers/LFGHandler.cpp`、`src/server/game/DungeonFinding/LFGMgr.{h,cpp}` 与 `LFG.h`。opcode 号即 3.3.5a 客户端编号。

## Opcode 表

| opcode | 值 | 方向 | 备注 |
|--------|----|------|------|
| `CMSG_LFG_JOIN` | 0x35C | C→S | 排队（副本列表、角色位、注释） |
| `CMSG_LFG_LEAVE` | 0x35D | C→S | 离队（仅队长有效，非队长被忽略） |
| `CMSG_LFG_GET_STATUS` | 0x296 | C→S | 主动查状态 |
| `CMSG_LFG_SET_ROLES` | 0x36A | C→S | role check 期间提交角色（uint8） |
| `CMSG_LFG_SET_BOOT_VOTE` | 0x36C | C→S | 踢人投票 |
| `CMSG_LFG_PROPOSAL_RESULT` | 0x362 | C→S | uint32 proposalID + bool accept |
| `CMSG_LFG_TELEPORT` | 0x370 | C→S | 进本/出本传送 |
| `SMSG_LFG_JOIN_RESULT` | 0x364 | S→C | 排队结果（含失败原因） |
| `SMSG_LFG_ROLE_CHECK_UPDATE` | 0x363 | S→C | role check 起止与成员角色 |
| `SMSG_LFG_ROLE_CHOSEN` | 0x2BB | S→C | 某成员已选角色 |
| `SMSG_LFG_QUEUE_STATUS` | 0x365 | S→C | 队列状态/等待时间 |
| `SMSG_LFG_PROPOSAL_UPDATE` | 0x361 | S→C | 组队提案 |
| `SMSG_LFG_BOOT_PROPOSAL_UPDATE` | 0x36D | S→C | 踢人提案 |
| `SMSG_LFG_PLAYER_INFO` | 0x36F | S→C | 玩家可排队副本与锁定 |
| `SMSG_LFG_PARTY_INFO` | 0x372 | S→C | 队伍成员可排队信息 |
| `SMSG_LFG_UPDATE_PLAYER` | 0x367 | S→C | 玩家侧队列状态变化 |
| `SMSG_LFG_UPDATE_PARTY` | 0x368 | S→C | 队伍侧队列状态变化 |
| `SMSG_LFG_UPDATE_SEARCH` | 0x369 | S→C | 搜索/奖励窗口 |
| `SMSG_LFG_OFFER_CONTINUE` | 0x293 | S→C | 副本结束询问是否继续 |
| `SMSG_LFG_PLAYER_REWARD` | 0x1FF | S→C | 随机副本奖励 |
| `SMSG_LFG_TELEPORT_DENIED` | 0x200 | S→C | 传送被拒（带原因） |
| `SMSG_LFG_DISABLED` | 0x398 | S→C | 服务端关了对应选项 |
| `CMSG_LFG_SET_NEEDS` | 0x36B | — | 服务端注册为 STATUS_NEVER，实际不处理 |

## CMSG_LFG_JOIN 精确布局（`LFGPackets.cpp` 的 `LFGJoin::Read()`）

| 顺序 | 类型 | 字段 | 说明 |
|------|------|------|------|
| 1 | uint32 | `Roles` | 角色位掩码 |
| 2 | uint8 | `NoPartialClear` | bool，一般 0 |
| 3 | uint8 | `Achievements` | bool，一般 0 |
| 4 | uint8 | Slots 个数 | 不能为 0，否则服务端直接忽略 |
| 5 | uint32×N | `Slots` | 低位 24 bit 是 dungeonID，高 8 bit 是 type（即 `dungeonID` 或上 `type<<24`）；服务端只用 `& 0x00FFFFFF` 取 dungeonID |
| 6 | uint8 | needs 个数 | 客户端固定 3，服务端跳过该值 |
| 7 | uint8×3 | `Needs` | 坦/治/DD 需求 |
| 8 | CString | `Comment` | **NUL 结尾**。AC `ByteBuffer::operator>>(std::string&)` = `ReadCString(true)`（`src/server/shared/Packets/ByteBuffer.h:291-295`），`LFGJoin::Read()` 末尾就是 `_worldPacket >> Comment`（`LFGPackets.cpp:34`）。漏写尾部 `\0` 会让服务端把后续字节吃进字符串、之后字段全部错位。 |

其它 CMSG：`LFGLeave` 空包；`CMSG_LFG_SET_ROLES` uint8；`CMSG_LFG_PROPOSAL_RESULT` uint32 + bool；`CMSG_LFG_TELEPORT` bool。

## 枚举与常量

- 角色位（`LFG.h` `LfgRoles`）：`NONE=0x00, LEADER=0x01, TANK=0x02, HEALER=0x04, DAMAGE=0x08`。队长排队时带 `LEADER`。
- 需求（`LFG.h` `LFGEnum`）：`TANKS_NEEDED=1, HEALERS_NEEDED=1, DPS_NEEDED=3`。
- 副本类型（`LFGMgr.h` `LfgType`）：`NONE=0, DUNGEON=1, RAID=2, ZONE=4, HEROIC=5, RANDOM=6`；类型来自 DBC `lfgdungeons.TypeID`，服务端只收 DUNGEON/HEROIC/RAID/RANDOM。
- 服务端选项（`LFGMgr.h` `LfgOptions`）：`ENABLE_DUNGEON_FINDER=0x01, ENABLE_RAID_BROWSER=0x02, ENABLE_SEASONAL_BOSSES=0x04`，对应配置 `DungeonFinder.OptionsMask`（默认 5）。
- 状态（`LFG.h` `LfgState`）：`NONE → ROLECHECK → QUEUED → PROPOSAL → DUNGEON → FINISHED_DUNGEON`，另有 `BOOT`、`RAIDBROWSER`。
- 时间：role check 45s、boot 120s、proposal 40s、队列刷新 8s、踢人需 3 票。
- 法术：deserter `71041`、副本冷却 `71328`、Luck of the Draw `72221`。

## 会被"静默忽略"的前置（`HandleLfgJoinOpcode`）

- `DungeonFinder.OptionsMask` 没开对应类型（三个 option 全为 0）。
- 自己在队伍里但不是队长，且（队伍已满 或 队伍不是 LFG 组）。
- `Slots` 为空。

`HandleLfgLeaveOpcode` 只有队长（或不在队伍）才真正退出队列。**排队前先确认自己是队长**，并检查服务端 OptionsMask。

## 需要解析的 SMSG（格式读服务端发送函数）

`LFGMgr.cpp`：`SendLfgJoinResult`、`SendLfgUpdatePlayer` / `SendLfgUpdateParty`（`LfgUpdateData`）、`SendLfgRoleCheckUpdate`、`SendLfgQueueStatus`、`SendLfgUpdateProposal`、`SendLfgBootProposalUpdate`、`SendLfgRoleChosen`。
解析目标：把 join / rolecheck / queue / proposal 四类状态映射进状态机，并在 role check 时发 `CMSG_LFG_SET_ROLES`、在 proposal 时发 `CMSG_LFG_PROPOSAL_RESULT`。

## 数据源

- DBC `lfgdungeons`：ID → 类型、名称、等级区间、组类型；**随机副本就是 TypeID=6 的条目**（如"随机巫妖王地下城"）。
- DB `lfg_dungeon_template`：`dungeonId, name, position_x, position_y, position_z, orientation, ...` —— 进本/出本落点，也是回退路径（自己走副本入口）的坐标来源。
- DB `lfg_dungeon_rewards`：`dungeonId, maxLevel, firstQuestId, otherQuestId` —— 随机副本奖励任务。

## 队列 → 进本时序（bot 侧动作）

1. 组队：`GroupInvite`/`GroupAccept`，用 `WaitGroupList` 等 `SMSG_GROUP_LIST`。
2. 队长发 `CMSG_LFG_JOIN`（随机副本用 `(6<<24)|id`），等 `SMSG_LFG_JOIN_RESULT`。
3. `SMSG_LFG_ROLE_CHECK_UPDATE` → 每个成员发 `CMSG_LFG_SET_ROLES`，等 role check 结束。
4. 等 `SMSG_LFG_QUEUE_STATUS`（当作周期状态观测点）。
5. `SMSG_LFG_PROPOSAL_UPDATE` → 发 `CMSG_LFG_PROPOSAL_RESULT(proposalID, true)`。
6. 进本：等 `SMSG_NEW_WORLD` → 重建对象缓存 → 确认 MapID 是副本地图。
7. 收尾：`SMSG_LFG_OFFER_CONTINUE`（继续或离开）、`SMSG_LFG_PLAYER_REWARD`；离开用 `CMSG_LFG_LEAVE`（队长）或 `CMSG_LFG_TELEPORT`。
