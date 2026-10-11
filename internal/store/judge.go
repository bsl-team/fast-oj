package store

import "context"

const judgeColumns = `id, name, languages, note, last_seen_at`

func scanJudge(row scanner) (Judge, error) {
	var (
		j     Judge
		langs string
	)
	err := row.Scan(&j.ID, &j.Name, &langs, &j.Note, &j.LastSeenAt)
	j.Languages = splitList(langs)
	return j, notFound(err)
}

// UpsertJudge registers a judge box, or refreshes its advertised toolchains.
func (db *DB) UpsertJudge(ctx context.Context, name string, langs []string, note string) (Judge, error) {
	_, err := db.SQL.ExecContext(ctx,
		`INSERT INTO judges (name, languages, note, last_seen_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET languages=excluded.languages, note=excluded.note, last_seen_at=excluded.last_seen_at`,
		name, joinList(langs), note, nowMS())
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
