package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"unicode/utf8"
)

type ElementID struct {
	ReplicaID string `json:"replicaId"`
	Counter   uint64 `json:"counter"`
}

type Operation struct {
	Kind  string     `json:"kind"`
	ID    ElementID  `json:"id"`
	After *ElementID `json:"after"`
	Value string     `json:"value"`
}

type Message struct {
	ProtocolVersion int             `json:"protocolVersion"`
	Type            string          `json:"type"`
	DocumentID      string          `json:"documentId,omitempty"`
	ReplicaID       string          `json:"replicaId,omitempty"`
	OperationID     string          `json:"operationId,omitempty"`
	Operation       json.RawMessage `json:"operation,omitempty"`
	AuthToken       string          `json:"authToken,omitempty"`
	Anchor          *uint64         `json:"anchor,omitempty"`
	Head            *uint64         `json:"head,omitempty"`
	Code            string          `json:"code,omitempty"`
	Message         string          `json:"message,omitempty"`
}

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
var operationIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,512}$`)

func ParseMessage(input []byte) (Message, error) {
	var fields map[string]json.RawMessage
	if err := decodeExactlyOne(input, &fields); err != nil {
		return Message{}, fmt.Errorf("decode sync message: %w", err)
	}
	if fields == nil {
		return Message{}, fmt.Errorf("sync message must be a JSON object")
	}

	var message Message
	if err := decodeField(fields, "protocolVersion", &message.ProtocolVersion); err != nil {
		return Message{}, err
	}
	if err := decodeField(fields, "type", &message.Type); err != nil {
		return Message{}, err
	}
	if message.ProtocolVersion != 1 {
		return Message{}, fmt.Errorf("unsupported protocol version %d", message.ProtocolVersion)
	}

	switch message.Type {
	case "join":
		if err := requireFieldsAllowing(fields, []string{"authToken"}, "protocolVersion", "type", "documentId", "replicaId"); err != nil {
			return Message{}, err
		}
		if err := decodeField(fields, "documentId", &message.DocumentID); err != nil {
			return Message{}, err
		}
		if err := decodeField(fields, "replicaId", &message.ReplicaID); err != nil {
			return Message{}, err
		}
		if rawToken, exists := fields["authToken"]; exists {
			if err := json.Unmarshal(rawToken, &message.AuthToken); err != nil || len(message.AuthToken) == 0 || len(message.AuthToken) > 512 {
				return Message{}, fmt.Errorf("authToken must contain 1 to 512 characters")
			}
		}
		if !validIdentifier(message.DocumentID) || !validIdentifier(message.ReplicaID) {
			return Message{}, fmt.Errorf("documentId and replicaId must be valid identifiers")
		}
	case "operation":
		if err := requireFields(fields, "protocolVersion", "type", "documentId", "replicaId", "operationId", "operation"); err != nil {
			return Message{}, err
		}
		if err := decodeField(fields, "documentId", &message.DocumentID); err != nil {
			return Message{}, err
		}
		if err := decodeField(fields, "replicaId", &message.ReplicaID); err != nil {
			return Message{}, err
		}
		if err := decodeField(fields, "operationId", &message.OperationID); err != nil {
			return Message{}, err
		}
		if !validIdentifier(message.DocumentID) || !validIdentifier(message.ReplicaID) || !validOperationID(message.OperationID) {
			return Message{}, fmt.Errorf("documentId, replicaId, and operationId must be valid")
		}
		message.Operation = fields["operation"]
		operation, err := ParseOperation(message.Operation)
		if err != nil {
			return Message{}, err
		}
		if operation.Kind == "insert" && operation.ID.ReplicaID != message.ReplicaID {
			return Message{}, fmt.Errorf("insert element ID must match replicaId")
		}
		expectedOperationID := fmt.Sprintf("%s:%s:%s:%d", operation.Kind, message.ReplicaID, operation.ID.ReplicaID, operation.ID.Counter)
		if message.OperationID != expectedOperationID {
			return Message{}, fmt.Errorf("operationId does not match operation identity")
		}
	case "ack":
		if err := requireFields(fields, "protocolVersion", "type", "documentId", "operationId"); err != nil {
			return Message{}, err
		}
		if err := decodeField(fields, "documentId", &message.DocumentID); err != nil {
			return Message{}, err
		}
		if err := decodeField(fields, "operationId", &message.OperationID); err != nil {
			return Message{}, err
		}
		if !validIdentifier(message.DocumentID) || !validOperationID(message.OperationID) {
			return Message{}, fmt.Errorf("invalid acknowledgement fields")
		}
	case "joined":
		if err := requireFields(fields, "protocolVersion", "type", "documentId"); err != nil {
			return Message{}, err
		}
		if err := decodeField(fields, "documentId", &message.DocumentID); err != nil {
			return Message{}, err
		}
		if !validIdentifier(message.DocumentID) {
			return Message{}, fmt.Errorf("documentId must be a valid identifier")
		}
	case "presence":
		if err := requireFields(fields, "protocolVersion", "type", "documentId", "replicaId", "anchor", "head"); err != nil {
			return Message{}, err
		}
		if err := decodeField(fields, "documentId", &message.DocumentID); err != nil {
			return Message{}, err
		}
		if err := decodeField(fields, "replicaId", &message.ReplicaID); err != nil {
			return Message{}, err
		}
		var anchor, head uint64
		if err := decodeField(fields, "anchor", &anchor); err != nil {
			return Message{}, err
		}
		if err := decodeField(fields, "head", &head); err != nil {
			return Message{}, err
		}
		if !validIdentifier(message.DocumentID) || !validIdentifier(message.ReplicaID) || anchor > 10_000_000 || head > 10_000_000 {
			return Message{}, fmt.Errorf("presence identity or cursor offsets are invalid")
		}
		message.Anchor = &anchor
		message.Head = &head
	case "presence_leave":
		if err := requireFields(fields, "protocolVersion", "type", "documentId", "replicaId"); err != nil {
			return Message{}, err
		}
		if err := decodeField(fields, "documentId", &message.DocumentID); err != nil {
			return Message{}, err
		}
		if err := decodeField(fields, "replicaId", &message.ReplicaID); err != nil {
			return Message{}, err
		}
		if !validIdentifier(message.DocumentID) || !validIdentifier(message.ReplicaID) {
			return Message{}, fmt.Errorf("presence leave identity is invalid")
		}
	case "error":
		if err := requireFields(fields, "protocolVersion", "type", "code", "message"); err != nil {
			return Message{}, err
		}
		if err := decodeField(fields, "code", &message.Code); err != nil {
			return Message{}, err
		}
		if err := decodeField(fields, "message", &message.Message); err != nil {
			return Message{}, err
		}
		if message.Code == "" || message.Message == "" {
			return Message{}, fmt.Errorf("error code and message must not be empty")
		}
	default:
		return Message{}, fmt.Errorf("unsupported sync message type %q", message.Type)
	}

	return message, nil
}

func ParseOperation(input []byte) (Operation, error) {
	var fields map[string]json.RawMessage
	if err := decodeExactlyOne(input, &fields); err != nil {
		return Operation{}, fmt.Errorf("decode operation: %w", err)
	}
	if fields == nil {
		return Operation{}, fmt.Errorf("operation must be a JSON object")
	}

	var operation Operation
	if err := decodeField(fields, "kind", &operation.Kind); err != nil {
		return Operation{}, err
	}
	switch operation.Kind {
	case "insert":
		if err := requireFields(fields, "kind", "id", "after", "value"); err != nil {
			return Operation{}, err
		}
		id, err := decodeElementID(fields["id"])
		if err != nil {
			return Operation{}, err
		}
		operation.ID = id
		if !bytes.Equal(bytes.TrimSpace(fields["after"]), []byte("null")) {
			after, err := decodeElementID(fields["after"])
			if err != nil {
				return Operation{}, err
			}
			operation.After = &after
		}
		if err := decodeField(fields, "value", &operation.Value); err != nil {
			return Operation{}, err
		}
		if !validElementID(operation.ID) || (operation.After != nil && !validElementID(*operation.After)) {
			return Operation{}, fmt.Errorf("insert operation has invalid element IDs")
		}
		if operation.After != nil && *operation.After == operation.ID {
			return Operation{}, fmt.Errorf("an element cannot reference itself as its predecessor")
		}
		if utf8.RuneCountInString(operation.Value) != 1 {
			return Operation{}, fmt.Errorf("insert operation value must contain exactly one Unicode code point")
		}
	case "delete":
		if err := requireFields(fields, "kind", "id"); err != nil {
			return Operation{}, err
		}
		id, err := decodeElementID(fields["id"])
		if err != nil {
			return Operation{}, err
		}
		operation.ID = id
		if !validElementID(operation.ID) {
			return Operation{}, fmt.Errorf("delete operation has invalid element ID")
		}
	default:
		return Operation{}, fmt.Errorf("unsupported operation kind %q", operation.Kind)
	}
	return operation, nil
}

func decodeExactlyOne(input []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("message must contain exactly one JSON value")
	}
	return nil
}

func decodeField(fields map[string]json.RawMessage, name string, destination any) error {
	raw, exists := fields[name]
	if !exists {
		return fmt.Errorf("missing required field %q", name)
	}
	if err := json.Unmarshal(raw, destination); err != nil {
		return fmt.Errorf("invalid field %q: %w", name, err)
	}
	return nil
}

func requireFields(fields map[string]json.RawMessage, names ...string) error {
	return requireFieldsAllowing(fields, nil, names...)
}

func requireFieldsAllowing(fields map[string]json.RawMessage, optional []string, names ...string) error {
	allowed := make(map[string]struct{}, len(names))
	for _, name := range names {
		allowed[name] = struct{}{}
		if _, exists := fields[name]; !exists {
			return fmt.Errorf("missing required field %q", name)
		}
	}
	for _, name := range optional {
		allowed[name] = struct{}{}
	}
	for name := range fields {
		if _, exists := allowed[name]; !exists {
			return fmt.Errorf("unknown field %q", name)
		}
	}
	return nil
}

func validIdentifier(value string) bool {
	return identifierPattern.MatchString(value)
}

func validElementID(id ElementID) bool {
	return validIdentifier(id.ReplicaID) && id.Counter > 0
}

func validOperationID(value string) bool {
	return operationIDPattern.MatchString(value)
}

func decodeElementID(input []byte) (ElementID, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(input, &fields); err != nil {
		return ElementID{}, fmt.Errorf("invalid element ID: %w", err)
	}
	if fields == nil {
		return ElementID{}, fmt.Errorf("element ID must be a JSON object")
	}
	if err := requireFields(fields, "replicaId", "counter"); err != nil {
		return ElementID{}, err
	}
	var id ElementID
	if err := decodeField(fields, "replicaId", &id.ReplicaID); err != nil {
		return ElementID{}, err
	}
	if err := decodeField(fields, "counter", &id.Counter); err != nil {
		return ElementID{}, err
	}
	return id, nil
}
