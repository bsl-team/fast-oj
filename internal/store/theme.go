package store

import (
	"context"
	"strings"
)

// theme.go 是"每个人自己的配色"的唯一入口：一串 key=value 文本 <-> 一个只认白名单的结构。
//
// 这一串会被拼进 <html style="…">，那是 CSS 上下文：一个 `}`、一条 `background:url(...)`
// 就能在页面里改写新规则，而这段文本的来路里有用户能提交的表单。所以两头都收口——写入前
// 校验、渲染前再校验一次，认不出的键或值一律整条丢掉。宁可让一个人的自定义静默失效，也不能
// 让页面里出现别人写进来的样式。

// ThemeKeys 是允许各人自己调的那九个变量，顺序就是渲染与写库的顺序（固定顺序才让"同一个人
// 存两次"产生的字符串可比，也让 style 里的顺序稳定）。键名对应 style.css 的 :root 变量。
var ThemeKeys = []string{"fg", "muted", "line", "bg", "code", "accent", "ok", "bad", "warn"}

// ThemeModes 是明暗底色的三种选择：空是跟着系统，另外两个盖过系统。
var ThemeModes = []string{"", "light", "dark"}

// Theme 是一个人存下来的配色。Colors 里只会出现 ThemeKeys 中的键——这个不变量由 ParseTheme
// 和 Set 共同守住，所以渲染侧可以直接遍历。
type Theme struct {
	Mode   string
	Colors map[string]string
}

// IsEmpty 报告这个人其实什么都没调。空串走的是最便宜的那条渲染路径：既不写 data-theme，
// 也不写 style，页面跟没这个功能之前一模一样。
func (t Theme) IsEmpty() bool {
	return t.Mode == "" && len(t.Colors) == 0
}

// ParseTheme 读库里那串文本。**不合法的部件被丢掉而不是让整次解析失败**：这一列可能被人
// 用 SQL 手改过，而"因为一行脏数据就渲染不出整页"是比丢一个颜色糟得多的后果。
func ParseTheme(raw string) Theme {
	t := Theme{Colors: map[string]string{}}
	for _, part := range strings.Split(raw, ";") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "mode" {
			for _, m := range ThemeModes {
				if m == v {
					t.Mode = v
					break
				}
			}
			continue
		}
		if !themeKeyKnown(k) {
			continue
		}
		if hex, ok := normalizeHex(v); ok {
			t.Colors[k] = hex
		}
	}
	return t
}

// String 写回库里的形式。空的 Theme 写回空串（而不是 ";"）。
func (t Theme) String() string {
	var b strings.Builder
	if t.Mode != "" {
		b.WriteString("mode=" + t.Mode)
	}
	for _, k := range ThemeKeys {
		if v, ok := t.Colors[k]; ok {
			if b.Len() > 0 {
				b.WriteByte(';')
			}
			b.WriteString(k + "=" + v)
		}
	}
	return b.String()
}

// Style 是 <html style="…"> 里那串自定义属性。只有已经认下来的键值能进来，顺序固定。
func (t Theme) Style() string {
	var b strings.Builder
	for _, k := range ThemeKeys {
		if v, ok := t.Colors[k]; ok {
			if b.Len() > 0 {
				b.WriteByte(';')
			}
			b.WriteString("--" + k + ":" + v)
		}
	}
	return b.String()
}

// Get 取一个人当前存着的色值，没调过的返回 ""（设置页拿它决定取色器显示哪个初值）。
func (t Theme) Get(key string) string { return t.Colors[key] }

// Set 记下一个人的选择：认不出的键或色值直接忽略，所以这里不能返回错误——表单里九个取色器
// 有一个被浏览器塞了怪东西，代价不该是整张表保存失败。
func (t *Theme) Set(key, value string) {
	value = strings.TrimSpace(value)
	if key == "mode" {
		for _, m := range ThemeModes {
			if m == value {
				t.Mode = value
				return
			}
		}
		t.Mode = ""
		return
	}
	if !themeKeyKnown(key) {
		return
	}
	if value == "" {
		delete(t.Colors, key)
		return
	}
	if hex, ok := normalizeHex(value); ok {
		if t.Colors == nil {
			t.Colors = map[string]string{}
		}
		t.Colors[key] = hex
		return
	}
	delete(t.Colors, key)
}

func themeKeyKnown(key string) bool {
	for _, k := range ThemeKeys {
		if k == key {
			return true
		}
	}
	return false
}

// normalizeHex 只收 `#rgb` 与 `#rrggbb`，一律摊成小写六位。别的（带空格、带分号、带括号、
// 函数式写法）一律不要——这一条就是"用户能不能往 CSS 里塞东西"的分界线。
func normalizeHex(v string) (string, bool) {
	if len(v) == 0 || v[0] != '#' {
		return "", false
	}
	digits := v[1:]
	out := make([]byte, 0, 6)
	switch len(digits) {
	case 3:
		// #abc -> #aabbcc：三位的简写也是合法 CSS，收进来再摊平。
		for i := 0; i < 3; i++ {
			c, ok := hexDigit(digits[i])
			if !ok {
				return "", false
			}
			out = append(out, c, c)
		}
	case 6:
		for i := 0; i < 6; i++ {
			c, ok := hexDigit(digits[i])
			if !ok {
				return "", false
			}
			out = append(out, c)
		}
	default:
		return "", false
	}
	return "#" + string(out), true
}

func hexDigit(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c, true
	case c >= 'a' && c <= 'f':
		return c, true
	case c >= 'A' && c <= 'F':
		return c + 32, true
	}
	return 0, false
}

// UpdateTheme 存下一个人的配色。**只有已经校验过的值会进库**：这里再走一遍 ParseTheme，
// 让"写进去的一定是安全的"这件事不依赖调用方。
func (db *DB) UpdateTheme(ctx context.Context, userID int64, t Theme) error {
	return db.setTheme(ctx, userID, ParseTheme(t.String()).String())
}

func (db *DB) setTheme(ctx context.Context, userID int64, raw string) error {
	_, err := db.SQL.ExecContext(ctx, `UPDATE users SET theme=? WHERE id=?`, raw, userID)
	return err
}
