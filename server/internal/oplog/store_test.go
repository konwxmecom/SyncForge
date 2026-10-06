package oplog

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRestoresOperationsFromLogAndCheckpoint(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	for sequence := 1; sequence <= checkpointEvery+3; sequence++ {
		message := []byte(fmt.Sprintf(`{"documentId":"doc","sequence":%d}`, sequence))
		if err := store.Append("doc", fmt.Sprintf("operation-%d", sequence), message); err != nil {
			t.Fatalf("append sequence %d: %v", sequence, err)
		}
	}

	restarted, err := Open(store.directory)
	if err != nil {
		t.Fatal(err)
	}
	records, err := restarted.Load("doc")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != checkpointEvery+3 {
		t.Fatalf("restored %d operations, want %d", len(records), checkpointEvery+3)
	}
	if records[99].Sequence != 100 || records[100].Sequence != 101 || records[len(records)-1].OperationID != "operation-103" {
		t.Fatalf("unexpected checkpoint/log boundary records: %#v, %#v, %#v", records[99], records[100], records[len(records)-1])
	}
}

func TestStoreRejectsInvalidDocumentIDAndCorruptRecord(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.Load("../outside"); err == nil {
		t.Fatal("Load accepted an invalid document ID")
	}
	path := filepath.Join(store.directory, documentFilename("doc")+".jsonl")
	if err := os.WriteFile(path, []byte("{not-json}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("doc"); err == nil {
		t.Fatal("Load accepted a corrupt complete log record")
	}
}

func TestOpenRejectsOverexposedDataDirectory(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(directory); err == nil {
		t.Fatal("Open accepted a data directory accessible by group or others")
	}
}

func TestStoreDeduplicatesOperationIDsAndRejectsConflictingReuse(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	message := []byte(`{"documentId":"doc","value":"x"}`)
	if err := store.Append("doc", "operation-1", message); err != nil {
		t.Fatal(err)
	}
	if err := store.Append("doc", "operation-1", message); err != nil {
		t.Fatalf("identical operation retry was not idempotent: %v", err)
	}
	if err := store.Append("doc", "operation-1", []byte(`{"documentId":"doc","value":"y"}`)); err == nil {
		t.Fatal("conflicting operation ID reuse was accepted")
	}
	records, err := store.Load("doc")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("stored %d records for one operation ID", len(records))
	}
}

func TestStoreRecoversIncompleteFinalLogRecordBeforeAppending(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append("doc", "operation-1", []byte(`{"documentId":"doc","sequence":1}`)); err != nil {
		t.Fatal(err)
	}
	logPath := store.logPath("doc")
	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte(`{"sequence":2`)); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := Open(store.directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Append("doc", "operation-2", []byte(`{"documentId":"doc","sequence":2}`)); err != nil {
		t.Fatal(err)
	}
	records, err := restarted.Load("doc")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[1].Sequence != 2 || records[1].OperationID != "operation-2" {
		t.Fatalf("incomplete log tail was not safely recovered: %#v", records)
	}
}
