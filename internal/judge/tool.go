package judge

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Tool describes how to build and run one language.
type Tool struct {
	Language   string
	SourceFile string
	Compile    []string
	Run        []string
}

// expand substitutes {src}, {exe} and {dir} in an argv template.
func (t Tool) expand(template []string, dir string) []string {
	if len(template) == 0 {
		return nil
	}
	src := filepath.Join(dir, t.SourceFile)
	exe := filepath.Join(dir, "program")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	out := make([]string, len(template))
	for i, arg := range template {
		out[i] = strings.NewReplacer("{src}", src, "{exe}", exe, "{dir}", dir).Replace(arg)
	}
	return out
}

func (t Tool) CompileArgs(dir string) []string { return t.expand(t.Compile, dir) }
func (t Tool) RunArgs(dir string) []string     { return t.expand(t.Run, dir) }
func (t Tool) Compiles() bool                  { return len(t.Compile) > 0 }

// DefaultTools covers the languages the judge knows out of the box. Templates
// are argv lists, never shell strings, so nothing here is interpolated into a
// shell command line.
func DefaultTools() map[string]Tool {
	cxx := []string{"g++", "-x", "c++", "-std=c++17", "-O2", "-w", "-static", "-o", "{exe}", "{src}"}
	return map[string]Tool{
		"cpp": {Language: "cpp", SourceFile: "main.cpp", Compile: cxx, Run: []string{"{exe}"}},
		"c": {Language: "c", SourceFile: "main.c",
			Compile: []string{"gcc", "-x", "c", "-std=c11", "-O2", "-w", "-static", "-o", "{exe}", "{src}"},
			Run:     []string{"{exe}"}},
	}
}

// Available drops tools whose compiler binary is not on PATH.
func Available(tools map[string]Tool) map[string]Tool {
	out := map[string]Tool{}
	for name, t := range tools {
		if !t.Compiles() {
			out[name] = t
			continue
		}
		if _, err := exec.LookPath(t.Compile[0]); err == nil {
			out[name] = t
		}
	}
	return out
}
