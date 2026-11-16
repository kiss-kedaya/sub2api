//go:build integration

package repository

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func emailBlacklistProbeKeys(emails ...string) []string {
	keys := make([]string, 0, 2*len(emails))
	for _, email := range emails {
		for _, probe := range service.EmailAliasDedupProbes(email) {
			keys = append(keys, service.EmailAliasProbeKey(probe))
		}
	}
	return keys
}

func (s *UserRepoSuite) TestListActiveUsersByEmailAliasProbeKeys_MatchesInboxVariants() {
	dotted := s.mustCreateUser(&service.User{Email: "bl.target@gmail.com"})
	plusGooglemail := s.mustCreateUser(&service.User{Email: "bltarget+promo@googlemail.com"})
	upperRootDot := s.mustCreateUser(&service.User{Email: "BL.Tar.Get@Gmail.com."})
	nonGmailDotted := s.mustCreateUser(&service.User{Email: "bl.target@company.example"})
	s.mustCreateUser(&service.User{Email: "bltarget2@gmail.com"})
	s.mustCreateUser(&service.User{Email: "b.l.target@gmail.com", Status: service.StatusDisabled})
	s.mustCreateUser(&service.User{Email: "bltarget@gmail.com", Role: service.RoleAdmin})
	deleted := s.mustCreateUser(&service.User{Email: "bltar.get+x@gmail.com"})
	s.Require().NoError(s.repo.Delete(s.ctx, deleted.ID))

	users, err := s.repo.ListActiveUsersByEmailAliasProbeKeys(s.ctx,
		emailBlacklistProbeKeys("bltarget@googlemail.com", "bltarget@company.example"))
	s.Require().NoError(err)

	ids := make([]int64, 0, len(users))
	for _, user := range users {
		s.Require().Equal(service.StatusActive, user.Status)
		ids = append(ids, user.ID)
	}
	// 仓储层按去点形态过度匹配非 Gmail 域名（bl.target@company.example），由 service 层复核。
	s.Require().ElementsMatch([]int64{dotted.ID, plusGooglemail.ID, upperRootDot.ID, nonGmailDotted.ID}, ids)
}

func (s *UserRepoSuite) TestListActiveUsersByEmailAliasProbeKeys_EmptyKeys() {
	s.mustCreateUser(&service.User{Email: "bl-empty@gmail.com"})

	users, err := s.repo.ListActiveUsersByEmailAliasProbeKeys(s.ctx, nil)
	s.Require().NoError(err)
	s.Require().Empty(users)
}

func (s *UserRepoSuite) TestBatchDisableUsers_SkipsAdminDisabledAndDeleted() {
	active := s.mustCreateUser(&service.User{Email: "bl-disable-active@example.com"})
	admin := s.mustCreateUser(&service.User{Email: "bl-disable-admin@example.com", Role: service.RoleAdmin})
	already := s.mustCreateUser(&service.User{Email: "bl-disable-already@example.com", Status: service.StatusDisabled})
	deleted := s.mustCreateUser(&service.User{Email: "bl-disable-deleted@example.com"})
	s.Require().NoError(s.repo.Delete(s.ctx, deleted.ID))

	disabled, err := s.repo.BatchDisableUsers(s.ctx, []int64{active.ID, admin.ID, already.ID, deleted.ID, 99999999})
	s.Require().NoError(err)
	s.Require().Equal([]int64{active.ID}, disabled)

	got, err := s.repo.GetByID(s.ctx, active.ID)
	s.Require().NoError(err)
	s.Require().Equal(service.StatusDisabled, got.Status)

	gotAdmin, err := s.repo.GetByID(s.ctx, admin.ID)
	s.Require().NoError(err)
	s.Require().Equal(service.StatusActive, gotAdmin.Status)
}
