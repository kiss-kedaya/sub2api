package cryptopay

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

func (s *Service) enrichBitgetRecords(ctx context.Context, records []SweepRecord) error {
	if len(records) == 0 || s.Config().SweepProvider != "bitget" {
		return nil
	}
	args := make([]any, 0, len(records))
	params := make([]string, 0, len(records))
	indices := map[int64]int{}
	for i, r := range records {
		indices[r.ID] = i
		args = append(args, r.ID)
		params = append(params, "$"+strconv.Itoa(len(args)))
	}
	rows, err := s.db.QueryContext(ctx, `SELECT job_id,order_id,intent->>'fee',intent->>'net',intent->>'decimals' FROM crypto_bitget_sweep_jobs WHERE job_id IN (`+strings.Join(params, ",")+`)`, args...)
	if err != nil {
		return fmt.Errorf("load gasless sweep details")
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var order, fee, net string
		var decimals int
		if err = rows.Scan(&id, &order, &fee, &net, &decimals); err != nil {
			return err
		}
		if i, ok := indices[id]; ok {
			records[i].Provider = "bitget"
			records[i].ProviderOrderID = order
			records[i].FeeAmount = rawDisplay(fee, decimals)
			records[i].NetAmount = rawDisplay(net, decimals)
		}
	}
	return rows.Err()
}

func rawDisplay(s string, decimals int) string {
	if decimals < 0 || decimals > 18 || len(s) > 78 {
		return ""
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return ""
		}
	}
	for len(s) <= decimals {
		s = "0" + s
	}
	if decimals == 0 {
		return s
	}
	return trimDecimalZeros(s[:len(s)-decimals] + "." + s[len(s)-decimals:])
}
