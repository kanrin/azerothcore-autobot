# 30 · WoW 3.3.5a 协议与数据规则

## 规范来源（唯一真相）

协议以服务端源码为准，顺序如下：

1. 包结构：`/Users/orz/Projects/other/azerothcore-wotlk/src/server/game/Server/Packets/*.h/.cpp`
   里客户端包的 `Read()` —— 它就是你必须写出的字节序。
2. opcode 号：`src/server/game/Server/Protocol/Opcodes.cpp`（编号即 3.3.5a 客户端 opcode）。
3. 处理逻辑与状态：`src/server/game/Handlers/*.cpp`（例：`LFGHandler.cpp`、`QuestHandler.cpp`、`GossipHandler.cpp`）。
4. 机制与状态机：对应子系统目录（例：`src/server/game/DungeonFinding/`）。

**禁止凭记忆写包格式。** 写任何 `CMSG_*` 前必须先读它的 `Read()` 并写一条 golden 字节测试。

## 编码约定

- 小端；字符串**一律** CString（NUL 结尾）。AC 的 `ByteBuffer::operator>>(std::string&)` 就是 `ReadCString(true)`
  （`src/server/shared/Packets/ByteBuffer.h:291-295`），**没有** uint32 长度前缀的变体。`LFGJoin::Read()` 的 `Comment`
  （`LFGPackets.cpp:34`）和 `CMSG_LFG_SET_COMMENT`（`LFGHandler.cpp:125`）都是这样。漏写尾部 `\0` 会让服务端把后面的
  字节读进字符串，之后所有字段错位 —— 表现为"字段值莫名其妙"，不是报错。
- GUID 有两种编码，别混用：
  - **裸 uint64 小端**（8 字节）——通用情况。`ByteBuffer >> ObjectGuid` 就是 `read<uint64>()`
    （`src/server/game/Entities/Object/ObjectGuid.cpp:76-80`）。任务、闲聊、组队、`CMSG_CHAR_DELETE` 用它。
  - **packed**（掩码 + 逐字节）——只有移动类包用（`PackedGuidReader`，`ObjectGuid.cpp:88-91`）。
  - 拿不准时对照 AzerothGhost 里同类包的写法，或直接看 AC 对应 `Read()`。
- `bool` 在 AC 的 `WorldPacket` 里是 1 字节；位域顺序严格按 `Read()` 里的顺序。
- 发送用 `WorldClient.SendPacketRaw(opcode, data)`（上游已提供的加密封包路径），不要在项目里另写加密。

## 认证与会话

- SRP6 认证用上游 `client.NewAuthClient(user, pass).Authenticate(addr)`，拿到 realm 列表与 `SessionKey()`。
- 世界连接：`client.NewWorldClient(strings.ToUpper(user), sessionKey, logf)` → `Connect(realm.Address)` →
  `go w.Run()` → `WaitForSessionPhase(client.PhaseAuthed, 20s)`。
- **世界地址是 authserver 按"客户端来源 IP"现算的**，不是 realmlist 表里的 `address` 原样返回：
  `AuthSession::SendRealms` 发的是 `realm.GetAddressForClient(GetRemoteIpAddress())` 的 `lexical_cast<string>`
  （`apps/authserver/Server/AuthSession.cpp:787`，**带端口**），选路在 `shared/Realms/Realm.cpp:23-56`：
  客户端是 loopback → 回环/LocalAddress；客户端落在 `localSubnetMask` 网段内 → `localAddress`；其余 → `address`
  （ExternalAddress）。**跨主机开发时**若 `realmlist.localAddress = 127.0.0.1`，authserver 会把 `127.0.0.1:8085`
  发给我们 → 世界连接必然打到自己。修法：把 `realmlist.localAddress` 设成服务端真实 IP（≤ `RealmsStateUpdateDelay`
  = 20s 生效，对同网段的真实客户端也正确）；本项目在 `Session.RealmAddress()` 里回显实际拿到的地址，便于诊断。
- 进角色列表/建角前必须依次发：`SendReadyForAccountDataTimes()`、`SendRealmSplit()`、`RequestCharList()`。
  这是 AC 的会话前置，缺了会出现"包发了没反应"。
- 认证要重试：authserver 单网络线程，多 bot 并发登录时 logon proof 读超时会偶发，按上游做法重试 3 次并退避。
- 失败清理：任何中途失败都要 `w.Close()`，否则旧 socket 仍在用旧 `session_key`，会出现 `AUTH_FAILED` 连锁失败。

## 阶段门禁

- 只有 `PhaseInWorld` 才能发移动/施法/攻击等玩法包。
- 跨图（`FarTransfer`）后对象缓存清空 → 重新等待单位；近距传送（`NearTeleport`）等待 TELEPORT_ACK。
- 移动必须走 `movement.MovementController` + 心跳/时间戳（`SendHeartbeatAtTime` 等）。
  位置/朝向跳变或超频会被服务端纠正甚至踢下线；调试传送用 `.tele` 并记录，别当成正常路径。

## 开发期服务端配置

- `Warden.Enabled = 0`。原因：AzerothGhost 发送 AUTH_LOGON_CHALLENGE 的 OS FourCC 是前导 NUL
  写法（`[4]byte{0,'n','i','W'}`），AC 的 `_os = os.data()` 会得到空串并写进 `account.os`；
  Warden 打开时 `WorldSocket::HandleAuthSession` 只接受 `Win`/`OSX`
  （硬拒绝：`SendAuthResponseError(AUTH_REJECT)` + `DelayedCloseSocket`，`WorldSocket.cpp:608-615`），
  于是每次登录都报 `attempted to log in using invalid client OS ()`。等上游修好再打开 Warden。
  - **AC 发行默认就是 `Warden.Enabled = 1`**（`worldserver.conf.dist:1167`），而 AC 的 docker 资产
    （`apps/docker/entrypoint.sh` 只把 `.dist` 复制成 `.conf`、`conf/dist/env.docker` 没有 Warden 覆盖）
    不会改它 —— 所以新起的 docker 服务端默认开着 Warden。
  - 上游修法：发送 `[4]byte{'n','i','W',0}`（NUL 放到最后）。在此之前只能关 Warden。
  - **不要**为了过检查去 UPDATE `acore_auth.account.os = 'Win'`：那属于规避反检测，规则禁止。
  - 本机实测（Mac 192.168.50.90 → 服务端 192.168.50.11）：auth 阶段成功、世界握手立刻回
    `SMSG_AUTH_RESPONSE` 码 **14 = AUTH_REJECT**，同时 `account.os` 是**空串**。这两个证据一起出现
    即可确诊，不需要读服务端日志。
- 需要的其它开关：`DungeonFinder.OptionsMask = 5`（默认值即可，含随机副本与季节性 boss）。
  若某些活动被关掉（如季节性），LFG 仍可指定普通副本路径。

## 数据库边界

| 库 | 用途 | 允许写？ |
|----|------|---------|
| `acore_auth` | 账号 provisioning（`account` + `account_access`）、校验 | 仅 bot 前缀账号，幂等 |
| `acore_characters` | 校验角色状态（等级/坐标/任务状态）、测试清理 | 仅自己创建的角色 |
| `acore_world` | 世界知识：任务、生物、掉落、LFG 表 | **只读** |

常用只读表：`quest_template`、`creature_queststarter/ender`、`creature`、`gameobject`、
`lfg_dungeon_template`（副本入口坐标）、`lfg_dungeon_rewards`、`spell_dbc`/`item_template`（按需）。

写操作硬性要求：幂等（`INSERT ... ON DUPLICATE KEY UPDATE` 之类）、有对应清理函数、
清理在 `defer` / `t.Cleanup` / 进程退出钩子里执行；绝不动非 bot 账号与角色。

## 账号与角色约定

- 账号名与角色名统一前缀（默认 profile 里配置），用于批量生成与清理；密码由 profile 提供。
- 复用上游的 SRP6 计算：`e2eharness.ComputeSRP6(user, pass)` + `EnsureAccount(db, user, pass)`，
  不要自己实现 SRP6 verifier。
- 角色名要用上游 `e2eharness.SanitizeCharName` / `UniqueLetterNames` 的同类规则：AC 默认只接受
  字母，2–12 字符，不能有前导/尾随/连续连字符，且要与已有角色去重。
- 死亡骑士有服务端前置（账号内需有 55 级以上角色，除非服务端关掉该限制）；bot 建号默认避开 DK。

## 已确认的上游缺口（必须自己解析/实现）

基于 AzerothGhost v1.0.9 实测清点（`client/world.go` 的 `case Smsg*` 列表）：

- 已处理：认证、角色列表/创建、登录校验、时间同步、Warden、登出、战斗、法术/光环、对象更新、
  怪物移动、传送、战利品与掷骰、交易、战场/竞技场状态、聊天、升级、组队（邀请/拒绝/列表/解散）、
  复活、新地图/传送、速度变化、召唤请求、副本重置。
- **未处理**：`SMSG_QUESTGIVER_*`、`SMSG_QUEST_*`（任务查询/更新）、任务日志、`SMSG_GOSSIP_*` 与 NPC 文本、
  LFG 全部包。这些是本项目的核心工作量，见 `40-features.md` 与 skill 的 `references/`。
- **声明了但没解析**：`SmsgCharDelete = 0x03C`（`client/world.go:44`）。删角色必须自己 arm
  （本项目走 `Session.Do`，见 `internal/wow/character.go` 的 `DeleteCharacter`）。注意：GUID 不在服务端
  角色缓存里时 AC **静默返回、一个包都不发**（`CharacterHandler.cpp:652-658`），所以只会表现为超时。

## 常见坑（踩过或上游文档点名）

- 任务/副本包常量存在 ≠ 已实现解析；不要以为调个函数就能拿任务。
- 物体/单位不在视野缓存时，`GetObject` 返回 nil：传送后先 `WaitUnit`/轮询，别硬编码 GUID。
- GM 命令只能用于 setup；战斗中 `.gm on` 或 `.damage` 会导致 evade/重置。
- 背包满时奖励可能进邮箱（`LFG.MailItemOnFullInventory`），断言奖励要看邮寄兜底。
- 组队邀请的接受者如果不是队长，某些包（LFG join）会被服务端静默拒绝；先确认队长身份。
- 一次会话里 `CMSG_LFG_JOIN` 的 `Slots` 为空会被服务端忽略；至少选一个副本/随机副本。
- **非 instant 登出有 20 秒倒计时**（`WorldSession.h:547`：`curr_time >= _logout_time + 20`），等
  `SMSG_LOGOUT_COMPLETE` 的超时必须 ≥30s；且被拒时（`MiscHandler.cpp:436-441`：战斗/下落/决斗/AFK）
  服务端**只发** `SMSG_LOGOUT_RESPONSE`（`uint32 reason + uint8 instant`，`CharacterPackets.cpp:30-35`）
  且**不发** COMPLETE —— 只等 COMPLETE 会把"被拒"误报成超时。本项目 `Session.Logout` 两个都 Arm 并判 reason。
- **`SMSG_LOGIN_VERIFY_WORLD` 不带角色自身更新字段**：紧随其后另有一条自身的 `SMSG_UPDATE_OBJECT`。
  实测此刻 `Health()==0`、`state` 快照为空，必须等自身对象进缓存（`MaxHealth() > 0`）再读。
- **race/class 组合要合法**：3.3.5a 没有人类猎人（1/3）等组合；服务端回 `CHAR_CREATE_ERROR` 且日志写
  `invalid race/class pair (R/C)`。权威矩阵在 `acore_world.playercreateinfo`：
  `SELECT race, GROUP_CONCAT(class ORDER BY class) FROM playercreateinfo GROUP BY race`。
