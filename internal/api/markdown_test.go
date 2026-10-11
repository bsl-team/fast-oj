package api

import (
	"strings"
	"testing"
)

const statementSource = `# 题目描述

输入两个整数 $a, b$，输出 $a+b$。平均值写成块级公式：

$$ \frac{a+b}{2} \times x_1 $$

## 输入格式

` + "```" + `cpp
int a, b;
std::cin >> a >> b;
` + "```" + `

## 数据范围

| 档 | a 的范围 | 分值 |
|----|----------|------|
| 1  | 0..10    | 40   |
| 2  | 0..1e9   | 60   |

- 行内代码用 ` + "`std::cin`" + ` 读入
- 链接：[官网](https://example.com/oj)
`

func dangerousStatement() string {
	return "裸 HTML：<script>alert(1)</script>\n\n" +
		"图片事件：<img src=x onerror=\"alert(2)\">\n\n" +
		"伪协议：[点我](javascript:alert(3))\n\n" +
		"数据协议：[文档](data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==)\n"
}

func TestRenderMarkdownKeepsOJFeatures(t *testing.T) {
	out := string(renderMarkdownHTML(statementSource))
	// The MathJax extension turns $...$ / $$...$$ into these spans with the TeX
	// kept inside as \( \) / \[ \]. That is the contract the KaTeX bootstrap in
	// the page template relies on, so assert it here rather than scanning text.
	for _, want := range []string{
		"<h1>题目描述</h1>", "<table>", "<th>分值</th>",
		`<pre><code class="language-cpp">`,
		`<span class="math inline">\(a, b\)</span>`,
		`<span class="math display">\[ \frac{a+b}{2} \times x_1 \]</span>`,
		`<a href="https://example.com/oj" target="_blank" rel="nofollow">`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in rendered statement:\n%s", want, out)
		}
	}
}

func TestRenderMarkdownRefusesInjectedHTML(t *testing.T) {
	out := string(renderMarkdownHTML(dangerousStatement()))
	for _, forbidden := range []string{"<script", "</script>", "onerror", "javascript:", "data:text/html"} {
		if strings.Contains(strings.ToLower(out), forbidden) {
			t.Errorf("leaked %q:\n%s", forbidden, out)
		}
	}
	// The text is still shown, just not as live markup.
	if !strings.Contains(out, "alert") {
		t.Errorf("content was dropped instead of escaped:\n%s", out)
	}
}

func TestRenderMarkdownEmptyIsNothing(t *testing.T) {
	if got := renderMarkdownHTML("   \n\t"); got != "" {
		t.Fatalf("blank statement rendered %q", got)
	}
	if got := renderMarkdownHTML(""); got != "" {
		t.Fatalf("nil statement rendered %q", got)
	}
}

func TestSafeURLPolicy(t *testing.T) {
	for _, bad := range []string{"javascript:alert(1)", "JavaScript:x", " data:text/html,x", "vbscript:y", "file:///c:/x", "about:blank", "blob:https://x/1"} {
		if safeURL([]byte(bad)) {
			t.Errorf("%s must be refused", bad)
		}
	}
	for _, good := range []string{"https://example.com", "http://a.b/c", "/relative/path", "./sibling", "#anchor", "mailto:a@b.c"} {
		if !safeURL([]byte(good)) {
			t.Errorf("%s must be allowed", good)
		}
	}
}
