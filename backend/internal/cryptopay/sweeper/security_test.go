package sweeper

import (
	"context"
	"errors"
	"testing"
)

type incidentWallet struct {
	*fixtureWallet
	incident error
	checks   int
}

func (w *incidentWallet) ValidateSweepAuthorization(context.Context) error {
	w.checks++
	return w.incident
}

func TestIncidentStopsRebroadcastOfPreviouslySignedSweep(t *testing.T) {
	for _, gas := range []bool{false, true} {
		t.Run(map[bool]string{false: "token", true: "gas"}[gas], func(t *testing.T) {
			executor, _, chain, entry := setupExecutor(t)
			incident := errors.New("wallet incident lock")
			fixture, ok := executor.wallet.(*fixtureWallet)
			if !ok {
				t.Fatal("unexpected wallet fixture")
			}
			guard := &incidentWallet{fixtureWallet: fixture, incident: incident}
			executor.wallet = guard
			job := signedJob(chain, entry, gas)
			ok, err := executor.settle(t.Context(), executor.pools["bsc"], job, gas, 2)
			if ok || !errors.Is(err, incident) || guard.checks != 1 {
				t.Fatal("stored signed transaction bypassed wallet lock", ok, err, guard.checks)
			}
			if len(chain.raws) != 0 {
				t.Fatal("locked wallet broadcast a signed transaction")
			}
		})
	}
}
