package api

import (
	"net/http"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

// Capability names. They are stored in the database, so changing one is a data
// migration, not a code edit that leaves stale rows behind.
const (
	permProblemCreate     = "problem.create"
	permProblemEditAny    = "problem.edit.any"
	permProblemPublish    = "problem.publish"
	permTestcaseManage    = "testcase.manage"
	permContestCreate     = "contest.create"
	permContestManageAny  = "contest.manage.any"
	permUserManage        = "user.manage"
	permPermissionManage  = "permission.manage"
	permJudgeView         = "judge.view"
	permFeedModerate      = "feed.moderate"
	permSiteSettings      = "site.settings"
	permSubmissionViewAny = "submission.view.any"
)

func (a actor) can(key string) bool { return a.perms[key] }

// canEditProblem is ownership-scoped authoring: whoever created a problem keeps
// editing it, and problem.edit.any is only needed to touch someone else's.
func (a actor) canEditProblem(p store.Problem) bool {
	if a.can(permProblemEditAny) {
		return true
	}
	return p.OwnerID != 0 && p.OwnerID == a.User.ID
}

// canSeeTestcases covers the non-public view of a problem's test set.
func (a actor) canSeeTestcases(p store.Problem) bool {
	return a.can(permTestcaseManage) || a.canEditProblem(p)
}

// requirePerm authenticates and enforces a single capability. The message names
// the key rather than a label so it can never drift from what the database holds.
func (s *Server) requirePerm(w http.ResponseWriter, r *http.Request, key string) (actor, bool) {
	a, err := s.authenticate(r)
	if err != nil {
		s.writeAuthError(w, r, err)
		return actor{}, false
	}
	if !a.can(key) {
		s.respondError(w, r, http.StatusForbidden, "缺少权限："+key)
		return a, false
	}
	return a, true
}

// requireProblemEdit loads a problem and checks authoring rights. Owning it is
// enough; problem.edit.any is only needed to touch someone else's.
func (s *Server) requireProblemEdit(w http.ResponseWriter, r *http.Request, key string) (actor, store.Problem, bool) {
	a, err := s.authenticate(r)
	if err != nil {
		s.writeAuthError(w, r, err)
		return actor{}, store.Problem{}, false
	}
	p, err := s.db.FindProblem(r.Context(), key)
	if err != nil {
		s.writeStoreError(w, r, err)
		return actor{}, store.Problem{}, false
	}
	if !a.canEditProblem(p) {
		s.respondError(w, r, http.StatusForbidden,
			"这道题由他人创建，需要 "+permProblemEditAny+" 才能修改")
		return a, p, false
	}
	return a, p, true
}

// requireCaseAccess admits the problem's author and, separately, a test-data
// maintainer — so someone can be allowed to upload cases to problems they did
// not write without being able to rewrite the statement.
func (s *Server) requireCaseAccess(w http.ResponseWriter, r *http.Request, key string) (actor, store.Problem, bool) {
	a, err := s.authenticate(r)
	if err != nil {
		s.writeAuthError(w, r, err)
		return actor{}, store.Problem{}, false
	}
	p, err := s.db.FindProblem(r.Context(), key)
	if err != nil {
		s.writeStoreError(w, r, err)
		return actor{}, store.Problem{}, false
	}
	if !a.canSeeTestcases(p) {
		s.respondError(w, r, http.StatusForbidden,
			"需要 "+permTestcaseManage+" 或该题的作者权限")
		return a, p, false
	}
	return a, p, true
}
