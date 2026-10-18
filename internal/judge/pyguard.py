# pyguard.py —— 判机在跑学生 Python 代码之前给解释器套上的护栏。
#
# 它是**纵深**，不是边界：真正的边界是牢房（无网络命名空间 + chroot + 内核 rlimit）。
# 这一层的价值是把最顺手的那几条逃逸路在解释器里就掐掉——起子进程、连网络、
# 用 ctypes 直接调 libc、装包——并且让学生**看得见是被挡的**（一条带中文原因的
# RuntimeError），而不是撞上一个查不出所以然的失败。
#
# 启动方式是 `python3 -I -B -S pyguard.py main.py`：-I 让 PYTHONPATH 之类的环境变量
# 进不来，-B 不落 .pyc，-S 不跑 site（少一条把 pip 的目录塞进 sys.path 的路）。
# 审计钩子装上就摘不掉（CPython 没有注销的接口），学生代码 exec/eval 出来的东西
# 一样会经过这里。
#
# 前提要说清：这一层假定**牢房开着**——它挡的是"离开这次运行的工作目录"，而那个
# 边界是 chroot 给的。判机没开沙盒时不收 Python（见 internal/judge/python.go 的闸门），
# 不是因为它信不过自己，而是因为把这句话单独讲就是骗人。

import sys

# 整个模块都不许 import 的那些：它们要么能起进程、要么能连网络、要么能绕过解释器
# 直接调系统调用。os 与 shutil 留着——它们只在这份工作目录里做事，而 random、pathlib、
# tempfile 这些常用库都要靠 os 才能起来；禁掉 os 会让一半标准库跟着塌。
#
# 这份名单要**短到只列真能出门的那几扇**，每多一项就多一次"学生的常用库突然 ImportError"：
# 实测 python3.14 的 pathlib 会去 import fcntl，而 fcntl 对着一个已经握在手里的 fd 做不了
# 任何越界的事——禁它就是纯伤害。同理 signal / termios / shlex / concurrent 也都放回来，
# 真要起进程的那条路（subprocess / multiprocessing / os.exec / pty）才是门。
DENY_MODULES = frozenset({
    "subprocess", "socket", "ssl", "select", "selectors", "asyncio",
    "ctypes", "multiprocessing", "pty", "webbrowser",
    "http", "urllib", "ftplib", "smtplib", "poplib", "imaplib", "telnetlib", "xmlrpc",
    "pip", "ensurepip", "venv", "distutils", "setuptools", "imp",
})

# 直接按事件名挡的（模块挡住了，事件是第二道）：这些 event 名来自 CPython 的审计文档。
DENY_EVENTS = frozenset({
    "os.system", "os.exec", "os.spawn", "os.startfile", "os.fork", "os.kill",
    "subprocess.Popen", "ctypes.dlopen", "ctypes.FFI", "ctypes.cfunc",
    "ctypes.memmgr", "pty.spawn", "socket.connect", "socket.getaddrinfo",
    "socket.sendto", "socket.recvfrom", "socket.accept", "urllib.Request",
    "sys.settrace",
})


def _guard(event, args):
    if event in DENY_EVENTS:
        raise RuntimeError("判机不允许这个操作：" + event + "（要进程、网络或直接调系统的题请改用 C++）")
    if event == "import":
        name = args[0] if args else ""
        top = str(name).split(".")[0]
        if top in DENY_MODULES:
            raise RuntimeError("判机不允许导入 " + str(name) + "（这台判机把进程、网络与装包的门都关着）")


def _main():
    if len(sys.argv) < 2:
        raise SystemExit("pyguard 需要一个要跑的脚本")
    sys.addaudithook(_guard)
    target = sys.argv[1]
    # 学生那一侧的 argv 应该看起来就是 `python3 main.py ...`，不该知道中间隔了一层护栏。
    sys.argv = sys.argv[1:]
    import runpy
    runpy.run_path(target, run_name="__main__")


if __name__ == "__main__":
    _main()
