package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// ---- test doubles ---------------------------------------------------------------------------------------------

type fakeRouteBindings struct {
	mu       sync.Mutex
	data     map[string]int64
	getErr   error
	setErr   error
	getDelay time.Duration
	gets     int
	sets     int
	lastTTL  time.Duration
}

func routeBindingTestKey(apiKeyID int64, model, session string) string {
	return fmt.Sprintf("%d|%s|%s", apiKeyID, model, session)
}

func (f *fakeRouteBindings) GetRouteGroup(ctx context.Context, apiKeyID int64, model, sessionHash string) (int64, error) {
	f.mu.Lock()
	f.gets++
	delay, err := f.getDelay, f.getErr
	id, ok := f.data[routeBindingTestKey(apiKeyID, model, sessionHash)]
	f.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, ErrStickySessionNotFound
	}
	return id, nil
}

func (f *fakeRouteBindings) SetRouteGroup(_ context.Context, apiKeyID int64, model, sessionHash string, groupID int64, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets++
	f.lastTTL = ttl
	if f.setErr != nil {
		return f.setErr
	}
	if f.data == nil {
		f.data = map[string]int64{}
	}
	f.data[routeBindingTestKey(apiKeyID, model, sessionHash)] = groupID
	return nil
}

func (f *fakeRouteBindings) bind(apiKeyID int64, model, session string, groupID int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.data == nil {
		f.data = map[string]int64{}
	}
	f.data[routeBindingTestKey(apiKeyID, model, session)] = groupID
}

func (f *fakeRouteBindings) bound(apiKeyID int64, model, session string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.data[routeBindingTestKey(apiKeyID, model, session)]
}

// routeBindingGatewayCache is a GatewayCache that can also bind route groups.
type routeBindingGatewayCache struct {
	*schedulerTestGatewayCache
	*fakeRouteBindings
}

func newRouteBindingGatewayCache() *routeBindingGatewayCache {
	return &routeBindingGatewayCache{schedulerTestGatewayCache: &schedulerTestGatewayCache{}, fakeRouteBindings: &fakeRouteBindings{}}
}

func routeTestGroup(id int64, platform string, rate float64) *Group {
	return &Group{ID: id, Platform: platform, Status: StatusActive, Hydrated: true, RateMultiplier: rate}
}

func routeTestAccount(id int64, platform string, groupID int64, models ...string) *Account {
	mapping := map[string]any{}
	for _, m := range models {
		mapping[m] = m
	}
	return &Account{
		ID: id, Name: "account", Platform: platform, Type: AccountTypeAPIKey, Status: StatusActive,
		Schedulable: true, Concurrency: 2, Priority: 1,
		AccountGroups: []AccountGroup{{AccountID: id, GroupID: groupID}}, GroupIDs: []int64{groupID},
		Credentials: map[string]any{"model_mapping": mapping},
	}
}

func routeTestKey(groups ...*Group) *APIKey {
	key := smartRouteCoreKey(groups...)
	key.ID = 7
	return key
}

func routeTestDeps(groups []*Group, quality map[int64]GroupRouteQualitySnapshot, bindings RouteGroupBindingCache) routeGroupDeps {
	byID := map[int64]*Group{}
	for _, g := range groups {
		byID[g.ID] = g
	}
	return routeGroupDeps{
		quality:  quality,
		bindings: bindings,
		ttl:      time.Hour,
		group:    func(id int64) *Group { return byID[id] },
		rate:     func(_, _ int64, def float64) float64 { return def },
	}
}

func goodQuality() GroupRouteQualitySnapshot {
	return GroupRouteQualitySnapshot{Overall: GroupRouteQuality{Samples: 100, SuccessRate: 1, CacheRate: 1, TTFTMs: 500}}
}

// worstButNotFailingQuality scores far below goodQuality yet keeps a success rate that
// does not make the monitor treat the group as failing.
func worstButNotFailingQuality() GroupRouteQualitySnapshot {
	return GroupRouteQualitySnapshot{Overall: GroupRouteQuality{Samples: 100, SuccessRate: 0.8, CacheRate: 0, TTFTMs: 4000}}
}

func poorQuality() GroupRouteQualitySnapshot {
	return GroupRouteQualitySnapshot{Overall: GroupRouteQuality{Samples: 100, SuccessRate: 0.3, CacheRate: 0, TTFTMs: 4000}}
}

func ctxWithUpstreamFailure(accountID int64) context.Context {
	ctx := withUpstreamAttempts(context.Background())
	state := ctx.Value(upstreamAttemptContextKey{}).(*upstreamAttempts)
	state.mu.Lock()
	state.failed[accountID] = false
	state.mu.Unlock()
	return ctx
}

// ---- scoring --------------------------------------------------------------------------------------------------

func TestRouteWeightsSumToOne(t *testing.T) {
	require.InDelta(t, 1.0, routeWeightSuccess+routeWeightCache+routeWeightTTFT+routeWeightCost+routeWeightPriority, 1e-9)
}

func TestRouteQualityTerms(t *testing.T) {
	s, c, f := routeQualityTerms(GroupRouteQuality{}, false)
	require.Equal(t, []float64{0.5, 0.5, 0.5}, []float64{s, c, f}, "unknown is neutral on all three")

	s, c, f = routeQualityTerms(GroupRouteQuality{SuccessRate: 1, CacheRate: 1, TTFTMs: 0, Samples: 100}, true)
	require.Equal(t, []float64{1, 1, 0.5}, []float64{s, c, f}, "no TTFT sample is neutral, not the best score")

	s, c, f = routeQualityTerms(GroupRouteQuality{SuccessRate: 0.4, CacheRate: 0.2, TTFTMs: 2500, Samples: 100}, true)
	require.InDelta(t, 0.4, s, 1e-9)
	require.InDelta(t, 0.2, c, 1e-9)
	require.InDelta(t, 0.5, f, 1e-9)

	_, _, f = routeQualityTerms(GroupRouteQuality{TTFTMs: 20_000, Samples: 100}, true)
	require.Zero(t, f, "slower than the target scores zero")
}

func TestRouteCostTerms(t *testing.T) {
	std := func(id int64, rate float64) *Group { return routeTestGroup(id, PlatformAnthropic, rate) }
	ids := []int64{1, 2, 3}

	t.Run("cheapest is 1 and a pricier group gets the ratio", func(t *testing.T) {
		groups := map[int64]*Group{1: std(1, 1.0), 2: std(2, 0.8), 3: std(3, 2.0)}
		terms := routeCostTerms(ids, groups, map[int64]float64{1: 1.0, 2: 0.8, 3: 2.0}, "m")
		require.InDelta(t, 0.8, terms[1], 1e-9)
		require.InDelta(t, 1.0, terms[2], 1e-9)
		require.InDelta(t, 0.4, terms[3], 1e-9)
	})

	t.Run("a free group scores 1 and does not drag the others to zero", func(t *testing.T) {
		groups := map[int64]*Group{1: std(1, 0), 2: std(2, 1.0), 3: std(3, 2.0)}
		terms := routeCostTerms(ids, groups, map[int64]float64{1: 0, 2: 1.0, 3: 2.0}, "m")
		require.InDelta(t, 1.0, terms[1], 1e-9)
		require.InDelta(t, 1.0, terms[2], 1e-9)
		require.InDelta(t, 0.5, terms[3], 1e-9)
	})

	t.Run("subscription, incomplete and not-allowed groups stay neutral and do not set the baseline", func(t *testing.T) {
		sub := std(1, 0.1)
		sub.SubscriptionType = SubscriptionTypeSubscription
		incomplete := std(2, 0.1)
		incomplete.Hydrated = false
		blocked := std(3, 0.1)
		blocked.ModelAllowlist = GroupModelsListConfig{Enabled: true, Models: []string{"other"}}
		ok1, ok2 := std(4, 1.0), std(5, 2.0)
		all := []int64{1, 2, 3, 4, 5}
		groups := map[int64]*Group{1: sub, 2: incomplete, 3: blocked, 4: ok1, 5: ok2}
		rates := map[int64]float64{1: 0.1, 2: 0.1, 3: 0.1, 4: 1.0, 5: 2.0}
		terms := routeCostTerms(all, groups, rates, "m")
		for _, id := range []int64{1, 2, 3} {
			require.InDelta(t, 0.5, terms[id], 1e-9, "group %d", id)
		}
		require.InDelta(t, 1.0, terms[4], 1e-9, "the baseline is the cheapest group that could actually serve the request")
		require.InDelta(t, 0.5, terms[5], 1e-9)
	})

	t.Run("fewer than two comparable groups means nothing to compare", func(t *testing.T) {
		groups := map[int64]*Group{1: std(1, 1.0), 2: std(2, 0.1)}
		terms := routeCostTerms([]int64{1, 2}, groups, map[int64]float64{1: 1.0}, "m") // group 2's rate is unknown
		require.Equal(t, 0.5, terms[1])
		require.Equal(t, 0.5, terms[2])
	})
}

func TestRankRouteGroups(t *testing.T) {
	t.Run("highest score first, never drops a group", func(t *testing.T) {
		scores := map[int64]float64{1: 0.4, 2: 0.9, 3: 0.6}
		require.Equal(t, []int64{2, 3, 1}, rankRouteGroups([]int64{1, 2, 3}, scores))
	})
	t.Run("scores within the resolution are a tie that goes to the owner's order", func(t *testing.T) {
		scores := map[int64]float64{1: 0.50, 2: 0.51, 3: 0.52}
		require.Equal(t, []int64{1, 2, 3}, rankRouteGroups([]int64{1, 2, 3}, scores))
	})
	t.Run("a lead larger than the resolution wins over the owner's order", func(t *testing.T) {
		scores := map[int64]float64{1: 0.50, 2: 0.56}
		require.Equal(t, []int64{2, 1}, rankRouteGroups([]int64{1, 2}, scores))
	})
	t.Run("ties are decided against the best remaining group, not by chaining", func(t *testing.T) {
		// 3 is within 0.02 of 2, but 1 is not within 0.02 of 3: 2 and 3 lead, 1 follows.
		scores := map[int64]float64{1: 0.50, 2: 0.55, 3: 0.54}
		require.Equal(t, []int64{2, 3, 1}, rankRouteGroups([]int64{1, 2, 3}, scores))
	})
}

// ---- planning ---------------------------------------------------------------------------------------------------

func TestPlanRouteGroupsLeavesTrivialCasesAlone(t *testing.T) {
	one := []*Group{routeTestGroup(1, PlatformAnthropic, 1)}
	bindings := &fakeRouteBindings{}
	plan := planRouteGroups(context.Background(), routeTestDeps(one, nil, bindings), routeTestKey(one...), []int64{1}, "m", "sess")
	require.Equal(t, []int64{1}, plan.Order)
	require.Zero(t, bindings.gets+bindings.sets, "a single group never touches Redis")

	plan = planRouteGroups(context.Background(), routeGroupDeps{}, nil, []int64{1, 2}, "m", "sess")
	require.Equal(t, []int64{1, 2}, plan.Order, "no key: keep the order")

	plan = planRouteGroups(context.Background(), routeGroupDeps{}, routeTestKey(one...), []int64{1, 2}, "m", "sess")
	require.Equal(t, []int64{1, 2}, plan.Order, "no information at all: the owner's order")
}

func TestPlanRouteGroupsNewSessionIsJudgedByAllFiveFactors(t *testing.T) {
	std := func(rate ...float64) []*Group {
		var groups []*Group
		for i, r := range rate {
			groups = append(groups, routeTestGroup(int64(i+1), PlatformAnthropic, r))
		}
		return groups
	}
	cases := []struct {
		name    string
		groups  []*Group
		quality map[int64]GroupRouteQualitySnapshot
		want    []int64
	}{
		{"nothing known keeps the owner's order", std(1, 1, 1), nil, []int64{1, 2, 3}},
		{"the owner's priority breaks a tie on equal quality and price", std(1, 1), map[int64]GroupRouteQualitySnapshot{1: goodQuality(), 2: goodQuality()}, []int64{1, 2}},
		{"success, cache and first-token time outweigh priority", std(1, 1), map[int64]GroupRouteQualitySnapshot{1: poorQuality(), 2: goodQuality()}, []int64{2, 1}},
		{"a much cheaper group overtakes an adjacent higher-priority one", std(1.0, 0.25, 1.0), nil, []int64{2, 1, 3}},
		{"a slightly cheaper group does not overtake the owner's first choice", std(1.0, 0.9), nil, []int64{1, 2}},
		{"price alone cannot beat the owner's first choice when quality is equal", std(1.0, 0.1), nil, []int64{1, 2}},
		{"a better and cheaper group wins", std(1.0, 0.5), map[int64]GroupRouteQualitySnapshot{1: poorQuality(), 2: goodQuality()}, []int64{2, 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ids := make([]int64, len(tc.groups))
			for i, g := range tc.groups {
				ids[i] = g.ID
			}
			plan := planRouteGroups(context.Background(), routeTestDeps(tc.groups, tc.quality, nil), routeTestKey(tc.groups...), ids, "m", "")
			require.Equal(t, tc.want, plan.Order)
		})
	}
}

func TestPlanRouteGroupsUsesTheModelsOwnNumbers(t *testing.T) {
	groups := []*Group{routeTestGroup(1, PlatformAnthropic, 1), routeTestGroup(2, PlatformAnthropic, 1)}
	// Group 1 is great overall but poor for the model asked about; group 2 the other way round.
	quality := map[int64]GroupRouteQualitySnapshot{
		1: {Overall: GroupRouteQuality{Samples: 1000, SuccessRate: 0.99, CacheRate: 0.9, TTFTMs: 400},
			ByModel: map[string]GroupRouteQuality{"claude-opus-5": {Samples: 50, SuccessRate: 0.3, CacheRate: 0, TTFTMs: 4500}}},
		2: {Overall: GroupRouteQuality{Samples: 1000, SuccessRate: 0.5, CacheRate: 0.1, TTFTMs: 3000},
			ByModel: map[string]GroupRouteQuality{"claude-opus-5": {Samples: 50, SuccessRate: 1, CacheRate: 1, TTFTMs: 400}}},
	}
	deps := routeTestDeps(groups, quality, nil)
	key := routeTestKey(groups...)
	require.Equal(t, []int64{2, 1}, planRouteGroups(context.Background(), deps, key, []int64{1, 2}, "Claude-Opus-5", "").Order)
	require.Equal(t, []int64{1, 2}, planRouteGroups(context.Background(), deps, key, []int64{1, 2}, "claude-haiku-5", "").Order,
		"a model with no numbers of its own is judged by the group as a whole")
}

func TestPlanRouteGroupsBoundSessionIsNotJudgedAgain(t *testing.T) {
	groups := []*Group{routeTestGroup(1, PlatformAnthropic, 1), routeTestGroup(2, PlatformAnthropic, 1), routeTestGroup(3, PlatformAnthropic, 1)}
	quality := map[int64]GroupRouteQualitySnapshot{1: goodQuality(), 2: GroupRouteQualitySnapshot{Overall: GroupRouteQuality{Samples: 100, SuccessRate: 0.9, CacheRate: 0.5, TTFTMs: 1500}}, 3: worstButNotFailingQuality()}
	bindings := &fakeRouteBindings{}
	bindings.bind(7, "m", "sess", 3) // the worst group by score, but not a failing one
	key := routeTestKey(groups...)

	plan := planRouteGroups(context.Background(), routeTestDeps(groups, quality, bindings), key, []int64{1, 2, 3}, "m", "sess")
	require.Equal(t, []int64{3, 1, 2}, plan.Order, "the bound group first, the rest in score order")
	require.Equal(t, int64(3), plan.Bound)

	// The same session a while later, after the numbers moved so that the others lead: still bound.
	quality[2] = goodQuality()
	quality[1] = worstButNotFailingQuality()
	plan = planRouteGroups(context.Background(), routeTestDeps(groups, quality, bindings), key, []int64{1, 2, 3}, "m", "sess")
	require.Equal(t, []int64{3, 2, 1}, plan.Order)
	require.Equal(t, int64(3), plan.Bound)
}

func TestPlanRouteGroupsRejudgesOnlyOnErrorOrNewSession(t *testing.T) {
	groups := []*Group{routeTestGroup(1, PlatformAnthropic, 1), routeTestGroup(2, PlatformAnthropic, 1)}
	quality := map[int64]GroupRouteQualitySnapshot{1: goodQuality(), 2: GroupRouteQualitySnapshot{Overall: GroupRouteQuality{Samples: 100, SuccessRate: 0.9, CacheRate: 0.6, TTFTMs: 1500}}}
	key := routeTestKey(groups...)

	t.Run("a new session is judged by score", func(t *testing.T) {
		bindings := &fakeRouteBindings{}
		plan := planRouteGroups(context.Background(), routeTestDeps(groups, quality, bindings), key, []int64{1, 2}, "m", "new")
		require.Equal(t, []int64{1, 2}, plan.Order)
		require.Zero(t, plan.Bound)
	})
	t.Run("an upstream failure earlier in this request is an error: judge again", func(t *testing.T) {
		bindings := &fakeRouteBindings{}
		bindings.bind(7, "m", "sess", 2)
		ctx := ctxWithUpstreamFailure(99)
		plan := planRouteGroups(ctx, routeTestDeps(groups, quality, bindings), key, []int64{1, 2}, "m", "sess")
		require.Equal(t, []int64{1, 2}, plan.Order, "the binding to group 2 is not honoured after an error")
		require.Zero(t, plan.Bound)
		require.Zero(t, bindings.gets, "and Redis is not even consulted")
	})
	t.Run("a request that has not failed keeps the binding even with attempt state installed", func(t *testing.T) {
		bindings := &fakeRouteBindings{}
		bindings.bind(7, "m", "sess", 2)
		plan := planRouteGroups(withUpstreamAttempts(context.Background()), routeTestDeps(groups, quality, bindings), key, []int64{1, 2}, "m", "sess")
		require.Equal(t, []int64{2, 1}, plan.Order)
	})
	t.Run("a group the monitor shows failing most requests is judged again", func(t *testing.T) {
		bindings := &fakeRouteBindings{}
		bindings.bind(7, "m", "sess", 2)
		failing := map[int64]GroupRouteQualitySnapshot{1: goodQuality(), 2: poorQuality()}
		plan := planRouteGroups(context.Background(), routeTestDeps(groups, failing, bindings), key, []int64{1, 2}, "m", "sess")
		require.Equal(t, []int64{1, 2}, plan.Order)
		require.Zero(t, plan.Bound)
	})
	t.Run("too few samples is not evidence that the group is failing", func(t *testing.T) {
		bindings := &fakeRouteBindings{}
		bindings.bind(7, "m", "sess", 2)
		thin := map[int64]GroupRouteQualitySnapshot{1: goodQuality(), 2: GroupRouteQualitySnapshot{Overall: GroupRouteQuality{Samples: 3, SuccessRate: 0}}}
		plan := planRouteGroups(context.Background(), routeTestDeps(groups, thin, bindings), key, []int64{1, 2}, "m", "sess")
		require.Equal(t, int64(2), plan.Order[0])
	})
	t.Run("a binding to a group the key no longer has is ignored", func(t *testing.T) {
		bindings := &fakeRouteBindings{}
		bindings.bind(7, "m", "sess", 42)
		plan := planRouteGroups(context.Background(), routeTestDeps(groups, quality, bindings), key, []int64{1, 2}, "m", "sess")
		require.Equal(t, []int64{1, 2}, plan.Order)
		require.Zero(t, plan.Bound)
	})
}

func TestPlanRouteGroupsBindingKeyIsPerKeyModelAndSession(t *testing.T) {
	groups := []*Group{routeTestGroup(1, PlatformAnthropic, 1), routeTestGroup(2, PlatformAnthropic, 1)}
	bindings := &fakeRouteBindings{}
	bindings.bind(7, "claude-opus-5", "sess", 2)
	key := routeTestKey(groups...)
	deps := routeTestDeps(groups, nil, bindings)

	require.Equal(t, int64(2), planRouteGroups(context.Background(), deps, key, []int64{1, 2}, "  Claude-Opus-5 ", "sess").Order[0], "the model is normalized")
	require.Equal(t, int64(1), planRouteGroups(context.Background(), deps, key, []int64{1, 2}, "claude-sonnet-5", "sess").Order[0], "another model is not bound")
	require.Equal(t, int64(1), planRouteGroups(context.Background(), deps, key, []int64{1, 2}, "claude-opus-5", "other-session").Order[0], "another session is not bound")
	other := routeTestKey(groups...)
	other.ID = 8
	require.Equal(t, int64(1), planRouteGroups(context.Background(), deps, other, []int64{1, 2}, "claude-opus-5", "sess").Order[0], "another key is not bound")
}

func TestPlanRouteGroupsWithoutASessionNeverTouchesTheBindingStore(t *testing.T) {
	groups := []*Group{routeTestGroup(1, PlatformAnthropic, 1), routeTestGroup(2, PlatformAnthropic, 1)}
	bindings := &fakeRouteBindings{}
	plan := planRouteGroups(context.Background(), routeTestDeps(groups, nil, bindings), routeTestKey(groups...), []int64{1, 2}, "m", "")
	plan.remember(context.Background(), 1)
	require.Zero(t, bindings.gets+bindings.sets)

	unsaved := routeTestKey(groups...)
	unsaved.ID = 0 // a key that has no id cannot own a binding
	plan = planRouteGroups(context.Background(), routeTestDeps(groups, nil, bindings), unsaved, []int64{1, 2}, "m", "sess")
	plan.remember(context.Background(), 1)
	require.Zero(t, bindings.gets+bindings.sets)
}

func TestPlanRouteGroupsFailsOpenWhenTheBindingStoreMisbehaves(t *testing.T) {
	groups := []*Group{routeTestGroup(1, PlatformAnthropic, 1), routeTestGroup(2, PlatformAnthropic, 1)}
	key := routeTestKey(groups...)

	broken := &fakeRouteBindings{getErr: errors.New("redis down"), setErr: errors.New("redis down")}
	plan := planRouteGroups(context.Background(), routeTestDeps(groups, nil, broken), key, []int64{1, 2}, "m", "sess")
	require.Equal(t, []int64{1, 2}, plan.Order)
	require.NotPanics(t, func() { plan.remember(context.Background(), 2) })

	slow := &fakeRouteBindings{getDelay: 5 * time.Second}
	slow.bind(7, "m", "sess", 2)
	started := time.Now()
	plan = planRouteGroups(context.Background(), routeTestDeps(groups, nil, slow), key, []int64{1, 2}, "m", "sess")
	require.Less(t, time.Since(started), time.Second, "a slow store must not hold the request")
	require.Equal(t, []int64{1, 2}, plan.Order, "and a binding that did not arrive in time is not used")
}

func TestRouteGroupPlanRememberWritesTheServingGroup(t *testing.T) {
	groups := []*Group{routeTestGroup(1, PlatformAnthropic, 1), routeTestGroup(2, PlatformAnthropic, 1)}
	bindings := &fakeRouteBindings{}
	plan := planRouteGroups(context.Background(), routeTestDeps(groups, nil, bindings), routeTestKey(groups...), []int64{1, 2}, "Claude-Opus-5", "sess")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the request is already over; the binding is still written
	plan.remember(ctx, 2)
	require.Equal(t, int64(2), bindings.bound(7, "claude-opus-5", "sess"))
	require.Equal(t, time.Hour, bindings.lastTTL)

	plan.remember(context.Background(), 0)
	require.Equal(t, 1, bindings.sets, "no group, no write")
}

// ---- selection through the OpenAI-compatible path -------------------------------------------------------------

type routeSelectionFixture struct {
	svc      *OpenAIGatewayService
	key      *APIKey
	cache    *routeBindingGatewayCache
	groups   []*Group
	tried    []int64
	serves   map[int64]bool
	modelIDs []string
}

func newRouteSelectionFixture(t *testing.T, rates ...float64) *routeSelectionFixture {
	t.Helper()
	f := &routeSelectionFixture{cache: newRouteBindingGatewayCache(), serves: map[int64]bool{}, modelIDs: []string{"gpt-5.5", "gpt-5.4"}}
	var accounts []*Account
	for i, rate := range rates {
		group := routeTestGroup(int64(i+1), PlatformOpenAI, rate)
		f.groups = append(f.groups, group)
		f.serves[group.ID] = true
		mapping := map[string]any{}
		for _, m := range f.modelIDs {
			mapping[m] = m
		}
		accounts = append(accounts, &Account{ID: int64(100 + i), Platform: PlatformOpenAI, GroupIDs: []int64{group.ID}, Credentials: map[string]any{"model_mapping": mapping}})
	}
	snapshot, _ := newSmartRouteCoreSnapshot(f.groups, accounts...)
	f.svc = &OpenAIGatewayService{schedulerSnapshot: snapshot, accountRepo: &modelsListAccountRepoStub{}, cache: f.cache, cfg: &config.Config{}}
	f.key = routeTestKey(f.groups...)
	return f
}

func (f *routeSelectionFixture) selectOne(_ context.Context, groupID *int64, _ []string, _ string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	f.tried = append(f.tried, *groupID)
	if f.serves[*groupID] {
		return &AccountSelectionResult{Account: &Account{ID: 100 + *groupID}}, OpenAIAccountScheduleDecision{}, nil
	}
	return nil, OpenAIAccountScheduleDecision{}, ErrNoAvailableAccounts
}

func (f *routeSelectionFixture) selectModel(ctx context.Context, model, session string) (*APIKey, error) {
	f.tried = nil
	_, _, routed, err := f.svc.selectAlongKeyRoutes(withRouteSessionHash(ctx, session), f.key, nil, model, f.selectOne)
	return routed, err
}

func (f *routeSelectionFixture) selectSession(ctx context.Context, session string) (*APIKey, error) {
	return f.selectModel(ctx, "gpt-5.5", session)
}

func withDefaultQuality(t *testing.T, snapshot map[int64]GroupRouteQualitySnapshot) {
	t.Helper()
	previous := DefaultGroupRouteQualityCache()
	t.Cleanup(func() { SetDefaultGroupRouteQualityCache(previous) })
	cache := NewGroupRouteQualityCache(&fakeRouteQualitySource{snapshot: snapshot})
	require.NoError(t, cache.Refresh(context.Background()))
	SetDefaultGroupRouteQualityCache(cache)
}

func TestSelectAlongKeyRoutes_NewSessionFollowsTheScoreAndBinds(t *testing.T) {
	// Group 2 is a quarter of the price of its neighbours: it leads for a new session.
	f := newRouteSelectionFixture(t, 1.0, 0.25, 1.0)
	routed, err := f.selectSession(context.Background(), "sess")
	require.NoError(t, err)
	require.Equal(t, int64(2), *routed.GroupID)
	require.Equal(t, []int64{2}, f.tried)
	require.Equal(t, int64(2), f.cache.bound(7, "gpt-5.5", "sess"), "the serving group is remembered")
}

func TestSelectAlongKeyRoutes_BoundSessionStaysEvenWhenAnotherGroupScoresHigher(t *testing.T) {
	f := newRouteSelectionFixture(t, 1.0, 0.25, 1.0)
	f.cache.bind(7, "gpt-5.5", "sess", 3)
	for i := 0; i < 5; i++ {
		routed, err := f.selectSession(context.Background(), "sess")
		require.NoError(t, err)
		require.Equal(t, int64(3), *routed.GroupID, "request %d", i+1)
		require.Equal(t, []int64{3}, f.tried, "the bound group is the only one tried")
	}
	require.Equal(t, int64(3), f.cache.bound(7, "gpt-5.5", "sess"))
}

func TestSelectAlongKeyRoutes_BoundGroupWithoutAccountsFallsBackByScoreAndRebinds(t *testing.T) {
	f := newRouteSelectionFixture(t, 1.0, 0.25, 1.0)
	f.cache.bind(7, "gpt-5.5", "sess", 3)
	f.serves[3] = false // the bound group cannot supply an account: an error, so judge again
	routed, err := f.selectSession(context.Background(), "sess")
	require.NoError(t, err)
	require.Equal(t, []int64{3, 2}, f.tried, "the bound group first, then the best of the rest")
	require.Equal(t, int64(2), *routed.GroupID)
	require.Equal(t, int64(2), f.cache.bound(7, "gpt-5.5", "sess"), "and the session moves with it")

	f.serves[3] = true // the old group recovers: the session stays where it now is
	routed, err = f.selectSession(context.Background(), "sess")
	require.NoError(t, err)
	require.Equal(t, int64(2), *routed.GroupID)
	require.Equal(t, []int64{2}, f.tried)
}

func TestSelectAlongKeyRoutes_FailoverRetryIsJudgedAgain(t *testing.T) {
	f := newRouteSelectionFixture(t, 1.0, 0.25, 1.0)
	f.cache.bind(7, "gpt-5.5", "sess", 3)
	routed, err := f.selectSession(ctxWithUpstreamFailure(103), "sess")
	require.NoError(t, err)
	require.Equal(t, []int64{2}, f.tried, "after an upstream error the binding is not honoured")
	require.Equal(t, int64(2), *routed.GroupID)
	require.Equal(t, int64(2), f.cache.bound(7, "gpt-5.5", "sess"))
}

func TestSelectAlongKeyRoutes_BindingNeverBypassesAuthorization(t *testing.T) {
	f := newRouteSelectionFixture(t, 1.0, 0.25, 1.0)
	f.groups[2].IsExclusive = true
	f.svc.schedulerSnapshot.storeCachedGroup(f.groups[2]) // the snapshot holds its own copy
	f.key.User.AllowedGroups = []int64{1, 2}              // access to group 3 was withdrawn after the session was bound
	f.cache.bind(7, "gpt-5.5", "sess", 3)
	routed, err := f.selectSession(context.Background(), "sess")
	require.NoError(t, err)
	require.NotContains(t, f.tried, int64(3), "an unauthorized group is never tried, bound or not")
	require.Equal(t, int64(2), *routed.GroupID)
	require.Equal(t, int64(2), f.cache.bound(7, "gpt-5.5", "sess"))
}

func TestSelectAlongKeyRoutes_BindingIsPerModel(t *testing.T) {
	f := newRouteSelectionFixture(t, 1.0, 0.25, 1.0)
	f.cache.bind(7, "gpt-5.5", "sess", 3)
	routed, err := f.selectModel(context.Background(), "gpt-5.4", "sess")
	require.NoError(t, err)
	require.Equal(t, int64(2), *routed.GroupID, "another model is a new decision")
	require.Equal(t, int64(3), f.cache.bound(7, "gpt-5.5", "sess"), "and does not disturb the first model's binding")
	require.Equal(t, int64(2), f.cache.bound(7, "gpt-5.4", "sess"))
}

func TestSelectAlongKeyRoutes_SessionlessRequestsAreJudgedByScoreOnly(t *testing.T) {
	f := newRouteSelectionFixture(t, 1.0, 0.25, 1.0)
	routed, err := f.selectSession(context.Background(), "")
	require.NoError(t, err)
	require.Equal(t, int64(2), *routed.GroupID)
	require.Zero(t, f.cache.gets+f.cache.sets)
}

func TestSelectAlongKeyRoutes_SingleGroupKeyNeverTouchesRedis(t *testing.T) {
	f := newRouteSelectionFixture(t, 1.0)
	_, err := f.selectSession(context.Background(), "sess")
	require.NoError(t, err)
	require.Zero(t, f.cache.gets+f.cache.sets)
}

func TestSelectAlongKeyRoutes_RedisFailureDoesNotBreakSelection(t *testing.T) {
	f := newRouteSelectionFixture(t, 1.0, 0.25, 1.0)
	f.cache.getErr, f.cache.setErr = errors.New("redis down"), errors.New("redis down")
	routed, err := f.selectSession(context.Background(), "sess")
	require.NoError(t, err)
	require.Equal(t, int64(2), *routed.GroupID, "judged by score, as if there were no binding")
}

func TestSelectAlongKeyRoutes_MonitorNumbersSteerNewSessions(t *testing.T) {
	// Same price; the monitor says group 1 is slow, erroring and cache-cold.
	f := newRouteSelectionFixture(t, 1.0, 1.0)
	withDefaultQuality(t, map[int64]GroupRouteQualitySnapshot{1: poorQuality(), 2: goodQuality()})
	routed, err := f.selectSession(context.Background(), "new")
	require.NoError(t, err)
	require.Equal(t, int64(2), *routed.GroupID)
}

func TestSelectAlongKeyRoutes_MonitorShowingTheBoundGroupFailingReleasesTheSession(t *testing.T) {
	f := newRouteSelectionFixture(t, 1.0, 1.0)
	withDefaultQuality(t, map[int64]GroupRouteQualitySnapshot{1: poorQuality(), 2: goodQuality()})
	f.cache.bind(7, "gpt-5.5", "sess", 1)
	routed, err := f.selectSession(context.Background(), "sess")
	require.NoError(t, err)
	require.Equal(t, []int64{2}, f.tried)
	require.Equal(t, int64(2), *routed.GroupID)
	require.Equal(t, int64(2), f.cache.bound(7, "gpt-5.5", "sess"))
}

func TestSelectAlongKeyRoutes_WithoutABindingStoreItStillScores(t *testing.T) {
	f := newRouteSelectionFixture(t, 1.0, 0.25, 1.0)
	f.svc.cache = &schedulerTestGatewayCache{} // a GatewayCache that cannot bind groups
	routed, err := f.selectSession(context.Background(), "sess")
	require.NoError(t, err)
	require.Equal(t, int64(2), *routed.GroupID)
}

// ---- selection through the Claude/Gemini path ------------------------------------------------------------------

func TestSelectAccountAlongKeyRoutes_GatewayBindsTheSessionAndKeepsIt(t *testing.T) {
	groups := []*Group{
		routeTestGroup(1, PlatformAnthropic, 1.0),
		routeTestGroup(2, PlatformAnthropic, 0.25), // a quarter of the price
		routeTestGroup(3, PlatformAnthropic, 1.0),
	}
	var accounts []*Account
	for _, g := range groups {
		accounts = append(accounts, routeTestAccount(1000+g.ID, PlatformAnthropic, g.ID, "claude-public"))
	}
	snapshot, _ := newSmartRouteCoreSnapshot(groups, accounts...)
	cache := newRouteBindingGatewayCache()
	svc := &GatewayService{
		accountRepo: &modelsListAccountRepoStub{}, schedulerSnapshot: snapshot, cfg: &config.Config{}, cache: cache,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}
	key := routeTestKey(groups...)
	selectOnce := func(ctx context.Context, session string) (*APIKey, int64) {
		ctx = ContextWithAPIKeyRoute(ctx, key)
		ctx, _ = WithGatewayTokenRequestPricing(ctx)
		result, routed, err := svc.SelectAccountAlongKeyRoutes(ctx, key, session, "claude-public", nil, "", key.UserID, PlatformAnthropic)
		require.NoError(t, err)
		require.NotNil(t, result)
		if result.ReleaseFunc != nil {
			defer result.ReleaseFunc()
		}
		return routed, result.Account.ID
	}

	// A new session goes to the much cheaper group and is bound to it.
	routed, accountID := selectOnce(context.Background(), "conv-1")
	require.Equal(t, int64(2), *routed.GroupID)
	require.Equal(t, int64(1002), accountID)
	require.Equal(t, int64(2), cache.bound(7, "claude-public", "conv-1"))

	// A session that was bound elsewhere stays there, whatever the score says.
	cache.bind(7, "claude-public", "conv-2", 3)
	for i := 0; i < 3; i++ {
		routed, accountID = selectOnce(context.Background(), "conv-2")
		require.Equal(t, int64(3), *routed.GroupID, "request %d", i+1)
		require.Equal(t, int64(1003), accountID)
	}

	// After an upstream error in this request the binding is not honoured.
	routed, _ = selectOnce(ctxWithUpstreamFailure(1003), "conv-2")
	require.Equal(t, int64(2), *routed.GroupID)
	require.Equal(t, int64(2), cache.bound(7, "claude-public", "conv-2"))
}

func TestAPIKeyRouteCandidateRemembersTheListEntryItCameFrom(t *testing.T) {
	primaryID, fallbackID := int64(1), int64(2)
	primary := &Group{ID: primaryID, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true}
	fallback := &Group{ID: fallbackID, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true}
	key := &APIKey{User: &User{ID: 7, Status: StatusActive, AllowedGroups: []int64{primaryID, fallbackID}}, GroupID: &primaryID, Group: primary, RouteGroupIDs: []int64{primaryID}}
	routes := newAPIKeyRouteIterator(context.Background(), key, []int64{primaryID}, "gpt-5",
		func(context.Context, *APIKey, int64) (*APIKey, error) { return key, nil },
		func(_ context.Context, routed *APIKey) (*APIKey, error) {
			return cloneAPIKeyWithGroupID(routed, fallback), nil
		},
		func(context.Context, *int64) groupModelsCatalog {
			return groupModelsCatalog{byPlatform: map[string][]string{PlatformOpenAI: {"gpt-5"}}, platforms: map[string]struct{}{PlatformOpenAI: {}}}
		},
		func(context.Context, *int64, string) (ChannelMappingResult, bool) {
			return ChannelMappingResult{}, false
		}, nil)
	candidate, ok := routes.next()
	require.True(t, ok)
	require.Equal(t, fallbackID, *candidate.key.GroupID, "billing and forwarding use the fallback group")
	require.Equal(t, primaryID, candidate.sourceGroupID, "the binding records the entry of the key's own list")
}
