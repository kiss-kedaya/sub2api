package repository

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

func loadGroupVideoModelPrices(ctx context.Context, sqlq sqlExecutor, groupIDs []int64) (map[int64]map[string]map[string]float64, error) {
	out := make(map[int64]map[string]map[string]float64, len(groupIDs))
	if sqlq == nil || len(groupIDs) == 0 {
		return out, nil
	}

	rows, err := sqlq.QueryContext(ctx, `
		SELECT id, video_model_prices
		FROM groups
		WHERE id = ANY($1) AND deleted_at IS NULL
	`, pq.Array(groupIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			groupID int64
			raw     []byte
		)
		if err := rows.Scan(&groupID, &raw); err != nil {
			return nil, err
		}
		prices, err := decodeVideoModelPrices(raw)
		if err != nil {
			return nil, err
		}
		if prices != nil {
			out[groupID] = prices
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func saveGroupVideoModelPrices(ctx context.Context, sqlq sqlExecutor, groupID int64, prices map[string]map[string]float64) error {
	if sqlq == nil || groupID <= 0 {
		return nil
	}
	normalized := service.NormalizeVideoModelPrices(prices)
	if len(normalized) == 0 {
		_, err := sqlq.ExecContext(ctx, `
			UPDATE groups
			SET video_model_prices = NULL
			WHERE id = $1
		`, groupID)
		return err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	_, err = sqlq.ExecContext(ctx, `
		UPDATE groups
		SET video_model_prices = $1::jsonb
		WHERE id = $2
	`, string(payload), groupID)
	return err
}

func applyVideoModelPricesToGroups(groups []service.Group, pricesByID map[int64]map[string]map[string]float64) {
	for i := range groups {
		if prices, ok := pricesByID[groups[i].ID]; ok {
			groups[i].VideoModelPrices = service.NormalizeVideoModelPrices(prices)
			continue
		}
		groups[i].VideoModelPrices = service.NormalizeVideoModelPrices(groups[i].VideoModelPrices)
	}
}

func applyVideoModelPricesToGroup(group *service.Group, pricesByID map[int64]map[string]map[string]float64) {
	if group == nil {
		return
	}
	if prices, ok := pricesByID[group.ID]; ok {
		group.VideoModelPrices = service.NormalizeVideoModelPrices(prices)
		return
	}
	group.VideoModelPrices = service.NormalizeVideoModelPrices(group.VideoModelPrices)
}

func decodeVideoModelPrices(raw []byte) (map[string]map[string]float64, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	// Driver may return NULL as nil slice; treat empty JSON as nil.
	trimmed := string(raw)
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var parsed map[string]map[string]float64
	if err := json.Unmarshal(raw, &parsed); err != nil {
		// Some drivers surface NULL via sql.NullString paths; tolerate empty object.
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return service.NormalizeVideoModelPrices(parsed), nil
}
