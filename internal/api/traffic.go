package api

import (
	"sort"
	"sync"
)

// Per-address traffic. Everything here is in memory by choice: the counters are a
// measurement, and one write per request to keep them would cost more than answering
// the request does. The numbers therefore mean "since this process started", the same
// way the request totals on the statistics page do.

// maxTrackedIPs bounds memory. Without a cap, a botnet rotating source addresses would
// grow this map until the process died — which turns a statistics feature into the
// attack itself. Once the table is full, new addresses stop being recorded (and the
// page says so) while the ones already tracked keep counting.
const maxTrackedIPs = 5000

type ipTrafficRow struct {
	IP        string
	Get       uint64
	Post      uint64
	Other     uint64
	Registers uint64
	Blocked   uint64
	Tracked   bool
}

type ipCounts struct {
	get, post, other, registers, blocked uint64
}

type trafficTable struct {
	mu   sync.Mutex
	rows map[string]*ipCounts
	// full records that the cap was hit, so the page can say the list is truncated
	// rather than quietly looking short.
	full bool
	// public is set the first time a non-loopback client arrives during this run.
	public bool
}

func (t *trafficTable) note(ip, method string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.rows == nil {
		t.rows = map[string]*ipCounts{}
	}
	c := t.rows[ip]
	if c == nil {
		if len(t.rows) >= maxTrackedIPs {
			t.full = true
			return
		}
		c = &ipCounts{}
		t.rows[ip] = c
	}
	switch method {
	case "GET":
		c.get++
	case "POST":
		c.post++
	default:
		c.other++
	}
}

// noteRegister counts a completed self-service sign-up, which is the number that
// actually matters when deciding whether an address is a spammer.
func (t *trafficTable) noteRegister(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.rows == nil {
		t.rows = map[string]*ipCounts{}
	}
	if c := t.rows[ip]; c != nil {
		c.registers++
		return
	}
	if len(t.rows) >= maxTrackedIPs {
		t.full = true
		return
	}
	t.rows[ip] = &ipCounts{registers: 1}
}

func (t *trafficTable) noteBlocked(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.rows == nil {
		t.rows = map[string]*ipCounts{}
	}
	if c := t.rows[ip]; c != nil {
		c.blocked++
		return
	}
	if len(t.rows) >= maxTrackedIPs {
		t.full = true
		return
	}
	t.rows[ip] = &ipCounts{blocked: 1}
}

// markPublic notes that a real visitor (not this machine) reached the server.
// markPublic notes that a real visitor (not this machine) reached the server.
func (t *trafficTable) markPublic() {
	t.mu.Lock()
	t.public = true
	t.mu.Unlock()
}

// sawPublicFlag is the in-memory half of the gate; the database row is the half
// that survives a restart.
func (t *trafficTable) sawPublicFlag() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.public
}

// snapshot is the whole table, for callers that need to sum a range rather than see
// the top of it. It copies because the caller reads it while requests keep arriving.
func (t *trafficTable) snapshot() map[string]ipCounts {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]ipCounts, len(t.rows))
	for ip, c := range t.rows {
		out[ip] = *c
	}
	return out
}

// top ranks addresses by total hits. Ties break on the address so the table does not
// reorder itself between two refreshes of the same page.
func (t *trafficTable) top(n int) ([]ipTrafficRow, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	all := make([]ipTrafficRow, 0, len(t.rows))
	for ip, c := range t.rows {
		all = append(all, ipTrafficRow{
			IP: ip, Get: c.get, Post: c.post, Other: c.other,
			Registers: c.registers, Blocked: c.blocked,
		})
	}
	sort.Slice(all, func(i, j int) bool {
		si, sj := all[i].Get+all[i].Post+all[i].Other, all[j].Get+all[j].Post+all[j].Other
		if si != sj {
			return si > sj
		}
		return all[i].IP < all[j].IP
	})
	if len(all) > n {
		all = all[:n]
	}
	return all, t.full
}
