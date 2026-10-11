package store

import (
	"context"
	"database/sql"
	"errors"
)

// ErrSelfFollow is refused here as well as in the API: without the check the
// schema's CHECK constraint would surface it as an opaque driver error.
var ErrSelfFollow = errors.New("cannot follow yourself")

// Follow is idempotent. Re-following is not an error, because a browser can
// double-click and a retry after a dropped response should settle quietly.
func (db *DB) Follow(ctx context.Context, followerID, followeeID int64) error {
	if followerID == followeeID {
		return ErrSelfFollow
	}
	var one int
	if err := db.SQL.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id=?`, followeeID).Scan(&one); err != nil {
		return notFound(err)
	}
	_, err := db.SQL.ExecContext(ctx,
		`INSERT INTO follows (follower_id, followee_id, followed_at) VALUES (?, ?, ?)
		 ON CONFLICT(follower_id, followee_id) DO NOTHING`, followerID, followeeID, nowMS())
	return err
}

// Unfollow reports whether a relation was actually removed.
func (db *DB) Unfollow(ctx context.Context, followerID, followeeID int64) (bool, error) {
	res, err := db.SQL.ExecContext(ctx,
		`DELETE FROM follows WHERE follower_id=? AND followee_id=?`, followerID, followeeID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (db *DB) IsFollowing(ctx context.Context, followerID, followeeID int64) (bool, error) {
	var one int
	err := db.SQL.QueryRowContext(ctx,
		`SELECT 1 FROM follows WHERE follower_id=? AND followee_id=?`, followerID, followeeID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// AreMutual is the definition of "not a stranger" that the message policy uses:
// two indexed point lookups, so no reason to fold them into one clever query.
func (db *DB) AreMutual(ctx context.Context, a, b int64) (bool, error) {
	ab, err := db.IsFollowing(ctx, a, b)
	if err != nil || !ab {
		return false, err
	}
	return db.IsFollowing(ctx, b, a)
}

// ListFollowing answers "who does this person read", newest relation first.
func (db *DB) ListFollowing(ctx context.Context, id int64, limit int) ([]User, error) {
	return db.listFollow(ctx, id, limit,
		`SELECT `+userColumns+` FROM follows f JOIN users u ON u.id=f.followee_id
		 WHERE f.follower_id=? ORDER BY f.followed_at DESC, f.followee_id DESC LIMIT ?`)
}

// ListFollowers answers "who reads this person".
func (db *DB) ListFollowers(ctx context.Context, id int64, limit int) ([]User, error) {
	return db.listFollow(ctx, id, limit,
		`SELECT `+userColumns+` FROM follows f JOIN users u ON u.id=f.follower_id
		 WHERE f.followee_id=? ORDER BY f.followed_at DESC, f.follower_id DESC LIMIT ?`)
}

func (db *DB) listFollow(ctx context.Context, id int64, limit int, query string) ([]User, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.SQL.QueryContext(ctx, query, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]User, 0, limit)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// FollowCounts feeds the two numbers on a profile page.
func (db *DB) FollowCounts(ctx context.Context, id int64) (following, followers int, err error) {
	if err = db.SQL.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM follows WHERE follower_id=?`, id).Scan(&following); err != nil {
		return 0, 0, err
	}
	err = db.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM follows WHERE followee_id=?`, id).Scan(&followers)
	return following, followers, err
}
