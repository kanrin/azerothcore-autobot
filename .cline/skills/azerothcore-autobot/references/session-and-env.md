# 环境、依赖与登录流程

## 依赖与 replace（已在本机实测）

```text
module azerothcore-autobot

go 1.26

require (
	github.com/azerothcore/AzerothGhost v1.0.9
	github.com/go-sql-driver/mysql v1.10.0
	gopkg.in/yaml.v3 v3.0.1
)

require github.com/o0olele/detour-go v0.0.0-20260624174442-8056537019f8 // indirect

replace github.com/o0olele/detour-go => github.com/walkline/detour-go v0.0.0-20260624174442-8056537019f8
```

- 最后两行必须存在：AzerothGhost 的 go.mod 用 `replace` 把 `o0olele/detour-go` 指到 `walkline/detour-go`，
  而 `replace` 不跨模块传递。缺了它，`go mod tidy` 报
  `github.com/o0olele/detour-go@v0.0.0-20260624174442-8056537019f8: invalid version: unknown revision 8056537019f8`。
- 本机 GOPROXY = `https://goproxy.cn`（中国大陆镜像），下载慢先怀疑网络。
- 与本地 AzerothGhost checkout 联调：用 `go.work`（gitignore）或临时 `replace` 指向本地目录。
- `e2e/e2eharness` 没有 build tag，可被本项目直接 import（会带上 `go-sql-driver/mysql`）。

## 服务端侧依赖（本项目需要但不在本仓库）

| 需要 | 说明 |
|------|------|
| authserver | `127.0.0.1:3724`，账号认证 + realm 列表 |
| worldserver | `127.0.0.1:8085`，世界会话；SOAP `7878` 可选（运维脚本） |
| MySQL | `127.0.0.1:3306`，`acore_auth` / `acore_characters` / `acore_world`，默认 `acore:acore` |
| 寻路数据 | `maps/`、`mmaps/`、`vmaps/`（mmaps 缺失 = 导航不可用）。docker 部署由 `ac-client-data-init` 服务写入数据卷；本地构建用 `apps/extractor/extractor.sh` |
| 配置 | 开发期 `Warden.Enabled = 0` |

起服务端最快的路径是 `azerothcore-wotlk` 仓库的 `docker-compose.yml`（服务：`ac-database`、`ac-db-import`、
`ac-authserver`、`ac-worldserver`、`ac-client-data-init`）。

## 本机实测环境（2026-10-11，服务端已起）

| 项 | 实测值 |
|----|--------|
| 跑 bot 的机器 | Mac `192.168.50.90` |
| 服务端（Docker，另一台机器） | `192.168.50.11`：auth `3724` ✔、world `8085` ✔、MySQL `3306` ✔；`7878` SOAP **未监听**（`SOAP.Enabled` 默认 0） |
| MySQL 账号 | **只有 `root` / `password` 能从远端连接**；`acore:acore` 返回 `ERROR 1045` |
| 初始数据 | `acore_auth.account` = 0 行、`acore_characters.characters` = 0 行、`acore_world.lfg_dungeon_template` = 24 行 |
| realmlist | `address=127.0.0.1`、`localAddress=127.0.0.1`、mask `255.255.255.0`、port `8085`、build `12340`。**必须把 `localAddress` 改成 `192.168.50.11`**，否则 authserver 会把 `127.0.0.1:8085` 发给客户端（原因见 `30-wow-protocol.md` 的地址选路） |
| Warden | `worldserver.conf.dist` 默认 1，容器没有覆盖 → 世界握手被 `AUTH_REJECT(14)` 拒。开发期必须改 0 并重启 worldserver |
| 客户端数据 | 登录/建号**不需要** maps/mmaps/vmaps（AzerothGhost 的 `client` 包不读数据目录）；做寻路/副本时再准备 |

关掉 Warden（conf 是 `${DOCKER_VOL_ETC:-./env/dist/etc}` 的 bind mount，容器内路径
`/azerothcore/env/dist/etc/worldserver.conf`）：

```bash
docker exec ac-worldserver sh -c "grep -n 'Warden.Enabled' /azerothcore/env/dist/etc/worldserver.conf"
docker exec ac-worldserver sh -c "sed -i 's/^Warden.Enabled = 1/Warden.Enabled = 0/' /azerothcore/env/dist/etc/worldserver.conf"
docker restart ac-worldserver
docker logs --tail 30 ac-worldserver | grep -i 'World initialized'
```

凭据只进 `.env.local`（gitignore），CLI 与 `-tags=e2e` 测试共用同一份：

```bash
set -a; source .env.local; set +a
go run ./cmd/autobot probe --account autobotprobe                 # 认证 + 世界连接，报告停在哪一步
go run ./cmd/autobot login --account autobotprobe --char-name Autobotprobe
go test -tags=e2e ./test/e2e/... -count=1 -v -timeout 10m
```

## 环境变量（沿用上游命名）

| 变量 | 默认 | 含义 |
|------|------|------|
| `E2E_AUTH_ADDR` | `127.0.0.1:3724` | 客户端看到的 auth/realm 入口 |
| `E2E_AUTH_DSN` | `acore:acore@tcp(127.0.0.1:3306)/acore_auth` | auth 库 |
| `E2E_CHAR_DSN` | `acore:acore@tcp(127.0.0.1:3306)/acore_characters` | 角色库 |
| `E2E_WORLD_DSN` | `acore:acore@tcp(127.0.0.1:3306)/acore_world` | 世界库（寻路/清理用） |
| `E2E_WORLD_LOG` | `info` | `info`/`debug`/`trace`/`warn`/`error`/`silent` |

参考文件：`azerothcore-wotlk/e2e/.env.example`。测试里 `set -a; source .env; set +a` 再跑 `go test`。

## profile 示例（`~/.config/autobot/profiles/local-ac.yml`）

```yaml
auth_addr: "127.0.0.1:3724"
realm_index: 0
data_dir: "/path/to/ac-data"      # 含 maps/ mmaps/ vmaps/
account:
  prefix: "autobot"               # 账号/角色名前缀，便于清理
  username: "autobot01"
  password_env: "AUTOBOT_PASSWORD"  # 从环境变量取，不落盘
character:
  name: "Autobot01"
  race: 1        # Human
  class: 1       # Warrior
  gender: 0
database:
  auth_dsn_env: "E2E_AUTH_DSN"
  char_dsn_env: "E2E_CHAR_DSN"
  world_dsn_env: "E2E_WORLD_DSN"
behavior:
  accept_party_invites: true
  invite_allow_prefixes: ["autobot", "Autobot"]
  lfg: { enabled: true, roles: ["TANK"], dungeons: ["random"] }
```

## 登录 / 建号流程（照抄上游 `e2eharness.LoginBot` 的做法）

```
EnsureAccount(db, user, pass)                    # INSERT account(salt,verifier,expansion=2) + account_access
  ↓
NewAuthClient(user, pass).Authenticate(authAddr)  # 失败重试 3 次、退避 300ms×n（authserver 单网络线程）
  ↓ SessionKey()
NewWorldClient(strings.ToUpper(user), sessionKey, logf)
  ↓ SetCharRace(race)（GM 命令的聊天语言取决于种族）
Connect(realms[0].Address) → go w.Run()
  ↓
WaitForSessionPhase(PhaseAuthed, 20s)
SendReadyForAccountDataTimes() / SendRealmSplit()
RequestCharList() → OnCharList（空则 CreateCharacter，结果看 OnCharCreateResult）
  ↓
LoginCharacter(guid) → WaitForLogin(60s)         # = SMSG_LOGIN_VERIFY_WORLD → PhaseInWorld
```

要点：

- 角色名要 sanitize（字母、2–12 字符、无前导/尾随/连续连字符），重名换名重试（`0x2F` 成功、`0x32` 重名）。
- 任何失败都要 `w.Close()`；否则旧 socket 仍用旧 `session_key` 认证 → 后续全部 `AUTH_FAILED`。
- 进世界后如果是首次角色，可能会走 cinematic 相关包；上游有 `CompleteCinematic` / `NextCinematicCamera` 可用。
- 离线校验用 DB；不要只信客户端缓存。

## 常见错误对照表

| 现象 | 原因 | 处理 |
|------|------|------|
| `attempted to log in using invalid client OS ()` | Warden 打开 + OS FourCC 前导 NUL 写法 | `Warden.Enabled = 0` |
| `AUTH_FAILED` 反复 | 旧连接未关闭、`session_key` 被覆盖 | 重连前 `Close()` |
| auth 偶发超时 | authserver 单网络线程 | 重试 3 次 + 退避 |
| 登录后发包无响应 | 缺 `SendReadyForAccountDataTimes`/`SendRealmSplit`，或阶段不是 `InWorld` | 补齐前置、等阶段 |
| `go mod tidy` 失败 `unknown revision` | 缺 detour `replace` | 见本文开头 |
| 寻路报错 / 不移动 | `data_dir` 缺 mmaps/vmaps | 补齐数据或改用采集好的数据卷 |
| `logout timeout`（等 15s 没用） | 非 instant 登出有 20s 倒计时 | 超时 ≥30s（`DefaultLogoutTimeout`） |
| 登出被拒却报超时 | 只等了 COMPLETE，被拒时服务端只发 RESPONSE | Arm 两个包并检查 reason（`ErrLogoutRefused`） |
| `health=0 max_health=0` 刚进世界 | 自身 `SMSG_UPDATE_OBJECT` 还没到 | 等 `MaxHealth() > 0`（`EnterWorld` 的 `ReadyTimeout`） |
| `CHAR_CREATE_ERROR` + 建号被拒 | race/class 组合服务端不支持（如人类猎人） | 查 `playercreateinfo` 矩阵；config 已做启动期守卫 |

## 实测环境（2026-10-12 验证通过）

- 拓扑：bot 在 Mac `192.168.50.90`；服务端 Docker 在 `192.168.50.11`（auth 3724 / world 8085 / MySQL 3306 / SOAP 7878）。
- 服务端容器：`ac-authserver`、`ac-worldserver`、`ac-database`；SSH 只读访问 `ssh orz@192.168.50.11`。
- `Warden.Enabled = 0`（已确认并重启验证：`docker exec ac-worldserver grep -n '^Warden.Enabled' /azerothcore/env/dist/etc/worldserver.conf` → `0`）。
- 客户端数据：docker 命名卷 `azerothcore_ac-client-data` → 容器内 `/azerothcore/env/dist/data`（ro），
  含 `dbc`(247 文件/87M，有 Achievement/AuctionHouse/MailTemplate.dbc)、`maps`(295M)、`mmaps`(2.1G)、`vmaps`(657M)。
  本机副本在仓库 `ac-data/`（gitignore，`AUTOBOT_DATA_DIR` 指向它）。
- DB 凭据：远端只用 `root` / `password`（`acore:acore` 在本机不可用）；实盘套件跑完 DB 为 0 账号 / 0 角色。
- 实盘命令与顺序：`go test -tags=e2e ./test/e2e/... -count=1 -v -timeout 15m`
  （`zz_cleanup_test.go` 必须最后跑；整套约 21 秒，其中约 20 秒是一次正常登出的倒计时）。
- 已验证结论：`probe` → `phase=authed`；`login` → `in world` + `database agrees` + `OK`；
  `state` 的金钱/物品槽/技能与 DB 逐字段一致；两次登录复用同一 guid；清理后 DB 归零。
