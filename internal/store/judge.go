package store

import "context"

const judgeColumns = `id, name, languages, capabilities, note, last_seen_at`

func scanJudge(row scanner) (Judge, error) {
	var (
		j            Judge
		langs, caps  string
	)
	err := row.Scan(&j.ID, &j.Name, &langs, &caps, &j.Note, &j.LastSeenAt)
	j.Languages = splitList(langs)
	j.Capabilities = splitList(caps)
	return j, notFound(err)
}

// UpsertJudge registers a judge box, or refreshes its advertised toolchains and
// self-reported capabilities.
func (db *DB) UpsertJudge(ctx context.Context, name string, langs, caps []string, note string) (Judge, error) {
	_, err := db.SQL.ExecContext(ctx,
		`INSERT INTO judges (name, languages, capabilities, note, last_seen_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET languages=excluded.languages, capabilities=excluded.capabilities,
		   note=excluded.note, last_seen_at=excluded.last_seen_at`,
		name, joinList(langs), joinList(caps), note, nowMS())
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
