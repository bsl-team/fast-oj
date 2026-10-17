package store

import (
	"context"
	"database/sql"
	"errors"
)

// The super admin (站主) is one account, not one role. users.role carries a CHECK
// constraint that would take a rebuild of the most-referenced table in the schema to
// extend, and "who owns this site" is a fact about one row rather than a tier.

// OwnerID is the super admin's account id, or 0 while nobody has claimed the site.
func (db *DB) OwnerID(ctx context.Context) (int64, error) {
	var id int64
	err := db.SQL.QueryRowContext(ctx, `SELECT user_id FROM site_owner WHERE id=1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// IsOwner answers the question every privilege-changing screen has to ask before it
// touches an account.
func (db *DB) IsOwner(ctx context.Context, userID int64) (bool, error) {
	id, err := db.OwnerID(ctx)
	if err != nil {
		return false, err
	}
	return id != 0 && id == userID, nil
}

// HasOtherHumanAccount reports whether anybody besides this account (and besides
// the token's own row) has signed up. The registration that would claim the seat
// has already written its own row by the time it asks, so the question has to
// exclude the one account that is asking it.
func (db *DB) HasOtherHumanAccount(ctx context.Context, exceptID int64) (bool, error) {
	var n int
	err := db.SQL.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE id <> ? AND handle <> ?`, exceptID, BootstrapHandle).Scan(&n)
	return n > 0, err
}

// ErrSiteOwner is what the four mutation paths return for the super admin's
// account. It is a stored error rather than a page message because the rule is a
// fact about the data, not a courtesy of one screen.
var ErrSiteOwner = errors.New("超级管理员的权限、角色与状态不可改动")

// guardOwner is the backstop behind every screen that changes an account.
func (db *DB) guardOwner(ctx context.Context, userID int64) error {
	owner, err := db.IsOwner(ctx, userID)
	if err != nil {
		return err
	}
	if owner {
		return ErrSiteOwner
	}
	return nil
}

// ClaimOwner seats userID if the seat is still empty, and reports whether this call
// is what filled it. It never reassigns: whoever arrived first keeps the site.
//
// Sitting in the seat also means being an administrator, so the claim promotes the
// account into that tier in the same transaction — every other admin check in the
// app reads users.role, and an owner who was left in the 'user' tier would be
// refused by those paths while still holding every capability. This is the one
// place a role write bypasses guardOwner: the immunity starts by making this
// account the one nobody else may change.
func (db *DB) ClaimOwner(ctx context.Context, userID int64) (bool, error) {
	claimed := false
	err := db.InTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO site_owner (id, user_id, claimed_at) VALUES (1, ?, ?)
			 ON CONFLICT(id) DO NOTHING`, userID, nowMS())
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET role=? WHERE id=?`, "admin", userID); err != nil {
			return err
		}
		claimed = true
		return nil
	})
	return claimed, err
}
