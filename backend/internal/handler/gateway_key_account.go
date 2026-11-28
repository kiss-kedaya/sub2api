package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// ---------- /v1/sub2api/account ----------

type keyAccountBalance struct {
	// Wallet is what the account can spend; Frozen is held for asynchronous batch jobs that are still running and
	// cannot be spent meanwhile. Both are in USD, with the precision the console shows.
	Wallet float64 `json:"wallet"`
	Frozen float64 `json:"frozen"`
	Unit   string  `json:"unit"`
}

type keyAccountSubscription struct {
	GroupID   int64     `json:"group_id"`
	GroupName string    `json:"group_name"`
	ExpiresAt time.Time `json:"expires_at"`
	// Remaining is what is left of the plan's tightest limit (daily, weekly or monthly), in USD. A plan without any
	// limit has no Remaining and Unlimited is true.
	Remaining *float64 `json:"remaining,omitempty"`
	Unlimited bool     `json:"unlimited"`
}

type keyAccountResponse struct {
	Object        string             `json:"object"`
	SchemaVersion int                `json:"schema_version"`
	Timezone      string             `json:"timezone"`
	Balance       *keyAccountBalance `json:"balance,omitempty"`
	// Subscriptions is present (possibly empty) when the read worked: an empty list means "no active subscription".
	Subscriptions *[]keyAccountSubscription `json:"subscriptions,omitempty"`
	// Today is the whole account's usage (every key of the owner together) for the local day the timezone names.
	Today *keyUsageBlock `json:"today,omitempty"`
	// Partial lists the blocks that could not be read within the time budget. They are left out of the answer,
	// never reported as zero.
	Partial    []string  `json:"partial,omitempty"`
	ObservedAt time.Time `json:"observed_at"`
}

const keyAccountUnit = "USD"

// KeyAccount returns what the account behind the authenticated key holds and has used today.
// GET /v1/sub2api/account[?timezone=Area/City]
//
// It is about the OWNER, not the key: the wallet balance, what is left of each active subscription, and today's usage
// of all the owner's keys together. That is deliberate (a program holding only an API key can read it), so whoever
// holds a valid key can read these figures. Nothing that says who the owner is (email, username, phone, id) and
// nothing about the owner's other keys is returned, and the account is always the key's own owner, never chosen by a
// request parameter. Like the other self-service endpoints it answers for a key that is expired or out of quota
// (authentication has already refused a switched-off key, a disabled owner and an IP the key does not allow).
//
// Every block is read separately and may be missing: a slow or busy database never turns into zero balance or zero
// usage, the block is left out and named in "partial". Nothing here is written to a log.
func (h *GatewayHandler) KeyAccount(c *gin.Context) {
	apiKey, ok := h.keyInfoSubject(c)
	if !ok {
		return
	}
	if h.apiKeyService == nil || h.usageService == nil {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "Account information is unavailable")
		return
	}
	frame := newKeyInfoFrame(c.Query("timezone"))
	// One budget for all three reads: they are small primary-key reads and one cached aggregate, and together they
	// must not hold a status poll longer than this.
	ctx, cancel := context.WithTimeout(c.Request.Context(), keyInfoQueryBudget*2)
	defer cancel()

	resp := keyAccountResponse{
		Object:        "sub2api.account",
		SchemaVersion: keyInfoSchemaVersion,
		Timezone:      frame.Loc.String(),
		ObservedAt:    frame.Now.UTC(),
	}
	partial := make([]string, 0, 3)

	if wallet, err := h.apiKeyService.GetAccountWallet(ctx, apiKey.UserID); err == nil && wallet != nil {
		resp.Balance = &keyAccountBalance{Wallet: wallet.Balance, Frozen: wallet.Frozen, Unit: keyAccountUnit}
	} else {
		partial = append(partial, "balance")
	}

	if subs, err := h.apiKeyService.ListActiveSubscriptions(ctx, apiKey.UserID); err == nil {
		list := h.keyAccountSubscriptions(subs, frame.Now)
		resp.Subscriptions = &list
	} else {
		partial = append(partial, "subscriptions")
	}

	dayStart := frame.TodayStart
	dayEnd := dayStart.AddDate(0, 0, 1)
	if summary, err := h.usageService.GetUserDayUsageSummary(ctx, apiKey.UserID, frame.Now, dayStart, dayEnd); err == nil && summary != nil {
		resp.Today = &keyUsageBlock{
			Label:              "today",
			Start:              dayStart.In(frame.Loc).Format("2006-01-02"),
			End:                dayEnd.AddDate(0, 0, -1).In(frame.Loc).Format("2006-01-02"),
			APIKeyUsageSummary: *summary,
		}
	} else {
		partial = append(partial, "today")
	}

	if len(partial) > 0 {
		resp.Partial = partial
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, resp)
}

// keyAccountSubscriptions turns the user's active subscriptions into what clients read: what is left of each plan's
// tightest limit. It reuses calculateSubscriptionRemaining, the figure GET /v1/usage reports for a subscription key,
// on a copy whose rolled-over windows count as unused. A window is only reset in the database by the next request
// that bills against it, so the stored counters can still hold yesterday's usage in the morning, and reading them as
// they are would report an allowance as used up that the next request will find full again.
func (h *GatewayHandler) keyAccountSubscriptions(subs []service.UserSubscription, now time.Time) []keyAccountSubscription {
	out := make([]keyAccountSubscription, 0, len(subs))
	for i := range subs {
		sub := subs[i] // a copy: the zeroing below must not touch the caller's data
		if sub.Group == nil || !sub.IsActive() {
			continue
		}
		if sub.NeedsDailyResetAt(now) {
			sub.DailyUsageUSD = 0
		}
		if reset := sub.WeeklyResetTime(); reset != nil && !now.Before(*reset) {
			sub.WeeklyUsageUSD = 0
		}
		if reset := sub.MonthlyResetTime(); reset != nil && !now.Before(*reset) {
			sub.MonthlyUsageUSD = 0
		}
		item := keyAccountSubscription{GroupID: sub.GroupID, GroupName: sub.Group.Name, ExpiresAt: sub.ExpiresAt.UTC()}
		if remaining := h.calculateSubscriptionRemaining(sub.Group, &sub); remaining < 0 {
			item.Unlimited = true
		} else {
			item.Remaining = &remaining
		}
		out = append(out, item)
	}
	return out
}
