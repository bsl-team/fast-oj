package store

import "context"

const judgeColumns = `id, name, languages, capabilities, note, last_seen_at, jailed, jailed_known`

func scanJudge(row scanner) (Judge, error) {
	var (
		j             Judge
		langs, caps   string
		jailed, known int
	)
	err := row.Scan(&j.ID, &j.Name, &langs, &caps, &j.Note, &j.LastSeenAt, &jailed, &known)
	j.Languages = splitList(langs)
	j.Capabilities = splitList(caps)
	// 两列是分开的两件事：0/0 = "这台机器还没说过"（升级后还没心跳过的老二进制），
	// 0/1 = "它说了开不起来"。把前者显示成后者，就是替一台没开口的机器编一句话。
	// 和 time_min/mem_min 一样先扫成 int：这个库里的布尔都是这么来的，不靠驱动肯不肯转。
	j.Jailed, j.JailedKnown = jailed != 0, known != 0
	return j, notFound(err)
}

// UpsertJudge registers a judge box, or refreshes its advertised toolchains, its
// self-reported capabilities and whether it can put a run inside a jail right now.
//
// jailed 是指针：nil = 这台判机没说过；&true / &false = 它说了有 / 没有。
func (db *DB) UpsertJudge(ctx context.Context, name string, langs, caps []string, note string, jailed *bool) (Judge, error) {
	var jail any = 0
	known := 0
	if jailed != nil {
		known = 1
		if *jailed {
			jail = 1
		}
	}
	_, err := db.SQL.ExecContext(ctx,
		`INSERT INTO judges (name, languages, capabilities, note, last_seen_at, jailed, jailed_known) VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET languages=excluded.languages, capabilities=excluded.capabilities,
		   note=excluded.note, last_seen_at=excluded.last_seen_at, jailed=excluded.jailed,
		   jailed_known=excluded.jailed_known`,
		name, joinList(langs), joinList(caps), note, nowMS(), jail, known)
	if err != nil {
		return Judge{}, err
	}
	return scanJudge(db.SQL.QueryRowContext(ctx, `SELECT `+judgeColumns+` FROM judges WHERE name=? COLLATE NOCASE`, name))
}

func (db *DB) TouchJudge(ctx context.Context, id int64) error {
	res, err := db.SQL.ExecContext(ctx, `UPDATE judges SET last_seen_at=? WHERE id=?`, nowMS(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) ListJudges(ctx context.Context) ([]Judge, error) {
	rows, err := db.SQL.QueryContext(ctx, `SELECT `+judgeColumns+` FROM judges ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Judge
	for rows.Next() {
		j, err := scanJudge(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (db *DB) GetJudge(ctx context.Context, name string) (Judge, error) {
	return scanJudge(db.SQL.QueryRowContext(ctx,
		`SELECT `+judgeColumns+` FROM judges WHERE name=? COLLATE NOCASE`, name))
}
