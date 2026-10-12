---
name: azerothcore-autobot
description: "开发与调试 azerothcore-autobot —— Go 编写的 WoW 3.3.5a 客户端级自动化 bot（基于 AzerothGhost 协议库，对接本地 azerothcore-wotlk 服务端）。当任务涉及登录/建号、任务自动化、组队邀请接受、LFG/随机地下城排队进本、opcode 与包编解码、mmaps 寻路、实盘 e2e 验证，或排查『包发了没反应/卡住/登录失败』这类协议问题时使用。Use when working on the AzerothCore autobot: session login, character creation, quest automation, party auto-accept, dungeon finder (LFG), packet/opcode encoding, pathfinding, live-stack e2e tests, or protocol-level debugging."
---

# AzerothCore Autobot 开发技能

先读项目规则 `.clinerules/00`–`60`（始终生效）；本技能只补"怎么做"的操作细节。

## 1. 开工三步

1. **定证据**：这次要证明什么？一条命令 + 期望看到的日志/包/DB 结果。
2. **查上游**：先看 `references/capability-map.md` 里 AzerothGhost 是否已有实现；有就复用，不要重写。
3. **查规范**：包格式以 `/Users/orz/Projects/other/azerothcore-wotlk/src/server/game/Server/Packets/<X>.h` 的 `Read()` 为准，opcode 号查 `src/server/Protocol/Opcodes.cpp`。**不要凭记忆写字节序。**

## 2. 环境自检（实盘前一分钟做完）

```bash
lsof -nP -iTCP -sTCP:LISTEN | grep -E '3724|8085|3306'
mysql -h127.0.0.1 -uacore -pacore -e 'show databases;' | grep acore
ls "$DATA_DIR"/maps "$DATA_DIR"/mmaps "$DATA_DIR"/vmaps
```

- 缺任一端口 → 先起服务端（`azerothcore-wotlk` 仓库的 docker-compose 或本地构建）。
- 开发期必须 `Warden.Enabled = 0`，否则登录报 `attempted to log in using invalid client OS ()`。
- 环境变量沿用上游命名：`E2E_AUTH_ADDR`、`E2E_AUTH_DSN`、`E2E_CHAR_DSN`、`E2E_WORLD_DSN`、`E2E_WORLD_LOG`；详见 `references/session-and-env.md`。

## 3. 构建与测试命令

```bash
go build ./...
go vet ./... && gofmt -l .
go test ./...
go test -race ./...
go test -tags=e2e ./test/e2e/... -count=1 -v -timeout 60m -parallel 1 -p 1
go test -tags=e2e ./test/e2e/lfg -run TestLfg_JoinRandomQueue -count=1 -v -timeout 10m
```

依赖坑（已实测）：AzerothGhost v1.0.9 需要本仓库自己写 `replace`，见 `references/session-and-env.md`；缺了它 `go mod tidy` 会以 `unknown revision 8056537019f8` 失败。

## 4. 登录序列（照抄上游做法）

```go
auth := client.NewAuthClient(user, pass)
realms, err := auth.Authenticate(authAddr)          // 失败重试 3 次（authserver 单网络线程，偶发超时）
w := client.NewWorldClient(strings.ToUpper(user), auth.SessionKey(), logf)
w.SetCharRace(race)                                  // 影响 GM 命令的聊天语言
_ = w.Connect(realms[0].Address)
go func() { _ = w.Run() }()
w.WaitForSessionPhase(client.PhaseAuthed, 20*time.Second)
_ = w.SendReadyForAccountDataTimes()
_ = w.SendRealmSplit()                               // 缺这两步后面会"包发了没反应"
w.OnCharList = func([]client.CharEnumEntry) { /* ... */ }
w.OnCharCreateResult = func(data []byte) { /* data[0]: 0x2F 成功, 0x32 重名 */ }
_ = w.RequestCharList()
_ = w.CreateCharacter(name, race, class, 0, 1, 1, 1, 1, 1, 0)
_ = w.LoginCharacter(guid)
w.WaitForLogin(60 * time.Second)                     // = 等到 PhaseInWorld
```

任何中途失败都要 `w.Close()`；否则旧 socket 用旧 `session_key` 继续认证会连环 `AUTH_FAILED`。

## 5. 新写一个协议子系统（LFG / 任务都这么做）

1. 读 AC 的 `Packets/<X>.h` 的 `Read()`，抄成 Go 结构体 + 编码函数。
2. 写 golden bytes 单测：期望字节写死（按 AC 读取顺序推导）。
3. 发送用 `w.SendPacketRaw(opcode, data)`；接收用 `w.AddPacketHook(fn)`（多订阅者、返回 `cancel`，用例结束调用）。
4. 解析出的状态进自己的状态机；上层用 **Arm → Send → Wait**，不要 sleep。
5. 实盘先证明"能发出且服务端有回应"，再写决策逻辑。

现成资料：`references/lfg-protocol.md`、`references/quest-protocol.md`。

## 6. 排查手册（先分类，再动手）

| 现象 | 类别 | 先查 |
|------|------|------|
| 包发了没反应 | P | 是否 `InWorld`；`Read()` 顺序/字段；是否缺前置（如 `SendReadyForAccountDataTimes`） |
| 登录 `invalid client OS ()` | P | `Warden.Enabled` 是否为 0 |
| `AUTH_FAILED` 连锁 | R | 重连前是否先关旧连接（`session_key` 被覆盖） |
| 卡住不动 | R | 是否用 sleep 代替等包；GUID 是否因传送/缓存失效 |
| NPC 不理会 / 无仇恨 | R | GM 模式是否还开着（战斗前必须还原） |
| 寻路失败 / 贴墙 | R | `data_dir` 的 mmaps/vmaps 是否齐；目标点是否可达 |
| 行为与源码不符 | 疑似服务端 | 收最小复现证据（命令、期望 vs 实际、AC 源码行、日志时间对齐）再问用户 |

```bash
E2E_WORLD_LOG=debug go test -tags=e2e -run <Test> ./test/e2e/... -v
# 服务端侧临时开：worldserver.conf → Logger.network=4,Console Server / Logger.network.opcode=4,Console Server
jq -c 'select(.opcode=="0x35C")' lfg-trace.jsonl
```

## 7. 每个功能的最小验证配方

- **登录/建号**：`go run ./cmd/autobot login --char-name ProbeBot -v` → 日志出现 `PhaseInWorld`；再跑一次仍成功（幂等）；DB 只多出 bot 前缀账号/角色。
- **任务**：接取后 `acore_characters.character_queststatus` 从进行中变完成，回交后状态更新；用 `e2eharness.QuestStatus` / `WaitQuestStatus` 断言，不看客户端缓存。
- **组队接受**：另一 bot 邀请 → 目标自动入队且 `GroupState()` 出现两名成员；非白名单邀请被拒且不卡状态。
- **LFG**：5 bot 组队 → 队长排队 → 有 rolecheck/queued/proposal 的证据包 → 接受 proposal → 等到 `SMSG_NEW_WORLD` 且地图 ID = 副本地图。

## 8. 反模式

凭记忆写包、`time.Sleep` 代替等待、无限重试、吞错误继续跑、多 goroutine 抢同一个 socket、把缓存当真相、为让测试好看而改服务端或放宽断言、实盘用例不清理世界状态。

## 9. References

- `references/capability-map.md` —— 上游有什么 / 缺什么（函数名 + 文件路径）。
- `references/session-and-env.md` —— 依赖与 replace、环境变量、profile 示例、登录流程图、常见错误对照。
- `references/lfg-protocol.md` —— LFG opcode 表、`CMSG_LFG_JOIN` 布局、角色位、服务端发送函数索引、数据表。
- `references/quest-protocol.md` —— 任务/gossip opcode 表、需解析的 SMSG、世界知识表、任务状态机。
