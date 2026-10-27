package requestcapture

import (
	"context"
	"database/sql"
	"encoding/json"
)

type SQLStore struct{ DB *sql.DB }

func (s *SQLStore) SaveTask(ctx context.Context, t *Task) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO request_capture_tasks(id,instance_id,created_at,data) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO UPDATE SET data=EXCLUDED.data`, t.ID, t.InstanceID, t.CreatedAt, string(b))
	return err
}
func (s *SQLStore) Tasks(ctx context.Context, instance string, limit, offset int) ([]Task, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT data FROM request_capture_tasks WHERE instance_id=$1 ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3`, instance, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Task{}
	for rows.Next() {
		var b []byte
		var t Task
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *SQLStore) SaveRecord(ctx context.Context, r *Record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO request_capture_records(id,task_id,request_id,is_error,created_at,data) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(id) DO UPDATE SET request_id=EXCLUDED.request_id,is_error=EXCLUDED.is_error,data=EXCLUDED.data`, r.ID, r.TaskID, r.RequestID, r.IsError, r.CreatedAt, string(b))
	return err
}
func (s *SQLStore) Records(ctx context.Context, task, requestID string, onlyErrors bool, limit, offset int) ([]Record, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT data - 'parts' - 'attempts' - 'usage' FROM request_capture_records WHERE task_id=$1 AND ($2='' OR request_id=$2) AND ($3=FALSE OR (is_error=TRUE AND data->>'finished_at' IS NOT NULL)) ORDER BY created_at DESC,id DESC LIMIT $4 OFFSET $5`, task, requestID, onlyErrors, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Record{}
	for rows.Next() {
		var b []byte
		var r Record
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *SQLStore) Record(ctx context.Context, task, id string) (*Record, error) {
	var b []byte
	if err := s.DB.QueryRowContext(ctx, `SELECT data FROM request_capture_records WHERE task_id=$1 AND id=$2`, task, id).Scan(&b); err != nil {
		return nil, err
	}
	var r Record
	err := json.Unmarshal(b, &r)
	return &r, err
}
func (s *SQLStore) DeleteTask(ctx context.Context, instance, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM request_capture_tasks WHERE instance_id=$1 AND id=$2`, instance, id)
	return err
}
func (s *SQLStore) DeleteRecord(ctx context.Context, task, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM request_capture_records WHERE task_id=$1 AND id=$2`, task, id)
	return err
}

func (s *SQLStore) Task(ctx context.Context, instance, id string) (*Task, error) {
	var b []byte
	if err := s.DB.QueryRowContext(ctx, `SELECT data FROM request_capture_tasks WHERE instance_id=$1 AND id=$2`, instance, id).Scan(&b); err != nil {
		return nil, err
	}
	var t Task
	err := json.Unmarshal(b, &t)
	return &t, err
}
