package main

import (
	"fmt"
	"io"
)

// usage prints the CLI help. It is Chinese because it is operator-facing; every
// identifier, log line and error in the code stays English.
func usage(w io.Writer) {
	fmt.Fprint(w, `autobot — 客户端侧 WoW 3.3.5a 自动玩家

用法:
  autobot <子命令> [参数]

子命令:
  version              打印版本信息
  probe                只做认证 + 世界连接，报告停在哪一步（排查前置问题用）
  login                确保账号存在 → 建/选角色 → 进入世界 → 校验数据库 → 登出
  state                进入世界后打印自身状态（金钱/背包/技能），用于校验更新字段偏移
  run                  常驻：登录 → agent 循环 → Ctrl-C 优雅登出（可用 --goto 走一个点）
  account create       创建账号（已存在则刷新密码，幂等）
  account list         列出 bot 命名空间下的账号（--json 可选）
  account delete       删除账号（仍拥有角色时会拒绝，需先在游戏里删角色）

账号子命令:
  autobot account create --account NAME [--password PW] [--prefix P] [--timeout D]
  autobot account list   [--prefix P] [--json]
  autobot account delete --account NAME [--prefix P]

  密码优先取 --password；没有则从 AUTOBOT_PASSWORD（或 profile 的 account.password_env）读。
  写在命令行会留在 shell history 里，长期使用建议放环境变量或 profile。
  前缀是 bot 命名空间的守卫：账号与角色名都必须以前缀开头，越界会被拒绝。
  默认前缀 autobot（profile 的 account.prefix / AUTOBOT_ACCOUNT_PREFIX）；
  要创建别的命名空间（比如 zhl001），用 --prefix zhl 明确声明。

通用参数（每个子命令都接受）:
  --profile PATH       profile YAML 路径
  --auth-addr HOST:PORT 认证服务器地址（覆盖 profile 与环境变量）
  --realm N            realm 索引
  --account NAME       账号名（必须带 bot 前缀，前缀见 profile 的 account.prefix）
  --prefix P           bot 命名空间前缀（覆盖 profile 与环境变量；账号/角色名都必须以它开头）
  --char-name NAME     角色名
  --race N             种族（1=人类 2=兽人 3=矮人 4=暗夜精灵 5=亡灵 6=牛头人 7=侏儒 8=巨魔 10=血精灵 11=德莱尼）
  --class N            职业（1=战士 2=圣骑士 3=猎人 4=盗贼 5=牧师 6=死亡骑士 7=萨满 8=法师 9=术士 11=德鲁伊）
  --gender N           0=男 1=女
  --timeout DURATION   整个流程的上限（默认 3m）

配置优先级: 命令行 flag > 环境变量 > profile 文件 > 内置默认值。
密码与数据库 DSN 只从环境变量读取，profile 里只写变量名。
环境变量: AUTOBOT_*（本项目）与 E2E_*（上游同名，作为回退）都可以用；见 .env.example。

例:
  set -a; source .env.local; set +a
  autobot probe --account autobotprobe
  autobot login --account autobotprobe --char-name Autobotprobe --race 1 --class 1

  # 测试账号（密码同账号名）：前缀 zhl 由 --prefix 显式声明为 bot 命名空间
  autobot account create --account zhl001 --password zhl001 --prefix zhl
  autobot account list --prefix zhl
  autobot account delete --account zhl001 --prefix zhl
`)
}
