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
	Version       int               `json:"version"`
	SourceID      string            `json:"source_id"`
	DestinationID string            `json:"destination_id"`
	Jobs          []Job             `json:"jobs"`
	Audit         []ResolutionAudit `json:"audit,omitempty"`
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
		data := snapshot{Version: 2, SourceID: cfg.SourceID, DestinationID: cfg.DestinationID, Jobs: []Job{}, Audit: []ResolutionAudit{}}
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
	if data.Version == 1 {
		// Existing attempts did not distinguish eligibility from delivery calls.
		// Preserve uncertainty and do not infer permission to retry from old codes.
		data.Version = 2
		data.Audit = []ResolutionAudit{}
		for i := range data.Jobs {
			job := &data.Jobs[i]
			job.Revision, job.LegacyHistory, job.TotalAttempts = 1, true, job.Attempts
			job.MayHaveDelivered = job.State == Completed ||
				((job.State == PendingDelivery || job.AttentionFrom == PendingDelivery) && job.Attempts > 0)
		}
		if !validSnapshot(cfg, data) {
			return fail(ErrStateInvalid)
		}
		if err := j.save(data); err != nil {
			return fail(ErrStore)
		}
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
	if (data.Version != 1 && data.Version != 2) || data.SourceID != cfg.SourceID || data.DestinationID != cfg.DestinationID ||
		data.Jobs == nil || len(data.Jobs) > MaxJobs {
		return false
	}
	seen := map[string]bool{}
	for _, j := range data.Jobs {
		if !validEvent(j.Event) || j.ID != jobID(cfg.SourceID, j.Event) || seen[j.ID] ||
			j.Attempts < 0 || j.Attempts > 10+MaxManualRetries || !validFailure(j.LastFailure) {
			return false
		}
		seen[j.ID] = true
		if data.Version == 1 {
			if j.Attempts > 10 || j.Revision != 0 || j.TotalAttempts != 0 || j.DeliveryAttempts != 0 || j.MayHaveDelivered ||
				j.LegacyHistory || j.RecoveryAllowed || j.ManualAttempt || j.ManualInFlight || !j.RetryNotBefore.IsZero() || j.ManualRetries != 0 || j.State == Stopped || len(data.Audit) != 0 {
				return false
			}
		} else if !validRecoveryMetadata(j) {
			return false
		}
		state := j.State
		if state == NeedsAttention || state == Stopped {
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
			if state == Completed && (j.State == NeedsAttention || j.State == Stopped || j.Attempts != 0 || j.LastFailure != "" || !j.NextAttemptAt.IsZero()) {
				return false
			}
		default:
			return false
		}
	}
	return data.Version == 1 && len(data.Audit) == 0 || data.Version == 2 && validAudit(data)
}

func validRecoveryMetadata(j Job) bool {
	if j.Revision == 0 || j.Revision == ^uint64(0) || j.TotalAttempts < j.Attempts || j.TotalAttempts > 30+MaxManualRetries ||
		j.DeliveryAttempts < 0 || j.DeliveryAttempts > j.TotalAttempts || j.ManualRetries < 0 || j.ManualRetries > MaxManualRetries {
		return false
	}
	if j.DeliveryAttempts > 0 && !j.MayHaveDelivered || j.MayHaveDelivered && j.DeliveryAttempts == 0 && !j.LegacyHistory {
		return false
	}
	stage := j.State
	if stage == NeedsAttention || stage == Stopped {
		stage = j.AttentionFrom
	}
	if (j.MayHaveDelivered || j.DeliveryAttempts > 0) && stage != PendingDelivery && stage != Completed {
		return false
	}
	if j.State == Completed && !j.MayHaveDelivered {
		return false
	}
	if j.RecoveryAllowed && (j.State != NeedsAttention || !retryFailureAllowed(j.LastFailure)) {
		return false
	}
	if j.ManualAttempt && (j.ManualRetries == 0 || (j.State != PendingReceive && j.State != PendingReply && j.State != PendingDelivery)) {
		return false
	}
	if j.ManualInFlight && (j.ManualAttempt || j.ManualRetries == 0 || j.Attempts == 0 || (j.State != PendingReceive && j.State != PendingReply && j.State != PendingDelivery)) {
		return false
	}
	if j.State == Completed && !j.RetryNotBefore.IsZero() {
		return false
	}
	return true
}

func validAudit(data snapshot) bool {
	if len(data.Audit) > MaxJobs*(MaxManualRetries+1) {
		return false
	}
	jobs := map[string]Job{}
	for _, j := range data.Jobs {
		jobs[j.ID] = j
	}
	operations := map[string]bool{}
	counts := map[string]int{}
	lastRevision := map[string]uint64{}
	terminal := map[string]ResolutionAudit{}
	for _, entry := range data.Audit {
		q := entry.Command
		j, ok := jobs[q.JobID]
		if !ok || !validResolution(q) || operations[q.OperationID] || entry.At.IsZero() ||
			entry.Revision != q.ExpectedRevision+1 || entry.Revision > j.Revision || q.ExpectedRevision <= lastRevision[j.ID] ||
			entry.Failure == "" || !validFailure(entry.Failure) || entry.Attempts < 0 || entry.Attempts > 10+MaxManualRetries ||
			entry.TotalAttempts < entry.Attempts || entry.TotalAttempts > j.TotalAttempts || entry.DeliveryAttempts < 0 || entry.DeliveryAttempts > entry.TotalAttempts ||
			(entry.FromStage != PendingReceive && entry.FromStage != PendingReply && entry.FromStage != PendingDelivery) {
			return false
		}
		if _, exists := terminal[j.ID]; exists {
			return false
		}
		if entry.MayHaveDelivered && !j.MayHaveDelivered || entry.DeliveryAttempts > 0 && !entry.MayHaveDelivered {
			return false
		}
		operations[q.OperationID] = true
		lastRevision[j.ID] = entry.Revision
		if entry.ManualRetriesBefore != counts[j.ID] {
			return false
		}
		switch q.Action {
		case RetryOnce:
			counts[j.ID]++
			if entry.ToState != entry.FromStage || !retryFailureAllowed(entry.Failure) || counts[j.ID] > MaxManualRetries {
				return false
			}
		case ConfirmDelivered:
			if entry.FromStage != PendingDelivery || !entry.MayHaveDelivered || entry.ToState != Completed {
				return false
			}
			terminal[j.ID] = entry
		case StopProcessing:
			if entry.ToState != Stopped {
				return false
			}
			terminal[j.ID] = entry
		}
		if entry.ManualRetriesAfter != counts[j.ID] {
			return false
		}
	}
	for _, j := range data.Jobs {
		if counts[j.ID] != j.ManualRetries {
			return false
		}
		if entry, ok := terminal[j.ID]; ok {
			if j.State != entry.ToState || j.Revision != entry.Revision {
				return false
			}
			if j.State == Stopped && (j.LastFailure != entry.Failure || j.AttentionFrom != entry.FromStage) {
				return false
			}
		} else if j.State == Stopped {
			return false
		}
	}
	return true
}
