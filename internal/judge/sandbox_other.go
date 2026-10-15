//go:build !linux

package judge

import "os/exec"

// applyJail 在这个平台上没有对应的内核机制，所以沙盒是"没有"而不是"降级"：
// -sandbox=on 会在启动时被拒绝（见 cmd/fast-judge），不会悄悄按老路子跑。
func applyJail(*exec.Cmd, Limits) error { return nil }
