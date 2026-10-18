package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

// IP banning. An entry is either a single address or a prefix, so a school can shut
// down one machine or a whole lab range.

// ErrNotAnAddress refuses anything that is not a literal IP or CIDR block. Hostnames
// are not accepted: banning by name would need a resolver on the request path.
var ErrNotAnAddress = errors.New("要写成 IP 或 CIDR 网段，例如 203.0.113.9 或 203.0.113.0/24")

// ErrLoopbackBanned refuses the ban that would lock the whole site out: on a machine
// where everything arrives from the loopback address (a reverse proxy on the same
// host, or a purely local deployment) banning 127.0.0.1 bans every visitor.
var ErrLoopbackBanned = errors.New("不能封环回地址（127.0.0.1 / ::1）：那等于把整站封掉")

// IPBan is one row of the ban list, already parsed so matching never re-parses text
// on the request path.
type IPBan struct {
	IP         string `json:"ip"`
	Note       string `json:"note"`
	CreatedAt  int64  `json:"created_at"`
	CreatedBy  int64  `json:"created_by"`
	CreatorTag string `json:"creator_tag"`

	prefix netip.Prefix
}

// Prefix is the parsed rule, for callers that want to match a set of addresses
// (the admin page uses it to total up traffic behind one CIDR ban).
func (b IPBan) Prefix() netip.Prefix { return b.prefix }

// Contains reports whether a client address falls inside this entry.
func (b IPBan) Contains(addr netip.Addr) bool {
	if !b.prefix.IsValid() {
		return false
	}
	// An IPv4 address never matches an IPv6 prefix and vice versa, except that Go
	// normalises 4-in-6; Unmap keeps a ::ffff:1.2.3.4 client from hitting a v6 rule.
	return b.prefix.Contains(addr.Unmap())
}

// NormalizeIPRule validates and canonically formats an address or prefix, so
// "203.0.113.009" and "203.0.113.9" cannot become two separate rows for one host.
func NormalizeIPRule(raw string) (string, netip.Prefix, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", netip.Prefix{}, ErrNotAnAddress
	}
	if strings.ContainsRune(raw, '/') {
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			return "", netip.Prefix{}, ErrNotAnAddress
		}
		if p.Addr().IsLoopback() {
			return "", netip.Prefix{}, ErrLoopbackBanned
		}
		return p.Masked().String(), p.Masked(), nil
	}
	a, err := netip.ParseAddr(raw)
	if err != nil {
		return "", netip.Prefix{}, ErrNotAnAddress
	}
	if a.IsLoopback() {
		return "", netip.Prefix{}, ErrLoopbackBanned
	}
	// A bare v4-mapped-v6 address ("::ffff:1.2.3.4") is stored as the v4 it is.
	a = a.Unmap()
	p := netip.PrefixFrom(a, a.BitLen())
	return a.String(), p, nil
}

// BanIP adds or refreshes an entry. Re-banning the same address updates the note and
// the timestamp instead of failing, because the admin's intent is "keep this out".
func (db *DB) BanIP(ctx context.Context, raw, note string, byUserID int64) (IPBan, error) {
	key, _, err := NormalizeIPRule(raw)
	if err != nil {
		return IPBan{}, err
	}
	note = strings.TrimSpace(note)
	if len(note) > 200 {
		note = note[:200]
	}
	_, err = db.SQL.ExecContext(ctx,
		`INSERT INTO ip_bans (ip, note, created_at, created_by) VALUES (?, ?, ?, ?)
		 ON CONFLICT(ip) DO UPDATE SET note=excluded.note, created_at=excluded.created_at,
		                                   created_by=excluded.created_by`,
		key, note, nowMS(), nullInt64(byUserID))
	if err != nil {
		return IPBan{}, err
	}
	return db.GetIPBan(ctx, key)
}

// UnbanIP reports whether an entry was actually removed.
func (db *DB) UnbanIP(ctx context.Context, raw string) (bool, error) {
	key, _, err := NormalizeIPRule(raw)
	if err != nil {
		return false, err
	}
	res, err := db.SQL.ExecContext(ctx, `DELETE FROM ip_bans WHERE ip=?`, key)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (db *DB) GetIPBan(ctx context.Context, key string) (IPBan, error) {
	// banColumns is only the column list (the list query prefixes its own SELECT), so
	// this call site has to bring it.
	return scanBan(db.SQL.QueryRowContext(ctx, `SELECT `+banColumns+
		" FROM ip_bans b LEFT JOIN users u ON u.id=b.created_by WHERE b.ip=?", key))
}

const banColumns = `b.ip, b.note, b.created_at, IFNULL(b.created_by, 0), IFNULL(u.handle, '')`

func scanBan(row scanner) (IPBan, error) {
	var (
		b   IPBan
		err error
	)
	if err = row.Scan(&b.IP, &b.Note, &b.CreatedAt, &b.CreatedBy, &b.CreatorTag); err != nil {
		return b, notFound(err)
	}
	_, b.prefix, err = NormalizeIPRule(b.IP)
	if err != nil {
		return b, fmt.Errorf("stored ip rule %q: %w", b.IP, err)
	}
	return b, nil
}

// ListIPBans is newest first: the thing you just did is the thing you want to see.
func (db *DB) ListIPBans(ctx context.Context) ([]IPBan, error) {
	rows, err := db.SQL.QueryContext(ctx, `SELECT `+banColumns+`
		 FROM ip_bans b LEFT JOIN users u ON u.id=b.created_by
		 ORDER BY b.created_at DESC, b.ip`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []IPBan
	for rows.Next() {
		b, err := scanBan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// MarkPublicClientSeen records that a request arrived from a non-loopback address.
// The IP features hide themselves when nothing but loopback has been seen for a day,
// so this is the one fact that has to outlive a restart — stored as a single
// settings row and written at most once a minute by the caller.
func (db *DB) MarkPublicClientSeen(ctx context.Context) error {
	_, err := db.SQL.ExecContext(ctx,
		`INSERT INTO site_settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		setPublicClientSeen, "1", nowMS())
	return err
}

// HasPublicClient answers whether a real visitor has shown up within the window.
// Absence of the row, or a stamp older than the window, means the server has only
// ever seen itself — behind a same-host proxy, or in local use — where IP banning
// would either do nothing or shut the whole site.
func (db *DB) HasPublicClient(ctx context.Context, window int64) (bool, error) {
	var at int64
	err := db.SQL.QueryRowContext(ctx,
		`SELECT updated_at FROM site_settings WHERE key=?`, setPublicClientSeen).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return nowMS()-at <= window, nil
}
