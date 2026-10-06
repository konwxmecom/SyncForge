package oplog

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

const checkpointEvery = 100

var documentIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

type Record struct {
	Sequence    uint64          `json:"sequence"`
	OperationID string          `json:"operationId"`
	Message     json.RawMessage `json:"message"`
}

type checkpoint struct {
	DocumentID string   `json:"documentId"`
	Sequence   uint64   `json:"sequence"`
	Operations []Record `json:"operations"`
}

type Store struct {
	directory  string
	mu         sync.Mutex
	sequences  map[string]uint64
	operations map[string]map[string][]byte
}

func Open(directory string) (*Store, error) {
	if directory == "" {
		return nil, errors.New("operation log directory must not be empty")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create operation log directory: %w", err)
	}
	info, err := os.Stat(directory)
	if err != nil {
		return nil, fmt.Errorf("inspect operation log directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("operation log path %q is not a directory", directory)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("operation log directory must not be accessible by group or others")
	}
	return &Store{
		directory:  directory,
		sequences:  make(map[string]uint64),
		operations: make(map[string]map[string][]byte),
	}, nil
}

func (store *Store) Load(documentID string) ([]Record, error) {
	if !documentIDPattern.MatchString(documentID) {
		return nil, errors.New("invalid document ID")
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	records := make([]Record, 0)
	checkpointSequence := uint64(0)
	checkpointPath := store.checkpointPath(documentID)
	rawCheckpoint, err := os.ReadFile(checkpointPath)
	if err == nil {
		var saved checkpoint
		if err := json.Unmarshal(rawCheckpoint, &saved); err != nil {
			return nil, fmt.Errorf("decode checkpoint for %q: %w", documentID, err)
		}
		if saved.DocumentID != documentID || saved.Sequence != uint64(len(saved.Operations)) {
			return nil, fmt.Errorf("checkpoint for %q has inconsistent metadata", documentID)
		}
		if err := validateRecords(saved.Operations, documentID, 0); err != nil {
			return nil, fmt.Errorf("validate checkpoint for %q: %w", documentID, err)
		}
		records = append(records, saved.Operations...)
		checkpointSequence = saved.Sequence
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read checkpoint for %q: %w", documentID, err)
	}

	logFile, err := os.Open(store.logPath(documentID))
	if errors.Is(err, os.ErrNotExist) {
		index, err := indexRecords(records)
		if err != nil {
			return nil, err
		}
		store.sequences[documentID] = uint64(len(records))
		store.operations[documentID] = index
		return records, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open operation log for %q: %w", documentID, err)
	}
	defer logFile.Close()

	reader := bufio.NewReader(logFile)
	expectedSequence := checkpointSequence + 1
	for {
		line, readErr := reader.ReadBytes('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, fmt.Errorf("read operation log for %q: %w", documentID, readErr)
		}
		if len(bytes.TrimSpace(line)) > 0 {
			if errors.Is(readErr, io.EOF) {
				break
			}
			var record Record
			if err := json.Unmarshal(line, &record); err != nil {
				return nil, fmt.Errorf("decode operation log for %q at sequence %d: %w", documentID, expectedSequence, err)
			}
			if record.Sequence < expectedSequence {
				continue
			}
			if record.Sequence != expectedSequence || len(record.Message) == 0 || record.OperationID == "" {
				return nil, fmt.Errorf("operation log for %q has invalid sequence %d, expected %d", documentID, record.Sequence, expectedSequence)
			}
			if err := validateRecords([]Record{record}, documentID, record.Sequence-1); err != nil {
				return nil, fmt.Errorf("validate operation log for %q: %w", documentID, err)
			}
			records = append(records, record)
			expectedSequence++
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	index, err := indexRecords(records)
	if err != nil {
		return nil, err
	}
	store.sequences[documentID] = uint64(len(records))
	store.operations[documentID] = index
	return records, nil
}

func (store *Store) Append(documentID, operationID string, message []byte) error {
	if !documentIDPattern.MatchString(documentID) || operationID == "" || len(message) == 0 {
		return errors.New("invalid operation log record")
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	sequence, cached := store.sequences[documentID]
	var records []Record
	if !cached {
		loaded, err := store.loadLocked(documentID)
		if err != nil {
			return err
		}
		records = loaded
		sequence = uint64(len(records))
	}
	if previous, exists := store.operations[documentID][operationID]; exists {
		if bytes.Equal(previous, message) {
			store.sequences[documentID] = sequence
			return nil
		}
		return fmt.Errorf("operation ID %q was reused with different content", operationID)
	}
	if err := store.removeIncompleteTail(documentID); err != nil {
		return err
	}
	record := Record{
		Sequence:    sequence + 1,
		OperationID: operationID,
		Message:     append(json.RawMessage(nil), message...),
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode operation log record: %w", err)
	}
	file, err := os.OpenFile(store.logPath(documentID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open operation log for append: %w", err)
	}
	line := append(encoded, '\n')
	if written, err := file.Write(line); err != nil {
		_ = file.Close()
		delete(store.sequences, documentID)
		return fmt.Errorf("append operation log record: %w", err)
	} else if written != len(line) {
		_ = file.Close()
		delete(store.sequences, documentID)
		return io.ErrShortWrite
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		delete(store.sequences, documentID)
		return fmt.Errorf("sync operation log record: %w", err)
	}
	if err := file.Close(); err != nil {
		delete(store.sequences, documentID)
		return fmt.Errorf("close operation log: %w", err)
	}
	if sequence == 0 {
		if err := store.syncDirectory(); err != nil {
			return err
		}
	}

	store.sequences[documentID] = record.Sequence
	if store.operations[documentID] == nil {
		store.operations[documentID] = make(map[string][]byte)
	}
	store.operations[documentID][operationID] = append([]byte(nil), message...)
	if record.Sequence%checkpointEvery == 0 {
		records, err := store.loadLocked(documentID)
		if err != nil {
			log.Printf("Unable to build snapshot for document %q after durable append: %v", documentID, err)
			return nil
		}
		if err := store.writeCheckpoint(documentID, records); err != nil {
			log.Printf("Unable to persist snapshot for document %q after durable append: %v", documentID, err)
		}
	}
	return nil
}

func (store *Store) loadLocked(documentID string) ([]Record, error) {
	records := make([]Record, 0)
	checkpointSequence := uint64(0)
	rawCheckpoint, err := os.ReadFile(store.checkpointPath(documentID))
	if err == nil {
		var saved checkpoint
		if err := json.Unmarshal(rawCheckpoint, &saved); err != nil {
			return nil, fmt.Errorf("decode checkpoint for %q: %w", documentID, err)
		}
		if saved.DocumentID != documentID || saved.Sequence != uint64(len(saved.Operations)) {
			return nil, fmt.Errorf("checkpoint for %q has inconsistent metadata", documentID)
		}
		if err := validateRecords(saved.Operations, documentID, 0); err != nil {
			return nil, fmt.Errorf("validate checkpoint for %q: %w", documentID, err)
		}
		records = append(records, saved.Operations...)
		checkpointSequence = saved.Sequence
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read checkpoint for %q: %w", documentID, err)
	}
	logFile, err := os.Open(store.logPath(documentID))
	if errors.Is(err, os.ErrNotExist) {
		index, err := indexRecords(records)
		if err != nil {
			return nil, err
		}
		store.operations[documentID] = index
		return records, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open operation log for %q: %w", documentID, err)
	}
	defer logFile.Close()
	reader := bufio.NewReader(logFile)
	expectedSequence := checkpointSequence + 1
	for {
		line, readErr := reader.ReadBytes('\n')
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("read operation log: %w", readErr)
		}
		var record Record
		if err := json.Unmarshal(line, &record); err != nil {
			return nil, fmt.Errorf("decode operation log record: %w", err)
		}
		if record.Sequence < expectedSequence {
			continue
		}
		if record.Sequence != expectedSequence || len(record.Message) == 0 || record.OperationID == "" {
			return nil, fmt.Errorf("operation log has invalid sequence %d, expected %d", record.Sequence, expectedSequence)
		}
		if err := validateRecords([]Record{record}, documentID, record.Sequence-1); err != nil {
			return nil, fmt.Errorf("validate operation log for %q: %w", documentID, err)
		}
		if err := validateRecords([]Record{record}, documentID, record.Sequence-1); err != nil {
			return nil, fmt.Errorf("validate operation log for %q: %w", documentID, err)
		}
		records = append(records, record)
		expectedSequence++
	}
	index, err := indexRecords(records)
	if err != nil {
		return nil, err
	}
	store.operations[documentID] = index
	return records, nil
}

func indexRecords(records []Record) (map[string][]byte, error) {
	index := make(map[string][]byte, len(records))
	for _, record := range records {
		if previous, exists := index[record.OperationID]; exists {
			if bytes.Equal(previous, record.Message) {
				return nil, fmt.Errorf("operation log contains duplicate operation ID %q", record.OperationID)
			}
			return nil, fmt.Errorf("operation log contains conflicting operation ID %q", record.OperationID)
		}
		index[record.OperationID] = append([]byte(nil), record.Message...)
	}
	return index, nil
}

func (store *Store) removeIncompleteTail(documentID string) error {
	path := store.logPath(documentID)
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open operation log for tail recovery: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect operation log for tail recovery: %w", err)
	}
	if info.Size() == 0 {
		return nil
	}
	var lastByte [1]byte
	if _, err := file.ReadAt(lastByte[:], info.Size()-1); err != nil {
		return fmt.Errorf("read operation log tail: %w", err)
	}
	if lastByte[0] == '\n' {
		return nil
	}
	const chunkSize = 4096
	buffer := make([]byte, chunkSize)
	end := info.Size()
	for end > 0 {
		start := end - chunkSize
		if start < 0 {
			start = 0
		}
		chunk := buffer[:end-start]
		if _, err := file.ReadAt(chunk, start); err != nil {
			return fmt.Errorf("read operation log for tail recovery: %w", err)
		}
		if newline := bytes.LastIndexByte(chunk, '\n'); newline >= 0 {
			end = start + int64(newline) + 1
			break
		}
		end = start
	}
	if err := file.Truncate(end); err != nil {
		return fmt.Errorf("truncate incomplete operation log tail: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync recovered operation log: %w", err)
	}
	return nil
}

func (store *Store) writeCheckpoint(documentID string, records []Record) error {
	saved := checkpoint{
		DocumentID: documentID,
		Sequence:   uint64(len(records)),
		Operations: records,
	}
	encoded, err := json.Marshal(saved)
	if err != nil {
		return fmt.Errorf("encode checkpoint for %q: %w", documentID, err)
	}
	temporary, err := os.CreateTemp(store.directory, ".checkpoint-*")
	if err != nil {
		return fmt.Errorf("create checkpoint temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set checkpoint permissions: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write checkpoint: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync checkpoint: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close checkpoint: %w", err)
	}
	if err := os.Rename(temporaryPath, store.checkpointPath(documentID)); err != nil {
		return fmt.Errorf("replace checkpoint: %w", err)
	}
	return store.syncDirectory()
}

func validateRecords(records []Record, documentID string, start uint64) error {
	expectedSequence := start + 1
	for _, record := range records {
		if record.Sequence != expectedSequence || record.OperationID == "" || len(record.Message) == 0 {
			return fmt.Errorf("invalid operation record at sequence %d", expectedSequence)
		}
		var message struct {
			DocumentID string `json:"documentId"`
		}
		if err := json.Unmarshal(record.Message, &message); err != nil {
			return err
		}
		if message.DocumentID != documentID {
			return fmt.Errorf("operation record at sequence %d belongs to a different document", expectedSequence)
		}
		expectedSequence++
	}
	return nil
}

func (store *Store) logPath(documentID string) string {
	return filepath.Join(store.directory, documentFilename(documentID)+".jsonl")
}

func (store *Store) checkpointPath(documentID string) string {
	return filepath.Join(store.directory, documentFilename(documentID)+".snapshot.json")
}

func documentFilename(documentID string) string {
	hash := sha256.Sum256([]byte(documentID))
	return fmt.Sprintf("%x", hash)
}

func (store *Store) syncDirectory() error {
	directory, err := os.Open(store.directory)
	if err != nil {
		return fmt.Errorf("open operation log directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync operation log directory: %w", err)
	}
	return nil
}
