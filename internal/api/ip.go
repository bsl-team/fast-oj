package api

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

// IP banning and per-address traffic. Two different lifetimes live here on purpose:
// a ban is an admin's decision and is stored in the database, while the counters are
// a measurement that lives in memory — persisting them would mean a write on every
// request, which is exactly the hot path this project keeps short.

// banTTL is how long the matched list may be stale. Bans are rare and small, so a
// short cache keeps the request path free of any database read while a fresh ban
// still lands within a couple of seconds.
const banTTL = 2 * time.Second

type banCache struct {
	mu     sync.RWMutex
	rules  []store.IPBan
	loaded time.Time
}

func (c *banCache) current(db *store.DB) []store.IPBan {
	c.mu.RLock()
	fresh := time.Since(c.loaded) < banTTL
	rules := c.rules
	c.mu.RUnlock()
	if fresh {
		return rules
	}
	list, err := db.ListIPBans(context.Background())
	if err != nil {
		// A database hiccup must not silently open the door: keep what was last good.
		return rules
	}
	c.mu.Lock()
	c.rules, c.loaded = list, time.Now()
	c.mu.Unlock()
	return list
}

func (c *banCache) invalidate() {
	c.mu.Lock()
	c.rules, c.loaded = nil, time.Time{}
	c.mu.Unlock()
}

// match returns the rule that covers addr, if any.
func (s *Server) matchBan(addr netip.Addr) (store.IPBan, bool) {
	for _, b := range s.bans.current(s.db) {
		if b.Contains(addr) {
			return b, true
		}
	}
	return store.IPBan{}, false
}

// clientAddr parses the peer address of a request. It is deliberately RemoteAddr and
// nothing else: X-Forwarded-For is a header any client can invent, so trusting it
// would let a banned host name itself as 8.8.8.8 and simultaneously poison the
// statistics with fake addresses.
func clientAddr(r *http.Request) (netip.Addr, bool) {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	// A zone id ("fe80::1%eth0") is not part of the address for matching purposes.
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

// guardIP blocks banned addresses and feeds the counters. It sits outside the CSRF
// gate so a blocked request is still counted and logged: that pile of refused hits is
// precisely what an operator needs to see before deciding to ban someone.
func (s *Server) guardIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addr, ok := clientAddr(r)
		if !ok {
			// Unparsable peer: nothing in the ban list can match it, so let it through
			// to the normal handlers rather than inventing a rejection here.
			next.ServeHTTP(w, r)
			return
		}
		s.observe(addr, r.Method)
		if b, hit := s.matchBan(addr); hit {
			s.traffic.noteBlocked(addr.String())
			// The matched rule is not echoed to the visitor: telling an attacker which
			// range is blocked teaches them where the edge is. The admin sees it here.
			s.log.Warn("ip blocked", "ip", addr.String(), "rule", b.IP, "path", r.URL.Path)
			s.respondError(w, r, http.StatusForbidden,
				"这个地址已被站方封禁。如果这是误封，请换一台机器上网并联系站主，或让同学替你提交一张工单。")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// observe remembers that a non-loopback visitor showed up, which is what keeps the
// IP page unlocked. The write is throttled to once a minute: the flag only needs to
// be roughly current, and a row per request would defeat the point.
func (s *Server) observe(addr netip.Addr, method string) {
	s.traffic.note(addr.String(), method)
	if addr.IsLoopback() {
		return
	}
	if s.traffic.sawPublicFlag() {
		return
	}
	// 落库每请求最多一次，而且一分钟内只写一次：这一位只要求大致准确。
	now := time.Now()
	s.muPublic.Lock()
	if now.Sub(s.publicSeen) < time.Minute {
		s.muPublic.Unlock()
		return
	}
	s.publicSeen = now
	s.muPublic.Unlock()

	s.traffic.markPublic()
	if err := s.db.MarkPublicClientSeen(context.Background()); err != nil {
		s.log.Error("mark public client", "err", err)
	}
}

// ipFeatureOn reports whether the IP tools are worth showing.
//
// 一台机器上跑着反向代理时，服务器看到的来源地址永远是 127.0.0.1：这时候"按 IP 封禁"
// 既封不掉任何人（所有人都是同一个地址），又随时可能变成自杀——封了这个地址就是封全站。
// 所以 24 小时内只要没出现过非环回的来源，这一页就自动收起，并说明为什么。
//
// The lookup is cached because a loopback-only deployment would otherwise ask the
// database on every page render, forever.
func (s *Server) ipFeatureOn(r *http.Request) bool {
	if s.traffic.sawPublicFlag() {
		return true
	}
	s.muPublic.Lock()
	defer s.muPublic.Unlock()
	if time.Since(s.publicChecked) < 30*time.Second {
		return s.publicOn
	}
	window := int64(24 * time.Hour.Milliseconds())
	on, err := s.db.HasPublicClient(r.Context(), window)
	if err != nil {
		s.log.Error("ip feature gate", "err", err)
		// A failed lookup keeps whatever was last known rather than flickering the
		// page in and out; enforcement itself never depends on this.
		return s.publicOn
	}
	s.publicChecked, s.publicOn = time.Now(), on
	return on
}

// --- the admin page ---

type ipBanRow struct {
	IP        string
	Note      string
	Creator   string
	When      string
	Get       uint64
	Post      uint64
	Other     uint64
	Registers uint64
	Blocked   uint64
}

type ipPageData struct {
	Enabled   bool
	Banned    []ipBanRow
	Traffic   []ipTrafficRow
	Note      string
	Truncated bool
	Reason    string
}

// injectIPGate turns the nav link off for everyone when the address data is useless.
// The capability itself already came from actor.viewer(); this only ANDs the gate onto
// it, so a deployment where every peer is the loopback hides the entry by itself.
func (s *Server) injectIPGate(me *viewer, r *http.Request) {
	if me != nil && me.IPs {
		me.IPs = s.ipFeatureOn(r)
	}
}

func (s *Server) handleIPs(w http.ResponseWriter, r *http.Request) {
	a, ok := s.requirePerm(w, r, permIPManage)
	if !ok {
		return
	}
	on := s.ipFeatureOn(r)
	list, err := s.db.ListIPBans(r.Context())
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	d := ipPageData{Enabled: on}
	counts := s.traffic.snapshot()
	for _, b := range list {
		row := ipBanRow{IP: b.IP, Note: b.Note, Creator: b.CreatorTag, When: agoMS(b.CreatedAt)}
		// A prefix covers many addresses, so the numbers shown next to it are the sum
		// of every tracked address inside it — otherwise a /24 ban looks inactive while
		// the traffic table below keeps counting.
		if c, ok := counts[b.IP]; ok {
			row.Get, row.Post, row.Other = c.get, c.post, c.other
			row.Registers, row.Blocked = c.registers, c.blocked
		} else if p := b.Prefix(); p.IsValid() {
			for ip, c := range counts {
				if a, err := netip.ParseAddr(ip); err == nil && p.Contains(a) {
					row.Get += c.get
					row.Post += c.post
					row.Other += c.other
					row.Registers += c.registers
					row.Blocked += c.blocked
				}
			}
		}
		d.Banned = append(d.Banned, row)
	}
	d.Traffic, d.Truncated = s.traffic.top(20)
	s.page(w, r, http.StatusOK, "IP 封禁与流量", "admin_ips", a.viewer(), msgOf(r), d)
}

func (s *Server) handleIPBanAdd(w http.ResponseWriter, r *http.Request) {
	a, ok := s.requirePerm(w, r, permIPManage)
	if !ok {
		return
	}
	back := "/admin/ips"
	raw := strings.TrimSpace(r.FormValue("ip"))
	ban, err := s.db.BanIP(r.Context(), raw, r.FormValue("note"), a.User.ID)
	switch {
	case err == store.ErrNotAnAddress:
		s.redirectMsg(w, r, back, "写错了："+raw+" 不是 IP，也不是 CIDR 网段。要写成 203.0.113.9 或 203.0.113.0/24。")
		return
	case err == store.ErrLoopbackBanned:
		s.redirectMsg(w, r, back, "环回地址不能封：那等于把整站封掉，包括你自己的管理入口。")
		return
	case err == store.ErrBanEverything:
		s.redirectMsg(w, r, back, "0.0.0.0/0 与 ::/0 封的是全世界，包括按这个按钮的你："+
			"封完之后这一页自己也打不开，谁都没法再解封。要停站请用站点开关。")
		return
	case err != nil:
		s.writeStoreError(w, r, err)
		return
	}
	s.bans.invalidate()
	s.log.Info("ip banned", "by", a.User.Handle, "rule", ban.IP, "note", ban.Note)
	s.redirectMsg(w, r, back, "已封禁 "+ban.IP+"：这个地址之后的所有请求都会被 403。")
}

func (s *Server) handleIPBanRemove(w http.ResponseWriter, r *http.Request) {
	a, ok := s.requirePerm(w, r, permIPManage)
	if !ok {
		return
	}
	back := "/admin/ips"
	removed, err := s.db.UnbanIP(r.Context(), r.PathValue("ip"))
	if err != nil {
		if err == store.ErrNotAnAddress {
			s.redirectMsg(w, r, back, "那个封禁记录写法不对，已经没法匹配了：请直接在列表里删掉它。")
			return
		}
		s.writeStoreError(w, r, err)
		return
	}
	s.bans.invalidate()
	msg := "没有这条封禁了（可能已经被删过）。"
	if removed {
		msg = "已解封 " + r.PathValue("ip") + "。"
		s.log.Info("ip unbanned", "by", a.User.Handle, "rule", r.PathValue("ip"))
	}
	s.redirectMsg(w, r, back, msg)
}

// redirectMsg is the one way this page reports back: a redirect plus a flash, so a
// refresh does not replay the write.
func (s *Server) redirectMsg(w http.ResponseWriter, r *http.Request, path, msg string) {
	http.Redirect(w, r, path+"?msg="+url.QueryEscape(msg), http.StatusSeeOther)
}
