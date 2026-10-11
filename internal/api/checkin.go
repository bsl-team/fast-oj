package api

import (
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// handleCheckIn records today's check-in. The streak rule (missing n days costs
// 3^n) lives in the store; this handler only says what happened.
func (s *Server) handleCheckIn(w http.ResponseWriter, r *http.Request) {
	a, err := s.authenticate(r)
	if err != nil || a.User.ID == 0 {
		http.Redirect(w, r, "/login?next=/", http.StatusSeeOther)
		return
	}
	streak, added, err := s.db.CheckIn(r.Context(), a.User.ID, time.Now())
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	msg := "您已连续打卡 " + strconv.Itoa(streak) + " 天"
	if !added {
		msg = "今天已经打过卡了，连续 " + strconv.Itoa(streak) + " 天"
	}
	s.log.Info("check-in", "by", a.User.Handle, "streak", streak, "added", added)
	http.Redirect(w, r, "/?msg="+url.QueryEscape(msg), http.StatusSeeOther)
}

// checkInView is the home-page widget: the button, or the fact that today is done.
type checkInView struct {
	Streak int
	Total  int
	Today  bool
}

func (s *Server) checkInFor(r *http.Request, a actor) (checkInView, error) {
	if a.User.ID == 0 {
		return checkInView{}, nil
	}
	st, err := s.db.CheckInState(r.Context(), a.User.ID, time.Now())
	if err != nil {
		return checkInView{}, err
	}
	return checkInView{Streak: st.Streak, Total: st.Total, Today: st.Today}, nil
}
