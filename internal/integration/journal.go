package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

const maxJournalBytes = 64 << 20

type snapshot struct {
	Version       int    `json:"version"`
	SourceID      string `json:"source_id"`
	DestinationID string `json:"destination_id"`
	Jobs          []Job  `json:"jobs"`
}
type journal struct {
	directory string
	lock      *os.File
}

func openJournal(cfg Config) (*journal, snapshot, error) {
	directory, err := filepath.Abs(cfg.Directory)
	if err != nil || directory == filepath.VolumeName(directory)+string(filepath.Separator) {
		return nil, snapshot{}, ErrInvalidInput
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, snapshot{}, ErrStore
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, snapshot{}, ErrStateInvalid
	}
	lockPath := filepath.Join(directory, "state.lock")
	if err := privateRegularFile(lockPath); err != nil {
		return nil, snapshot{}, err
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, snapshot{}, ErrStore
	}
	if err := lockFile(lock); err != nil {
		_ = lock.Close()
		return nil, snapshot{}, err
	}
	j := &journal{directory: directory, lock: lock}
	fail := func(err error) (*journal, snapshot, error) { _ = j.close(); return nil, snapshot{}, err }
	path := filepath.Join(directory, "state.json")
	if err := privateRegularFile(path); err != nil {
		return fail(err)
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		data := snapshot{Version: 1, SourceID: cfg.SourceID, DestinationID: cfg.DestinationID, Jobs: []Job{}}
		if err := j.save(data); err != nil {
			return fail(ErrStore)
		}
		return j, data, nil
	}
	if err != nil {
		return fail(ErrStore)
	}
	encoded, readErr := io.ReadAll(io.LimitReader(file, maxJournalBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return fail(ErrStore)
	}
	if len(encoded) > maxJournalBytes {
		return fail(ErrStateInvalid)
	}
	var data snapshot
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&data); err != nil {
		return fail(ErrStateInvalid)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fail(ErrStateInvalid)
	}
	if !validSnapshot(cfg, data) {
		return fail(ErrStateInvalid)
	}
	return j, data, nil
}
func privateRegularFile(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return ErrStore
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return ErrStateInvalid
	}
	return nil
}
func (j *journal) save(data snapshot) error {
	encoded, err := json.Marshal(data)
	if err != nil || len(encoded) > maxJournalBytes {
		return ErrStore
	}
	file, err := os.CreateTemp(j.directory, ".state-*.tmp")
	if err != nil {
		return ErrStore
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		_ = file.Close()
		return ErrStore
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return ErrStore
	}
	if err := file.Close(); err != nil {
		return ErrStore
	}
	if err := os.Rename(file.Name(), filepath.Join(j.directory, "state.json")); err != nil {
		return ErrStore
	}
	directory, err := os.Open(j.directory)
	if err != nil {
		return ErrStore
	}
	syncErr, closeErr := directory.Sync(), directory.Close()
	if syncErr != nil || closeErr != nil {
		return ErrStore
	}
	return nil
}
func (j *journal) close() error {
	if err := j.lock.Close(); err != nil {
		return ErrStore
	}
	return nil
}

func validSnapshot(cfg Config, data snapshot) bool {
	if data.Version != 1 || data.SourceID != cfg.SourceID || data.DestinationID != cfg.DestinationID ||
		data.Jobs == nil || len(data.Jobs) > MaxJobs {
		return false
	}
	seen := map[string]bool{}
	for _, j := range data.Jobs {
		if !validEvent(j.Event) || j.ID != jobID(cfg.SourceID, j.Event) || seen[j.ID] ||
			j.Attempts < 0 || j.Attempts > 10 || !validFailure(j.LastFailure) {
			return false
		}
		seen[j.ID] = true
		state := j.State
		if state == NeedsAttention {
			if j.LastFailure == "" || !j.NextAttemptAt.IsZero() {
				return false
			}
			state = j.AttentionFrom
		} else if j.AttentionFrom != "" {
			return false
		}
		switch state {
		case PendingReceive:
			if j.TriggerMessageID != "" || j.ReplyMessageID != "" || j.ReplyContent != "" || j.DeliveryKey != "" {
				return false
			}
		case PendingReply:
			if !validID(j.TriggerMessageID) || j.ReplyMessageID != "" || j.ReplyContent != "" || j.DeliveryKey != "" {
				return false
			}
		case PendingDelivery, Completed:
			if !validID(j.TriggerMessageID) || !validID(j.ReplyMessageID) || j.TriggerMessageID == j.ReplyMessageID ||
				!validText(j.ReplyContent, MaxReplyBytes) || j.DeliveryKey != deliveryKey(cfg, j) {
				return false
			}
			if state == Completed && (j.State == NeedsAttention || j.Attempts != 0 || j.LastFailure != "" || !j.NextAttemptAt.IsZero()) {
				return false
			}
		default:
			return false
		}
	}
	return true
}
