package service

import (
	"context"
	"errors"
)

// errTwoFactorResetForeignSession 表示用户的会话索引里出现了不属于该用户的刷新令牌。
// 这种索引不可信：只删能证明属于该用户的令牌，绝不能整组删除。
var errTwoFactorResetForeignSession = errors.New("two-factor reset: session index references a token owned by another user")

// TwoFactorResetStateStore 是 TotpCache 里「2FA 绑定过程的短期状态」那一部分：
// 绑定会话（totp:setup:<uid>）与验证次数预算（totp:attempts:<uid>）。
// 重置 2FA 后必须清掉，否则忘码输错 5 次的用户重新绑定最长要被卡 15 分钟。
type TwoFactorResetStateStore interface {
	DeleteSetupSession(ctx context.Context, userID int64) error
	ClearVerifyAttempts(ctx context.Context, userID int64) error
}

// RevokeUserSessionsForTwoFactorReset 清掉「用户 2FA 被重置之后」不能继续存活的 Redis 状态：
//
//  1. 该用户全部 refresh token（只按用户索引集合列举，绝不 SCAN 整库）；
//  2. 全部会话（refresh token 家族 ID + 遗留键 u<uid>）× 全部因子的 step-up 授权；
//  3. 绑定会话 totp:setup:<uid> 与验证次数预算 totp:attempts:<uid>。
//
// 它是 #641 命令行（cmd/server -reset-admin-2fa）与 #642 管理端接口共用的唯一实现，
// 两条恢复路径的清理强度因此始终一致。它不撤销已签发的 access JWT（无状态，
// 到期前仍可用）和登录用的 5 分钟 2FA 凭证（two_factor_grant:*，调用方按需另行撤销）。
//
// 任何一步失败都不会阻止后面的步骤（尽量清干净），所有错误合并后返回；调用方
// 必须如实上报，不能谎报「已撤销」。
//
// 会话索引不可信时（读索引失败、读令牌失败、令牌不属于该用户）只逐个删除已证明
// 属于该用户的令牌，不调用整组删除，避免一个被污染的索引把别的用户登出。
func RevokeUserSessionsForTwoFactorReset(
	ctx context.Context,
	userID int64,
	refreshTokens RefreshTokenCache,
	stepUp StepUpGrantRevoker,
	state TwoFactorResetStateStore,
) error {
	if userID <= 0 {
		return errors.New("two-factor reset: invalid user")
	}

	var (
		lookupErr   error
		revokeErr   error
		sessionKeys []string
	)
	if refreshTokens == nil {
		lookupErr = errors.New("two-factor reset: refresh token store unavailable")
	} else {
		var ownedHashes []string
		seenFamilies := make(map[string]struct{})
		hashes, err := refreshTokens.GetUserTokenHashes(ctx, userID)
		unsafeIndex := err != nil
		lookupErr = err
		for _, hash := range hashes {
			data, err := refreshTokens.GetRefreshToken(ctx, hash)
			if errors.Is(err, ErrRefreshTokenNotFound) {
				continue
			}
			if err != nil {
				lookupErr, unsafeIndex = err, true
				continue
			}
			if data == nil || data.UserID != userID {
				lookupErr, unsafeIndex = errTwoFactorResetForeignSession, true
				continue
			}
			ownedHashes = append(ownedHashes, hash)
			if _, dup := seenFamilies[data.FamilyID]; data.FamilyID != "" && !dup {
				seenFamilies[data.FamilyID] = struct{}{}
				sessionKeys = append(sessionKeys, data.FamilyID)
			}
		}
		if unsafeIndex {
			for _, hash := range ownedHashes {
				revokeErr = errors.Join(revokeErr, refreshTokens.DeleteRefreshToken(ctx, hash))
			}
		} else {
			revokeErr = refreshTokens.DeleteUserRefreshTokens(ctx, userID)
		}
	}

	// RevokeStepUpGrantsForUser 自己会补遗留键 u<uid>，这里只传已知的会话族 ID。
	var stepUpErr error
	if stepUp == nil {
		stepUpErr = errors.New("two-factor reset: step-up grant store unavailable")
	} else {
		_, stepUpErr = stepUp.RevokeStepUpGrantsForUser(ctx, userID, sessionKeys)
	}

	var stateErr error
	if state == nil {
		stateErr = errors.New("two-factor reset: enrolment state store unavailable")
	} else {
		stateErr = errors.Join(state.DeleteSetupSession(ctx, userID), state.ClearVerifyAttempts(ctx, userID))
	}
	return errors.Join(lookupErr, revokeErr, stepUpErr, stateErr)
}
