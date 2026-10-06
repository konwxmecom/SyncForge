package protocol

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSharedJoinExample(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "..", "protocol", "examples", "join.v1.json")
	input, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}

	message, err := ParseMessage(input)
	if err != nil {
		t.Fatal(err)
	}
	if message.ProtocolVersion != 1 || message.Type != "join" || message.DocumentID != "demo-doc" || message.ReplicaID != "browser-tab-a" {
		t.Fatalf("unexpected message: %+v", message)
	}
}

func TestParseJoinWithOptionalAuthToken(t *testing.T) {
	message, err := ParseMessage([]byte(`{"protocolVersion":1,"type":"join","documentId":"demo-doc","replicaId":"browser-tab-a","authToken":"01234567890123456789012345678901"}`))
	if err != nil {
		t.Fatal(err)
	}
	if message.AuthToken != "01234567890123456789012345678901" {
		t.Fatalf("unexpected auth token: %q", message.AuthToken)
	}
	if _, err := ParseMessage([]byte(`{"protocolVersion":1,"type":"join","documentId":"demo-doc","replicaId":"browser-tab-a","authToken":""}`)); err == nil {
		t.Fatal("empty auth token was accepted")
	}
}

func TestParsePresenceAndRejectsInvalidOffsets(t *testing.T) {
	message, err := ParseMessage([]byte(`{"protocolVersion":1,"type":"presence","documentId":"doc","replicaId":"writer","anchor":0,"head":12}`))
	if err != nil {
		t.Fatal(err)
	}
	if message.Anchor == nil || *message.Anchor != 0 || message.Head == nil || *message.Head != 12 {
		t.Fatalf("unexpected presence message: %+v", message)
	}
	if _, err := ParseMessage([]byte(`{"protocolVersion":1,"type":"presence","documentId":"doc","replicaId":"writer","anchor":-1,"head":12}`)); err == nil {
		t.Fatal("negative cursor offset was accepted")
	}
	if _, err := ParseMessage([]byte(`{"protocolVersion":1,"type":"presence","documentId":"doc","replicaId":"writer","anchor":0,"head":10000001}`)); err == nil {
		t.Fatal("cursor offset above the protocol limit was accepted")
	}
}

func TestParseRejectsUnsupportedOrInvalidMessages(t *testing.T) {
	cases := [][]byte{
		[]byte(`{"protocolVersion":2,"type":"join","documentId":"doc","replicaId":"replica"}`),
		[]byte(`{"protocolVersion":1,"type":"join","documentId":"doc","replicaId":"replica","extra":true}`),
		[]byte(`{"protocolVersion":1,"type":"leave","documentId":"doc","replicaId":"replica"}`),
		[]byte(`{"protocolVersion":1,"type":"join","documentId":"","replicaId":"replica"}`),
		[]byte(`{"protocolVersion":1,"type":"join","documentId":"doc","replicaId":"replica"} {}`),
	}

	for _, input := range cases {
		if _, err := ParseMessage(input); err == nil {
			t.Errorf("ParseMessage(%s) succeeded, want error", input)
		}
	}
}

func TestParseOperationAndAcknowledgementMessages(t *testing.T) {
	operation, err := ParseMessage([]byte(`{"protocolVersion":1,"type":"operation","documentId":"doc","replicaId":"writer","operationId":"insert:writer:writer:1","operation":{"kind":"insert","id":{"replicaId":"writer","counter":1},"after":null,"value":"x"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if operation.Type != "operation" || len(operation.Operation) == 0 || operation.OperationID != "insert:writer:writer:1" {
		t.Fatalf("unexpected operation message: %+v", operation)
	}

	ack, err := ParseMessage([]byte(`{"protocolVersion":1,"type":"ack","documentId":"doc","operationId":"insert:writer:writer:1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if ack.OperationID != "insert:writer:writer:1" {
		t.Fatalf("unexpected acknowledgement: %+v", ack)
	}
}

func TestParseRejectsInvalidOperationMessages(t *testing.T) {
	cases := [][]byte{
		[]byte(`{"protocolVersion":1,"type":"operation","documentId":"doc","replicaId":"writer","operationId":"insert:writer:writer:1","operation":{"kind":"insert","id":{"replicaId":"writer","counter":1},"after":null,"value":"xy"}}`),
		[]byte(`{"protocolVersion":1,"type":"operation","documentId":"doc","replicaId":"writer","operationId":"delete:writer:writer:1","operation":{"kind":"delete","id":{"replicaId":"writer","counter":1,"extra":true}}}`),
		[]byte(`{"protocolVersion":1,"type":"operation","documentId":"doc","replicaId":"writer","operationId":"unknown:writer:writer:1","operation":{"kind":"unknown","id":{"replicaId":"writer","counter":1}}}`),
		[]byte(`{"protocolVersion":1,"type":"operation","documentId":"doc","replicaId":"writer","operationId":"insert:writer:other:1","operation":{"kind":"insert","id":{"replicaId":"other","counter":1},"after":null,"value":"x"}}`),
	}

	for _, input := range cases {
		if _, err := ParseMessage(input); err == nil {
			t.Errorf("ParseMessage(%s) succeeded, want error", input)
		}
	}
}
