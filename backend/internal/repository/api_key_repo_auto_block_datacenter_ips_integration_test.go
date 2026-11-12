//go:build integration

package repository

import "github.com/Wei-Shaw/sub2api/internal/service"

// The auto-block flag must survive the whole write/read path: Create writes it,
// Update writes it only when the field mask says so, the auth projection
// (GetByKeyForAuth) still reads it back, and the runner's listing selects it.
func (s *APIKeyRepoSuite) TestAutoBlockDatacenterIPsDefaultAndRoundTrip() {
	user := s.mustCreateUser("auto-block-datacenter@example.com")
	key := &service.APIKey{
		UserID:                 user.ID,
		Key:                    "auto-block-default-key",
		Name:                   "default",
		Status:                 service.StatusActive,
		AutoBlockDatacenterIPs: true,
	}
	s.Require().NoError(s.repo.Create(s.ctx, key))

	loaded, err := s.repo.GetByID(s.ctx, key.ID)
	s.Require().NoError(err)
	s.Require().True(loaded.AutoBlockDatacenterIPs)

	loaded.AutoBlockDatacenterIPs = false
	s.Require().NoError(s.repo.Update(s.ctx, loaded, service.APIKeyUpdateFields{AutoBlockDatacenterIPs: true}))
	reloaded, err := s.repo.GetByID(s.ctx, key.ID)
	s.Require().NoError(err)
	s.Require().False(reloaded.AutoBlockDatacenterIPs)

	// Ent creates that omit the field take the schema default (false).
	entity, err := s.client.APIKey.Create().SetUserID(user.ID).
		SetKey("auto-block-ent-default-key").SetName("ent default").Save(s.ctx)
	s.Require().NoError(err)
	s.Require().False(entity.AutoBlockDatacenterIps)

	// Opt back in, then the runner's listing must return exactly this key.
	reloaded.AutoBlockDatacenterIPs = true
	s.Require().NoError(s.repo.Update(s.ctx, reloaded, service.APIKeyUpdateFields{AutoBlockDatacenterIPs: true}))
	keys, err := s.repo.ListAutoBlockDatacenterIPsKeys(s.ctx, 200)
	s.Require().NoError(err)
	found := false
	for _, candidate := range keys {
		if candidate.ID == key.ID {
			found = true
		}
	}
	s.Require().True(found, "opted-in key must be listed by the runner")
}
