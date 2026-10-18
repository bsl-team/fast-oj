#!/usr/bin/env bash
# fast-oj 判机隔离与判机令牌的体检/修复脚本（Ubuntu/Debian，在生产判机那台机器上跑）。
#
#   sudo bash scripts/judge-isolation.sh --check           # 只看现在是什么状态，不动任何东西
#   sudo bash scripts/judge-isolation.sh --root            # 让 fast-judge 以 root 跑：牢房开得起
#   sudo bash scripts/judge-isolation.sh --apparmor        # 保持非 root，给判机一份 AppArmor profile
#   sudo bash scripts/judge-isolation.sh --rotate-token     # 换掉判机令牌并把它从命令行上拿掉
#
# 为什么需要它：判机上跑的是别人提交的代码。内核不给非特权 user namespace 时（Ubuntu 24.04
# 起 kernel.apparmor_restrict_unprivileged_userns=1 就是这种），牢房建不起来，判机会照常判题
# 只是没有隔离——这件事过去只在启动日志里写了一行，没人盯日志就等于没人知道。
#
# 每一步都要**当场验证**，不接受"配好了"的推断：--root / --apparmor 走完都要重新真建一次
# 牢房才算成。--check 不改任何东西。
set -euo pipefail

UNIT_JUDGE=${UNIT_JUDGE:-fast-judge}
UNIT_OJ=${UNIT_OJ:-fast-oj}
ENV_FILE=${ENV_FILE:-/etc/fast-oj/env}
JUDGE_BIN=${JUDGE_BIN:-/opt/fast-oj/fast-judge}
PROFILE=${PROFILE:-/etc/apparmor.d/opt.fast-oj.fast-judge}
JUDGE_USER=$(systemctl show "$UNIT_JUDGE" -p User --value 2>/dev/null || true)
[ -n "$JUDGE_USER" ] || JUDGE_USER=root

die() { echo "$*" >&2; exit 1; }

need_root() { [ "$(id -u)" = 0 ] || die "这一步要 root：sudo bash $0 $1"; }

# jail_ok 真建一次牢房跑 /bin/true：这是唯一算数的判据。unshare 成功不等于判机的牢房能建起来
# （还要 mount/chroot/降权那一串），反过来 unshare 直接被拒就一定建不起来。
jail_ok() {
  local user=$1
  if [ "$user" = root ]; then
    unshare -Urm true >/dev/null 2>&1
  else
    su -s /bin/sh "$user" -c 'unshare -Urm true' >/dev/null 2>&1
  fi
}

# token_in_argv 只看判机那个进程自己的 argv。
#
# 别用 `ps -eo args= -C fast-judge | grep -- '-token'`：-e 会让 -C 形同不存在，于是列的是
# **全部进程**的命令行 —— 而调用这个脚本的那句 `... --rotate-token` 自己就含 "-token"，
# 结果永远报"泄露"，修好了也报修不好（这台机器上真就这么演了一遍）。
token_in_argv() {
  local pid
  pid=$(systemctl show "$UNIT_JUDGE" -p MainPID --value 2>/dev/null || true)
  [ -n "$pid" ] && [ "$pid" != "0" ] || return 1
  # 只匹配单独一个 -token 参数，绝不打印值本身。
  tr '\0' '\n' <"/proc/$pid/cmdline" 2>/dev/null | grep -qx -- '-token'
}

check() {
  echo "== 判机 $UNIT_JUDGE 的隔离状态 =="
  echo "  运行身份：$JUDGE_USER"
  if jail_ok "$JUDGE_USER"; then
    echo "  非特权 user namespace：可以 —— 牢房开得起"
  else
    echo "  非特权 user namespace：被拒 —— 牢房开不起来，提交现在是在 $JUDGE_USER 的身份下裸跑的"
    echo "  内核那条限制：$(sysctl -n kernel.apparmor_restrict_unprivileged_userns 2>/dev/null || echo '(没有这个开关)')"
  fi
  if token_in_argv; then
    echo "  判机令牌：**写在命令行上**（/proc/<pid>/cmdline 是所有本地账号可读的）——跑 --rotate-token"
  else
    echo "  判机令牌：没出现在命令行上（好）"
  fi
  if [ -f "$ENV_FILE" ]; then
    echo "  $ENV_FILE 权限：$(stat -c '%a %U:%G' "$ENV_FILE")"
  else
    echo "  $ENV_FILE：不存在（令牌只能写在 ExecStart 里的话，就一定在泄露）"
  fi
}

confirm() {
  printf '%s [y/N] ' "$1"
  read -r ans
  case "$ans" in [yY]*) return 0 ;; *) echo "没动任何东西。"; exit 1 ;; esac
}

# envValue 读出 EnvironmentFile 里某个键的当前值（不打印出来，只比较存在与否）。
has_env_key() { grep -q "^$1=" "$ENV_FILE" 2>/dev/null; }

rotate_token() {
  need_root "$1"
  [ -f "$ENV_FILE" ] || die "没有 $ENV_FILE：先把令牌放进一个 0600 的 EnvironmentFile，别写进 ExecStart"
  has_env_key FASTOJ_JUDGE_TOKEN || die "$ENV_FILE 里没有 FASTOJ_JUDGE_TOKEN 这一行"
  confirm "换掉判机令牌，并把 -token 从 ExecStart 里去掉（会重启 fast-oj 与 fast-judge）？"

  local new
  new=$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')
  local tmp
  tmp=$(mktemp)
  # 只改 FASTOJ_JUDGE_TOKEN 那一行；其余键原样留着。旧令牌就此作废：拿着它的人再也领不了单。
  awk -v v="$new" 'BEGIN{FS=OFS="="} $1=="FASTOJ_JUDGE_TOKEN"{$2=v} {print}' "$ENV_FILE" > "$tmp"
  install -m 0600 -o root -g root "$tmp" "$ENV_FILE"
  rm -f "$tmp"

  # drop-in 把 ExecStart 整个换掉：systemd 会先展开 $VAR 再 exec，所以只要 ExecStart 里
  # 还写着 -token，0600 的那个文件就白设了。判机二进制的 -token 默认值本来就是环境变量。
  local dir=/etc/systemd/system/$UNIT_JUDGE.service.d
  mkdir -p "$dir"
  cat > "$dir/no-argv-token.conf" <<'EOF'
# ExecStart 里不许出现判机令牌：/proc/<pid>/cmdline 是 0444，判机上跑的正是别人的代码。
# systemd 会先展开 ExecStart 里的 $VAR 再 exec，所以 0600 的 EnvironmentFile 也白设。
# 清空原 ExecStart 再写一份：令牌由 EnvironmentFile 提供，fast-judge 自己读环境变量。
#
# [Service] 这一段不是可省的装饰：drop-in 里落在段外的键会被 systemd 直接忽略，
# 于是"改了没生效"、判机继续以带令牌的命令行启动并拒绝起来（这次就踩过）。
[Service]
EOF
  # 从现有 unit 里抄 ExecStart，去掉 -token <值> 那一段。
  local exec
  exec=$(systemctl cat "$UNIT_JUDGE" | sed -n 's/^ExecStart=//p' | tail -1)
  [ -n "$exec" ] || die "读不到 $UNIT_JUDGE 的 ExecStart"
  exec=$(printf '%s' "$exec" | sed -E 's/[[:space:]]-token(=[^[:space:]]*| [^[:space:]]*)//g')
  { echo "ExecStart="; echo "ExecStart=$exec"; } >> "$dir/no-argv-token.conf"

  systemctl daemon-reload
  # 两个服务一起重启：服务器的判机令牌也来自同一个文件，不一起换会 401 到队列卡住。
  systemctl restart "$UNIT_JUDGE" "$UNIT_OJ"
  sleep 2
  if token_in_argv; then
    echo "还是写在命令行上，检查一下是不是有别的 drop-in 也在设 ExecStart" >&2
    exit 1
  fi
  echo "令牌已换，且不在命令行上了。"
  echo "现在跑：journalctl -u $UNIT_JUDGE -n 20   看它有没有重新连上（401 说明两边令牌不一致）。"
}

root_mode() {
  need_root "$1"
  confirm "把 $UNIT_JUDGE 改成以 root 跑（牢房靠这个开起来；提交仍然会降到 nobody）？"
  local dir=/etc/systemd/system/$UNIT_JUDGE.service.d
  mkdir -p "$dir"
  cat > "$dir/jail-as-root.conf" <<EOF
# 判机以 root 跑：非特权 user namespace 被 AppArmor 拦着的机器上，这是牢房开得起来
# 的那条路。学生程序仍然在降权之后才被 exec（internal/jail 的 drop()），拿到的是
# nobody/未映射的 uid、没有网络、看不到宿主文件系统。
#
# 没有 [Service] 这一行，systemd 会把 User= 当段落外的键忽略掉——改完看似成功，判机
# 仍以原来的身份跑，比不改更坏。
[Service]
User=root
EOF
  systemctl daemon-reload
  systemctl restart "$UNIT_JUDGE"
  sleep 2
  if jail_ok root; then
    echo "root 身份下 user namespace 可用。去 /judges 看那台机器的「隔离」一列，应当写着有牢房。"
  else
    echo "root 下也开不起来：这不是 AppArmor 那条限制的问题，先看 journalctl -u $UNIT_JUDGE -n 30" >&2
    exit 1
  fi
}

# apparmor 这条路给的是"保持非 root"的选项。profile 的内容是按 Ubuntu 的 userns 限制
# 写的最小一份；**它到底有没有生效由下面的实测决定**，不由这段文本决定。
apparmor_mode() {
  need_root "$1"
  confirm "给 $JUDGE_BIN 装一份允许非特权 user namespace 的 AppArmor profile（$JUDGE_USER 保持非 root）？"
  cat > "$PROFILE" <<EOF
# 让判机这个可执行文件可以用非特权 user namespace。
# Ubuntu 24.04 起 kernel.apparmor_restrict_unprivileged_userns=1：没被这样的 profile
# 允许的程序，unshare(CLONE_NEWUSER) 会被拒。
#
# 装完必须实测（这个脚本会做）：profile 写得不对、内核版本不同、或判机换了路径，
# 结果都是"照旧开不起牢房"。不要相信文本。
#include <tunables/global>

"$JUDGE_BIN" flags=(attach_disconnected) {{
  #include <abstractions/base>

  /opt/fast-oj/fast-judge          mrix,
  /usr/**                           rm,
  /bin/**                           rm,
  /lib/**                           rm,
  /lib64/**                         rm,
  /etc/ld.so.cache                  r,
  /proc/**                          rw,
  /sys/**                           r,
  /var/lib/fast-oj/**               rw,
  /var/tmp/**                       rw,
  /tmp/**                           rw,

  capability,
  network,
  mount,
  umount,
  pivot_root,
  change_profile,
  signal,
  ptrace,
  userns,
}}
EOF
  if ! apparmor_parser -Q -W "$PROFILE"; then
    rm -f "$PROFILE"
    die "这份 profile 内核不认（上面有原因）。已删掉，没留半套配置。"
  fi
  apparmor_parser -r "$PROFILE"
  sleep 1
  if jail_ok "$JUDGE_USER"; then
    echo "非特权 user namespace 通了。去 /judges 看「隔离」一列确认。"
  else
    echo "还是被拒：这份 profile 没起到想要的效果。要么用 --root，要么把它撤掉：" >&2
    echo "  sudo apparmor_parser -R $PROFILE && sudo rm $PROFILE" >&2
    exit 1
  fi
}

case "${1:-}" in
  --check) check ;;
  --root) root_mode "$1" ;;
  --apparmor) apparmor_mode "$1" ;;
  --rotate-token) rotate_token "$1" ;;
  *) die "用法：sudo bash $0 --check | --root | --apparmor | --rotate-token" ;;
esac
