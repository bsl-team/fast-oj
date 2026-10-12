package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

// Messages are rate limited per sender, not per conversation: the point is to make
// flooding expensive, and a spammer opens as many threads as needed. The throttle
// table counts attempts inside a window, so "one per two seconds" is a one-second
// window with a single allowance.
const (
	dmWindow     = 2 * time.Second
	maxDMsWindow = 1
)

// requireMember is the page-level version of requireSignedIn: it sends the visitor
// to the login page with the address they wanted, instead of the settings page.
func (s *Server) requireMember(w http.ResponseWriter, r *http.Request) (actor, bool) {
	a, err := s.authenticate(r)
	if err == nil && a.User.ID != 0 {
		return a, true
	}
	http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.Path), http.StatusSeeOther)
	return actor{}, false
}

// threadRow and messageRow add the human-readable timestamp to what the store
// returns, so the templates never do arithmetic on epoch milliseconds.
type threadRow struct {
	store.DMThread
	When string
}

type messageRow struct {
	store.DM
	When string
	Read bool
}

// handleInbox lists conversations, newest activity first.
func (s *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	a, ok := s.requireMember(w, r)
	if !ok {
		return
	}
	threads, err := s.db.ListDMThreads(r.Context(), a.User.ID, 50)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if !wantsHTML(r) {
		var b strings.Builder
		b.WriteString("peer        unread  last\n")
		for _, t := range threads {
			fmt.Fprintf(&b, "%-12s %-6d %s\n", t.Peer, t.Unread, clip(t.Last, 40))
		}
		fmt.Fprintf(&b, "shown=%d\n", len(threads))
		respond(w, r, http.StatusOK, b.String(), threads)
		return
	}
	rows := make([]threadRow, 0, len(threads))
	for _, t := range threads {
		rows = append(rows, threadRow{DMThread: t, When: agoMS(t.LastAt)})
	}
	s.page(w, r, http.StatusOK, "私信", "messages", a.viewer(), msgOf(r), map[string]any{
		"Threads": rows,
	})
}

// handleThread shows one conversation and marks the other side's messages read.
func (s *Server) handleThread(w http.ResponseWriter, r *http.Request) {
	a, ok := s.requireMember(w, r)
	if !ok {
		return
	}
	peer, err := s.db.GetUserByHandle(r.Context(), r.PathValue("handle"))
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	messages, err := s.db.ListDMConversation(r.Context(), a.User.ID, peer.ID, 100)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	// Opening the conversation is the read receipt: the badge drops as soon as the
	// messages are on screen, not when they are dismissed.
	if _, err := s.db.MarkDMRead(r.Context(), a.User.ID, peer.ID); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	for i := range messages {
		messages[i].FromMe = messages[i].SenderID == a.User.ID
	}
	if !wantsHTML(r) {
		var b strings.Builder
		fmt.Fprintf(&b, "%s ↔ %s\n", a.User.Handle, peer.Handle)
		for _, m := range messages {
			fmt.Fprintf(&b, "%-12s %s\n", m.Sender, clip(m.Body, 60))
		}
		fmt.Fprintf(&b, "shown=%d\n", len(messages))
		respond(w, r, http.StatusOK, b.String(), messages)
		return
	}

	may, err := s.db.MayMessage(r.Context(), a.User.ID, peer.ID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	rows := make([]messageRow, 0, len(messages))
	for _, m := range messages {
		rows = append(rows, messageRow{DM: m, When: agoMS(m.CreatedAt), Read: m.ReadAt != 0})
	}
	s.page(w, r, http.StatusOK, peer.Handle, "message_thread", a.viewer(), msgOf(r), map[string]any{
		"Peer":     peer.Handle,
		"PeerName": nameOf(peer),
		"Messages": rows,
		"CanSend":  may && peer.ID != a.User.ID,
		"MaxBytes": store.MaxDMBody,
		"RateNote": "每 2 秒最多 1 条",
		"Inbox":    "/messages",
	})
}

// handleSend is the only way a message is created, so the recipient's policy and
// the sender's rate limit are checked in exactly one place.
func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	a, err := s.authenticate(r)
	if err != nil || a.User.ID == 0 {
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.Path), http.StatusSeeOther)
		return
	}
	peer, err := s.db.GetUserByHandle(r.Context(), r.PathValue("handle"))
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	back := "/messages/" + url.PathEscape(peer.Handle)
	if peer.ID == a.User.ID {
		s.replyBack(w, r, back, "不能给自己发私信")
		return
	}

	may, err := s.db.MayMessage(r.Context(), a.User.ID, peer.ID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if !may {
		s.replyBack(w, r, back, "对方的隐私设置不接受这条私信")
		return
	}

	key := fmt.Sprintf("dm:%d", a.User.ID)
	allowed, err := s.db.LoginAllowed(r.Context(), key, dmWindow, maxDMsWindow)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if !allowed {
		s.replyBack(w, r, back, "发得太快了，每 2 秒最多一条")
		return
	}

	if _, err := s.db.SendDM(r.Context(), a.User.ID, peer.ID, r.FormValue("body")); err != nil {
		if errors.Is(err, store.ErrDMEmpty) {
			s.replyBack(w, r, back, "写点什么再发")
			return
		}
		if errors.Is(err, store.ErrDMTooLong) {
			s.replyBack(w, r, back, "太长了，每条私信最多 "+fmt.Sprint(store.MaxDMBody)+" 字")
			return
		}
		s.replyBack(w, r, back, "发送失败")
		return
	}
	if _, err := s.db.RegisterLoginFailure(r.Context(), key, dmWindow); err != nil {
		s.log.Error("dm counter", "err", err)
	}
	s.log.Info("dm sent", "by", a.User.Handle, "to", peer.Handle)
	http.Redirect(w, r, back+"?msg="+url.QueryEscape("已发送"), http.StatusSeeOther)
}

// replyBack keeps a refused action on the page the person was already reading.
func (s *Server) replyBack(w http.ResponseWriter, r *http.Request, back, msg string) {
	http.Redirect(w, r, back+"?msg="+url.QueryEscape(msg), http.StatusSeeOther)
}

// handlePrivacy stores who may send this person a message.
func (s *Server) handlePrivacy(w http.ResponseWriter, r *http.Request) {
	a, err := s.requireSignedIn(w, r)
	if err != nil {
		return
	}
	policy := r.FormValue("dm_policy")
	if !store.ValidDMPolicy(policy) {
		data, err := s.buildSettings(r, a, "")
		if err != nil {
			s.writeStoreError(w, r, err)
			return
		}
		s.page(w, r, http.StatusBadRequest, "设置", "settings", a.viewer(), "不认识的消息策略", data)
		return
	}
	if err := s.db.SetDMPolicy(r.Context(), a.User.ID, policy); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings?msg="+url.QueryEscape("已保存隐私设置"), http.StatusSeeOther)
}
