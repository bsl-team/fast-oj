//go:build !linux

package judge

import "os/exec"

// applyJail 在这个平台上没有对应的内核机制，所以沙盒是"没有"而不是"降级"：
// -sandbox=on 会在启动时被拒绝（见 cmd/fast-judge），不会悄悄按老路子跑。
func applyJail(*exec.Cmd, Limits) error { return nil }

// unshareRequested 在这个平台上永远为假：没有命名空间可开，也就不会有那种被误读成
// "文件没权限"的 unshare 失败。
func unshareRequested(*exec.Cmd) bool { return false }
