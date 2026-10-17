package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

// stats.go is the operator's dashboard: how many people, how many submissions, what
// the judge actually said, and how much traffic this process has served. It answers
// "这个站现在怎么样" in one page, and only for someone holding stats.view.

type statsRow struct {
	Label string
	Value string
	Note  string
}

type statsData struct {
	Headline []statsRow
	Verdicts []store.NameCount
	Days     []store.NameCount
	Top      []store.NameCount
	Columns  []statsRow
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	a, ok := s.requirePerm(w, r, permStatsView)
	if !ok {
		return
	}
	ctx := r.Context()
	stats, err := s.db.GlobalStats(ctx)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	verdicts, err := s.db.VerdictCounts(ctx)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	days, err := s.db.SubmitsByDay(ctx, 14)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	top, err := s.db.TopSubmittedProblems(ctx, 10)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}

	uptime := time.Since(s.started).Round(time.Second)
	gets, posts, other := s.hits.get.Load(), s.hits.post.Load(), s.hits.other.Load()
	total := gets + posts + other
	var qps float64
	if secs := uptime.Seconds(); secs > 0 {
		qps = float64(total) / secs
	}

	d := statsData{
		Verdicts: verdicts,
		Days:     days,
		Top:      top,
		Headline: []statsRow{
			{"注册用户", strconv.Itoa(stats.Users), fmt.Sprintf("启用 %d，封禁 %d，管理员 %d", stats.ActiveUsers, stats.BannedUsers, stats.Admins)},
			{"提交次数", strconv.Itoa(stats.Submissions), fmt.Sprintf("其中有 %d 次在比赛里", stats.ContestSubs)},
			{"人均提交", fmt.Sprintf("%.1f", stats.AverageSubmits()), "分母是提交过的人，不是全部注册用户"},
			{"通过率", fmt.Sprintf("%.1f%%", stats.ACRate()), fmt.Sprintf("AC %d 条；判完的才算分母", stats.ACs)},
			{"题目", strconv.Itoa(stats.Problems), fmt.Sprintf("公开 %d，判题用例 %d 条", stats.Visible, stats.Testcases)},
			{"内容", fmt.Sprintf("%d / %d / %d", stats.Posts, stats.Topics, stats.Comments), "动态 / 帖子 / 评论"},
			{"比赛", strconv.Itoa(stats.Contests), fmt.Sprintf("工单 %d 张，未结 %d 张", stats.Tickets, stats.OpenTickets)},
			{"判题队列", strconv.Itoa(stats.Queued), fmt.Sprintf("在线判机 %d 台", stats.JudgesOnline)},
		},
		Columns: []statsRow{
			{"GET 请求", strconv.Itoa(int(gets)), "本次运行以来"},
			{"POST 请求", strconv.Itoa(int(posts)), "本次运行以来"},
			{"其他请求", strconv.Itoa(int(other)), "HEAD、OPTIONS 等"},
			{"平均每秒", fmt.Sprintf("%.2f", qps), fmt.Sprintf("已运行 %s；进程重启后归零", uptime)},
		},
	}
	s.page(w, r, http.StatusOK, "站点统计", "admin_stats", a.viewer(), msgOf(r), d)
}
