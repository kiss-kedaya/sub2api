package service

import "context"

// requireAccountOwnerAccess protects admin operations that otherwise write
// directly by ID. Background callers without an administrator scope are unchanged.
func (s *adminServiceImpl) requireAccountOwnerAccess(ctx context.Context, accountID int64) error {
	if _, _, scoped := AccountOwnerScopeDetail(ctx); !scoped {
		return nil
	}
	_, err := s.accountRepo.GetByID(ctx, accountID)
	return err
}
