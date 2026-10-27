package requestcapture

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func (m *Manager) OpenPart(ctx context.Context, task, id, name string) (*os.File, *Part, error) {
	r, err := m.Record(ctx, task, id)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range r.Parts {
		if p.Name == name && filepath.Base(name) == name && strings.HasSuffix(name, ".txt") {
			file, err := os.Open(filepath.Join(m.dir, task, id, name))
			return file, &p, err
		}
	}
	return nil, nil, ErrNotFound
}
func (m *Manager) Preview(ctx context.Context, task, id, name string, offset int64) (string, int64, bool, error) {
	if offset < 0 {
		return "", 0, false, fmt.Errorf("negative offset")
	}
	file, part, err := m.OpenPart(ctx, task, id, name)
	if err != nil {
		return "", 0, false, err
	}
	defer func() { _ = file.Close() }()
	if offset > part.Bytes {
		offset = part.Bytes
	}
	if _, err = file.Seek(offset, io.SeekStart); err != nil {
		return "", 0, false, err
	}
	n := part.Bytes - offset
	if n > PreviewLimit {
		n = PreviewLimit
	}
	raw, err := io.ReadAll(io.LimitReader(file, n))
	if offset+int64(len(raw)) < part.Bytes {
		for cut := 0; cut < utf8.UTFMax && len(raw) > 0 && !utf8.Valid(raw); cut++ {
			raw = raw[:len(raw)-1]
		}
	}
	return string(raw), offset + int64(len(raw)), offset+int64(len(raw)) < part.Bytes, err
}
func (m *Manager) Export(ctx context.Context, w io.Writer, taskID, recordID string) error {
	t, err := m.Task(ctx, taskID)
	if err != nil {
		return err
	}
	if t.Status == "running" {
		return ErrFinalizing
	}
	m.mu.Lock()
	rt := m.tasks[taskID]
	pending := rt != nil && rt.refs > 0
	m.mu.Unlock()
	if pending {
		return ErrFinalizing
	}
	select {
	case m.exports <- struct{}{}:
		defer func() { <-m.exports }()
	default:
		return ErrCapacity
	}
	m.exportMu.RLock()
	defer m.exportMu.RUnlock()
	var single *Record
	if recordID != "" {
		single, err = m.Record(ctx, taskID, recordID)
		if err != nil {
			return err
		}
	}
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	// Finish gzip only on success so interrupted archives cannot look complete.
	writeJSON := func(name string, v any) error {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		if err = tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(b))}); err != nil {
			return err
		}
		_, err = tw.Write(b)
		return err
	}
	if err = writeJSON("task.json", t); err != nil {
		return err
	}
	writeRecord := func(r *Record) error {
		if err := writeJSON(r.ID+"/record.json", r); err != nil {
			return err
		}
		for _, p := range r.Parts {
			if p.Bytes == 0 {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if filepath.Base(p.Name) != p.Name {
				return ErrNotFound
			}
			file, err := os.Open(filepath.Join(m.dir, taskID, r.ID, p.Name))
			if err != nil {
				return err
			}
			if err = tw.WriteHeader(&tar.Header{Name: r.ID + "/" + p.Name, Mode: 0600, Size: p.Bytes}); err == nil {
				_, err = io.CopyN(tw, file, p.Bytes)
			}
			_ = file.Close()
			if err != nil {
				return err
			}
		}
		return nil
	}
	if recordID != "" {
		if err = writeRecord(single); err != nil {
			return err
		}
	} else {
		for offset := 0; ; offset += 20 {
			records, err := m.Records(ctx, taskID, "", false, 20, offset)
			if err != nil {
				return err
			}
			for _, summary := range records {
				r, err := m.Record(ctx, taskID, summary.ID)
				if err != nil {
					return err
				}
				if err = writeRecord(r); err != nil {
					return err
				}
			}
			if len(records) < 20 {
				break
			}
		}
	}
	if err = tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}
