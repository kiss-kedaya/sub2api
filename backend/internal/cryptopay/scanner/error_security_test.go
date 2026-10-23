package scanner

import (
	"strings"
	"testing"
)

func TestReceiptQuantityErrorsNeverEchoRPCInput(t *testing.T) {
	for _, input := range []string{"fixture-upstream-private-message", "0xfixture-upstream-private-message"} {
		_, err := hexQuantity(input)
		if err == nil || strings.Contains(err.Error(), "fixture-") {
			t.Fatal("receipt quantity parse error echoed upstream input")
		}
	}
}
