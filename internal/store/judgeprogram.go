package store

import (
	"context"
	"fmt"
	"strings"
)

// 题目自带的判机程序：SPJ 判题器与交互题的交互器。
//
// 两类共用一套存、取、删的代码，因为对判机来说它们是同一件事——"编译这份源码，然后按
// 退出码说话"。区别只在跑的时机：判题器在学生程序跑完之后拿三个文件判定，交互器与学生
// 程序同时跑、互相通过标准输入输出对话。

const (
	ProgramChecker    = "checker"
	ProgramInteractor = "interactor"
)

// ValidProgramKind 是 kind 参数的白名单。写成函数而不是靠表上的 CHECK：入口先挡住才能
// 给出人话错误，而不是让 SQLite 的约束文本冒出来。
func ValidProgramKind(kind string) bool {
	return kind == ProgramChecker || kind == ProgramInteractor
}

// MaxProgramSource 是判题器源码的上限。它比学生提交宽一些（判题器要自带读入与比对逻辑，
// 百来行很正常），但仍然必须有上限：这一份是每次领取工单都要过一遍网络的文本。
const MaxProgramSource = 256 << 10

type JudgeProgram struct {
	ProblemID int64  `json:"problem_id"`
	Kind      string `json:"kind"`
	Language  string `json:"language"`
	Source    string `json:"source"`
	UpdatedAt int64  `json:"updated_at"`
}

func (db *DB) GetJudgeProgram(ctx context.Context, problemID int64, kind string) (JudgeProgram, error) {
	var p JudgeProgram
	err := db.SQL.QueryRowContext(ctx,
		`SELECT problem_id, kind, language, source, updated_at FROM judge_programs
		 WHERE problem_id=? AND kind=?`, problemID, kind).
		Scan(&p.ProblemID, &p.Kind, &p.Language, &p.Source, &p.UpdatedAt)
	return p, notFound(err)
}

// PutJudgeProgram 写入或替换一份判机程序。空源码等于撤掉：这一条让 UI 可以用同一个表单
// 表达"这题不用 SPJ 了"，不必再多一个按钮和一套权限判断。
func (db *DB) PutJudgeProgram(ctx context.Context, p JudgeProgram) error {
	if !ValidProgramKind(p.Kind) {
		return fmt.Errorf("不认识的判机程序种类：%q", p.Kind)
	}
	if len(p.Source) > MaxProgramSource {
		return fmt.Errorf("判题器源码太长（上限 %d 字节）", MaxProgramSource)
	}
	if strings.TrimSpace(p.Source) == "" {
		_, err := db.SQL.ExecContext(ctx,
			`DELETE FROM judge_programs WHERE problem_id=? AND kind=?`, p.ProblemID, p.Kind)
		return err
	}
	lang := strings.ToLower(strings.TrimSpace(p.Language))
	if lang == "" {
		lang = "cpp"
	}
	_, err := db.SQL.ExecContext(ctx,
		`INSERT INTO judge_programs (problem_id, kind, language, source, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(problem_id, kind) DO UPDATE SET language=excluded.language,
		     source=excluded.source, updated_at=excluded.updated_at`,
		p.ProblemID, p.Kind, lang, p.Source, nowMS())
	return err
}

// JudgePrograms 按题目取出全部判机程序，供领取工单时一次装进任务里。
// 没有配程序的题目返回空 map，调用方不必判空。
func (db *DB) JudgePrograms(ctx context.Context, problemID int64) (map[string]JudgeProgram, error) {
	rows, err := db.SQL.QueryContext(ctx,
		`SELECT problem_id, kind, language, source, updated_at FROM judge_programs WHERE problem_id=?`,
		problemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]JudgeProgram{}
	for rows.Next() {
		var p JudgeProgram
		if err := rows.Scan(&p.ProblemID, &p.Kind, &p.Language, &p.Source, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out[p.Kind] = p
	}
	return out, rows.Err()
}
