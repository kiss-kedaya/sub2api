//go:build integration

package repository

import "github.com/Wei-Shaw/sub2api/internal/service"

func (s *APIKeyRepoSuite) TestDeepSeekToolInjectionDefaultAndAuthProjection() {
	user := s.mustCreateUser("deepseek-tool-default@example.com")
	key := &service.APIKey{UserID: user.ID, Key: "deepseek-default-key", Name: "default", Status: service.StatusActive}
	s.Require().NoError(s.repo.Create(s.ctx, key))
	loaded, err := s.repo.GetByKeyForAuth(s.ctx, key.Key)
	s.Require().NoError(err)
	s.Require().True(loaded.IsDeepSeekToolInjectionEnabled())

	// Ent creates that omit the new field use the same true database default.
	entity, err := s.client.APIKey.Create().SetUserID(user.ID).
		SetKey("deepseek-ent-default-key").SetName("ent default").Save(s.ctx)
	s.Require().NoError(err)
	s.Require().True(entity.DeepseekToolInjectionEnabled)
}

func (s *APIKeyRepoSuite) TestDeepSeekToolInjectionDisabledPersistsAndUnrelatedEditsPreserveIt() {
	user := s.mustCreateUser("deepseek-tool-disabled@example.com")
	disabled, enabled := false, true
	key := &service.APIKey{
		UserID: user.ID, Key: "deepseek-disabled-key", Name: "disabled", Status: service.StatusActive,
		DeepSeekToolInjectionEnabled: &disabled,
	}
	s.Require().NoError(s.repo.Create(s.ctx, key))
	loaded, err := s.repo.GetByKeyForAuth(s.ctx, key.Key)
	s.Require().NoError(err)
	s.Require().NotNil(loaded.DeepSeekToolInjectionEnabled)
	s.Require().False(loaded.IsDeepSeekToolInjectionEnabled(), "the minimum auth projection must retain explicit false")

	// A stale unrelated edit must not overwrite the key's tool setting.
	loaded.Name = "renamed"
	loaded.DeepSeekToolInjectionEnabled = &enabled
	s.Require().NoError(s.repo.Update(s.ctx, loaded, service.APIKeyUpdateFields{Name: true}))
	loaded, err = s.repo.GetByID(s.ctx, key.ID)
	s.Require().NoError(err)
	s.Require().False(loaded.IsDeepSeekToolInjectionEnabled())

	// Updating the switch must not reset live billing counters.
	_, err = s.repo.IncrementQuotaUsed(s.ctx, key.ID, 12)
	s.Require().NoError(err)
	loaded.DeepSeekToolInjectionEnabled = &enabled
	s.Require().NoError(s.repo.Update(s.ctx, loaded, service.APIKeyUpdateFields{DeepSeekToolInjectionEnabled: true}))
	loaded, err = s.repo.GetByKeyForAuth(s.ctx, key.Key)
	s.Require().NoError(err)
	s.Require().True(loaded.IsDeepSeekToolInjectionEnabled())
	s.Require().Equal(12.0, loaded.QuotaUsed)
	rows, err := s.client.QueryContext(s.ctx, `SELECT COUNT(*) FROM auth_cache_invalidation_outbox
WHERE cache_key = encode(sha256(convert_to($1, 'UTF8')), 'hex')`, key.Key)
	s.Require().NoError(err)
	defer func() { _ = rows.Close() }()
	s.Require().True(rows.Next())
	var invalidations int
	s.Require().NoError(rows.Scan(&invalidations))
	s.Require().Greater(invalidations, 0, "tool setting changes must enqueue durable cross-instance cache invalidation")
}
