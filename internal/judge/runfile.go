package judge

import "os"

// writeRunFile 写一个"牢房里的程序必须能读到"的文件：源码、护栏、输入与答案文件都算。
//
// 为什么不相信 os.WriteFile 的那三个模式位：它们要过**判机自己的 umask**。systemd 单元里
// `UMask=0027` 是很合理的默认（服务不该到处留可写文件），于是请求的 0644 落盘变成 0640；
// 而以 root 跑的判机在 exec 前把身份降到 nobody 之后，那份 root 所有、别人不可读的文件
// 连**学生自己都读不到** —— 编译打不开 main.cpp，解释器打不开 pyguard.py，判题器与交互器
// 打不开输入与答案文件，全部是门口一个 EACCES。
//
// 所以这里写完再显式 chmod 一次：chmod 不受 umask 影响。判机的工作目录每次跑完就删，
// 这几份文件也只活这一次判题，把它们给"其他"读是它们唯一有用的读法。
func writeRunFile(path string, body []byte) error {
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o644); err != nil {
		return err
	}
	return nil
}
