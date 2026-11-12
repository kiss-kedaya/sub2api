package ledger

import (
	"context"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/nativeprice"
	"github.com/shopspring/decimal"
)

// stubPricer is the deterministic market feed used by the native-credit
// tests. It never touches the network; each test chooses the quote or the
// failure it wants to exercise.
type stubPricer struct {
	price decimal.Decimal
	at    time.Time
	err   error
	calls int
	// chain records the last chain asked for, so a test can prove a
	// stablecoin deposit never consulted the feed.
	chain string
}

func (p *stubPricer) Quote(_ context.Context, chain string) (nativeprice.Quote, error) {
	p.calls++
	p.chain = chain
	if p.err != nil {
		return nativeprice.Quote{}, p.err
	}
	at := p.at
	if at.IsZero() {
		at = time.Now()
	}
	return nativeprice.Quote{USD: p.price, At: at, Source: "stub"}, nil
}

// fixedPricer returns a usable quote for tests that only care that a native
// deposit can be credited at all.
func fixedPricer(price int64) *stubPricer {
	return &stubPricer{price: decimal.NewFromInt(price)}
}

var errTestPrice = errors.New("test price feed is down")
