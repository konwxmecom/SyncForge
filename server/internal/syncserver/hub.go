package syncserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/konwxmecom/SyncForge/server/internal/access"
	"github.com/konwxmecom/SyncForge/server/internal/oplog"
	"github.com/konwxmecom/SyncForge/server/internal/protocol"
)

var (
	errHistoryLimit        = errors.New("document operation history limit reached")
	errOperationConflict   = errors.New("operation ID was reused with different content")
	errStorageFailure      = errors.New("durable operation log failure")
	errRoomConnectionLimit = errors.New("document connection limit reached")
)

const (
	maxMessageBytes       = 64 << 10
	joinTimeout           = 10 * time.Second
	writeWait             = 10 * time.Second
	pongWait              = 60 * time.Second
	pingInterval          = (pongWait * 9) / 10
	outboundBuffer        = 32
	maxRoomHistory        = 10_000
	maxHistoryBytes       = 32 << 20
	inboundMessageWindow  = time.Minute
	defaultMaxConnections = 256
	defaultMaxRoomClients = 32
	defaultMessagesMinute = 600
)

type Limits struct {
	MaxConnectionsPerDocument int
	MaxConnections            int
	MaxMessagesPerMinute      int
}

func DefaultLimits() Limits {
	return Limits{
		MaxConnectionsPerDocument: defaultMaxRoomClients,
		MaxConnections:            defaultMaxConnections,
		MaxMessagesPerMinute:      defaultMessagesMinute,
	}
}

type Hub struct {
	mu                sync.RWMutex
	rooms             map[string]*room
	store             operationStore
	authorizer        access.Authorizer
	limits            Limits
	activeConnections int
}

type room struct {
	peers       map[*peer]struct{}
	operations  [][]byte
	byID        map[string][]byte
	presences   map[string]presenceState
	historySize int
}

type presenceState struct {
	owner   *peer
	message []byte
}

type operationStore interface {
	Load(documentID string) ([]oplog.Record, error)
	Append(documentID, operationID string, message []byte) error
}

type peer struct {
	connection         *websocket.Conn
	room               string
	replicaID          string
	outbound           chan outboundMessage
	stopped            chan struct{}
	stopOnce           sync.Once
	messageWindowStart time.Time
	messagesInWindow   int
}

type outboundMessage struct {
	payload []byte
	written chan error
}

func NewHub() *Hub {
	return NewHubWithLimits(nil, nil, DefaultLimits())
}

func NewHubWithServices(store operationStore, authorizer access.Authorizer) *Hub {
	return NewHubWithLimits(store, authorizer, DefaultLimits())
}

func NewHubWithLimits(store operationStore, authorizer access.Authorizer, limits Limits) *Hub {
	return &Hub{
		rooms:      make(map[string]*room),
		store:      store,
		authorizer: authorizer,
		limits:     limits,
	}
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     checkSameHostOrigin,
}

func checkSameHostOrigin(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return false
	}
	requestHost := request.Host
	if host, _, err := net.SplitHostPort(requestHost); err == nil {
		requestHost = host
	}
	return strings.EqualFold(parsed.Hostname(), strings.Trim(requestHost, "[]"))
}

func (hub *Hub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	if !hub.acquireConnection() {
		http.Error(w, "connection limit reached", http.StatusServiceUnavailable)
		return
	}
	defer hub.releaseConnection()

	connection, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	connection.SetReadLimit(maxMessageBytes)
	if err := connection.SetReadDeadline(time.Now().Add(joinTimeout)); err != nil {
		_ = connection.Close()
		return
	}
	messageType, raw, err := connection.ReadMessage()
	if err != nil {
		_ = connection.Close()
		return
	}
	message, err := protocol.ParseMessage(raw)
	if messageType != websocket.TextMessage || err != nil || message.Type != "join" {
		hub.writeErrorAndClose(connection, "invalid_join", "The first message must be a valid join")
		return
	}
	if hub.authorizer != nil && !hub.authorizer.Allows(message.AuthToken, message.DocumentID) {
		hub.writeErrorAndClose(connection, "unauthorized", "Access to this document is not authorized")
		return
	}
	if err := connection.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		_ = connection.Close()
		return
	}
	connection.SetPongHandler(func(string) error {
		return connection.SetReadDeadline(time.Now().Add(pongWait))
	})

	client := &peer{
		connection: connection,
		room:       message.DocumentID,
		replicaID:  message.ReplicaID,
		outbound:   make(chan outboundMessage, outboundBuffer),
		stopped:    make(chan struct{}),
	}
	joined, err := json.Marshal(protocol.Message{
		ProtocolVersion: 1,
		Type:            "joined",
		DocumentID:      message.DocumentID,
	})
	if err != nil {
		_ = connection.Close()
		return
	}
	go client.writePump(client.stopped)
	if err := hub.addAndReplay(client, joined); err != nil {
		if errors.Is(err, errRoomConnectionLimit) {
			client.sendError("room_limit", "Document connection limit reached")
			client.stop()
			return
		}
		log.Printf("Unable to restore document %q: %v", client.room, err)
		hub.writeErrorAndClose(connection, "storage_error", "Unable to restore document history")
		client.stop()
		return
	}
	if client.isStopped() {
		client.stop()
		return
	}
	defer func() {
		client.stop()
		hub.remove(client)
	}()

	for {
		messageType, raw, readErr := connection.ReadMessage()
		if readErr != nil {
			return
		}
		if !client.allowInbound(time.Now(), hub.limits.MaxMessagesPerMinute) {
			client.sendError("rate_limited", "Message rate limit exceeded")
			return
		}
		message, parseErr := protocol.ParseMessage(raw)
		if messageType != websocket.TextMessage || parseErr != nil {
			client.sendError("invalid_message", "Expected a valid sync message")
			return
		}
		if message.DocumentID != client.room || message.ReplicaID != client.replicaID {
			client.sendError("identity_mismatch", "Message room or replica does not match the joined session")
			return
		}
		if message.Type == "presence" {
			if message.Anchor == nil || message.Head == nil {
				client.sendError("invalid_presence", "Cursor offsets are required")
				return
			}
			if err := hub.updatePresence(client, *message.Anchor, *message.Head); err != nil {
				client.sendError("internal_error", "Unable to publish cursor presence")
				return
			}
			continue
		}
		if message.Type != "operation" {
			client.sendError("invalid_message", "Expected an operation or presence message")
			return
		}
		operation, parseErr := protocol.ParseOperation(message.Operation)
		if parseErr != nil {
			client.sendError("invalid_operation", "Operation payload is invalid")
			return
		}
		if operation.Kind == "insert" && operation.ID.ReplicaID != client.replicaID {
			client.sendError("identity_mismatch", "Inserted element ID must belong to the joined replica")
			return
		}

		canonicalOperation, marshalErr := json.Marshal(operation)
		if marshalErr != nil {
			client.sendError("internal_error", "Unable to encode operation")
			return
		}
		operationMessage, marshalErr := json.Marshal(protocol.Message{
			ProtocolVersion: 1,
			Type:            "operation",
			DocumentID:      client.room,
			ReplicaID:       message.ReplicaID,
			OperationID:     message.OperationID,
			Operation:       canonicalOperation,
		})
		if marshalErr != nil {
			client.sendError("internal_error", "Unable to route operation")
			return
		}
		if err := hub.recordAndBroadcast(client, message.OperationID, operationMessage); err != nil {
			switch {
			case errors.Is(err, errHistoryLimit):
				client.sendError("history_limit", fmt.Sprintf("Document history is limited to %d operations or %d bytes", maxRoomHistory, maxHistoryBytes))
			case errors.Is(err, errOperationConflict):
				client.sendError("operation_conflict", "Operation ID was reused with different content")
			case errors.Is(err, errStorageFailure):
				log.Printf("Unable to persist operation for document %q: %v", client.room, err)
				client.sendError("storage_error", "Unable to durably record operation")
			default:
				log.Printf("Unable to record operation for document %q: %v", client.room, err)
				client.sendError("internal_error", "Unable to record operation")
			}
			return
		}

		ack, marshalErr := json.Marshal(protocol.Message{
			ProtocolVersion: 1,
			Type:            "ack",
			DocumentID:      client.room,
			OperationID:     message.OperationID,
		})
		if marshalErr != nil {
			client.sendError("internal_error", "Unable to acknowledge operation")
			return
		}
		if !client.send(ack) {
			return
		}
	}
}

func (hub *Hub) addAndReplay(client *peer, joined []byte) error {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	documentRoom := hub.rooms[client.room]
	if documentRoom == nil {
		documentRoom = &room{
			peers:     make(map[*peer]struct{}),
			byID:      make(map[string][]byte),
			presences: make(map[string]presenceState),
		}
		if hub.store != nil {
			records, err := hub.store.Load(client.room)
			if err != nil {
				return err
			}
			for _, record := range records {
				message, err := protocol.ParseMessage(record.Message)
				if err != nil || message.Type != "operation" || message.DocumentID != client.room || message.OperationID != record.OperationID {
					return fmt.Errorf("invalid persisted operation at sequence %d", record.Sequence)
				}
				if _, duplicate := documentRoom.byID[record.OperationID]; duplicate {
					return fmt.Errorf("duplicate persisted operation ID %q", record.OperationID)
				}
				documentRoom.operations = append(documentRoom.operations, append([]byte(nil), record.Message...))
				documentRoom.byID[record.OperationID] = append([]byte(nil), record.Message...)
				documentRoom.historySize += len(record.Message)
			}
			if len(documentRoom.operations) > maxRoomHistory || documentRoom.historySize > maxHistoryBytes {
				return fmt.Errorf("persisted document history exceeds configured limits")
			}
		}
		hub.rooms[client.room] = documentRoom
	}
	if len(documentRoom.peers) >= hub.limits.MaxConnectionsPerDocument {
		return errRoomConnectionLimit
	}
	documentRoom.peers[client] = struct{}{}
	for _, operation := range documentRoom.operations {
		if !client.enqueue(operation) {
			delete(documentRoom.peers, client)
			return nil
		}
	}
	for _, presence := range documentRoom.presences {
		if !client.enqueue(presence.message) {
			delete(documentRoom.peers, client)
			return nil
		}
	}
	if !client.enqueue(joined) {
		delete(documentRoom.peers, client)
		return nil
	}
	return nil
}

func (hub *Hub) acquireConnection() bool {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.activeConnections >= hub.limits.MaxConnections {
		return false
	}
	hub.activeConnections++
	return true
}

func (hub *Hub) releaseConnection() {
	hub.mu.Lock()
	hub.activeConnections--
	hub.mu.Unlock()
}

func (client *peer) allowInbound(now time.Time, limit int) bool {
	if client.messageWindowStart.IsZero() || now.Sub(client.messageWindowStart) >= inboundMessageWindow || now.Before(client.messageWindowStart) {
		client.messageWindowStart = now
		client.messagesInWindow = 0
	}
	if client.messagesInWindow >= limit {
		return false
	}
	client.messagesInWindow++
	return true
}

func (hub *Hub) remove(client *peer) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	documentRoom := hub.rooms[client.room]
	if documentRoom == nil {
		return
	}
	delete(documentRoom.peers, client)
	if presence, exists := documentRoom.presences[client.replicaID]; exists && presence.owner == client {
		delete(documentRoom.presences, client.replicaID)
		leaveMessage, err := json.Marshal(protocol.Message{
			ProtocolVersion: 1,
			Type:            "presence_leave",
			DocumentID:      client.room,
			ReplicaID:       client.replicaID,
		})
		if err == nil {
			hub.broadcastEphemeralLocked(documentRoom, client, leaveMessage)
		}
	}
	if len(documentRoom.peers) == 0 && len(documentRoom.operations) == 0 {
		delete(hub.rooms, client.room)
	}
}

func (hub *Hub) updatePresence(client *peer, anchor, head uint64) error {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	documentRoom := hub.rooms[client.room]
	if documentRoom == nil {
		return fmt.Errorf("document room is no longer available")
	}
	message, err := json.Marshal(protocol.Message{
		ProtocolVersion: 1,
		Type:            "presence",
		DocumentID:      client.room,
		ReplicaID:       client.replicaID,
		Anchor:          &anchor,
		Head:            &head,
	})
	if err != nil {
		return err
	}
	if documentRoom.presences == nil {
		documentRoom.presences = make(map[string]presenceState)
	}
	if current, exists := documentRoom.presences[client.replicaID]; exists && bytes.Equal(current.message, message) {
		documentRoom.presences[client.replicaID] = presenceState{owner: client, message: current.message}
		return nil
	}
	documentRoom.presences[client.replicaID] = presenceState{owner: client, message: message}
	hub.broadcastEphemeralLocked(documentRoom, client, message)
	return nil
}

func (hub *Hub) broadcastEphemeralLocked(documentRoom *room, sender *peer, message []byte) {
	slowPeers := make([]*peer, 0)
	for client := range documentRoom.peers {
		if client == sender {
			continue
		}
		select {
		case client.outbound <- outboundMessage{payload: message}:
		default:
			slowPeers = append(slowPeers, client)
		}
	}
	for _, client := range slowPeers {
		client.stop()
	}
}

func (hub *Hub) recordAndBroadcast(sender *peer, operationID string, message []byte) error {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	documentRoom := hub.rooms[sender.room]
	if documentRoom == nil {
		return fmt.Errorf("document room is no longer available")
	}
	if previous, exists := documentRoom.byID[operationID]; exists {
		if !bytes.Equal(previous, message) {
			return errOperationConflict
		}
		return nil
	}
	if len(documentRoom.operations) >= maxRoomHistory || documentRoom.historySize+len(message) > maxHistoryBytes {
		return errHistoryLimit
	}
	if hub.store != nil {
		if err := hub.store.Append(sender.room, operationID, message); err != nil {
			return fmt.Errorf("%w: %v", errStorageFailure, err)
		}
	}
	documentRoom.operations = append(documentRoom.operations, append([]byte(nil), message...))
	documentRoom.byID[operationID] = append([]byte(nil), message...)
	documentRoom.historySize += len(message)
	slowPeers := make([]*peer, 0)
	for client := range documentRoom.peers {
		if client == sender {
			continue
		}

		select {
		case client.outbound <- outboundMessage{payload: message}:
		default:
			slowPeers = append(slowPeers, client)
		}
	}
	for _, client := range slowPeers {
		client.stop()
	}
	return nil
}

func (client *peer) isStopped() bool {
	select {
	case <-client.stopped:
		return true
	default:
		return false
	}
}

func (client *peer) send(message []byte) bool {
	select {
	case client.outbound <- outboundMessage{payload: message}:
		return true
	case <-client.stopped:
		return false
	default:
		client.stop()
		return false
	}
}

func (client *peer) enqueue(message []byte) bool {
	select {
	case client.outbound <- outboundMessage{payload: message}:
		return true
	case <-client.stopped:
		return false
	}
}

func (client *peer) sendError(code, message string) {
	raw, err := json.Marshal(protocol.Message{
		ProtocolVersion: 1,
		Type:            "error",
		Code:            code,
		Message:         message,
	})
	if err != nil {
		client.stop()
		return
	}
	written := make(chan error, 1)
	select {
	case client.outbound <- outboundMessage{payload: raw, written: written}:
	case <-client.stopped:
		return
	}
	select {
	case <-written:
	case <-client.stopped:
	}
}

func (hub *Hub) writeErrorAndClose(connection *websocket.Conn, code, message string) {
	_ = connection.SetWriteDeadline(time.Now().Add(writeWait))
	if err := connection.WriteJSON(protocol.Message{
		ProtocolVersion: 1,
		Type:            "error",
		Code:            code,
		Message:         message,
	}); err != nil {
		_ = connection.Close()
		return
	}
	_ = connection.Close()
}

func (client *peer) writePump(stopped <-chan struct{}) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case message := <-client.outbound:
			_ = client.connection.SetWriteDeadline(time.Now().Add(writeWait))
			err := client.connection.WriteMessage(websocket.TextMessage, message.payload)
			if message.written != nil {
				message.written <- err
			}
			if err != nil {
				client.stop()
				return
			}
		case <-ticker.C:
			_ = client.connection.SetWriteDeadline(time.Now().Add(writeWait))
			if err := client.connection.WriteMessage(websocket.PingMessage, nil); err != nil {
				client.stop()
				return
			}
		case <-stopped:
			return
		}
	}
}

func (client *peer) stop() {
	client.stopOnce.Do(func() {
		close(client.stopped)
		_ = client.connection.Close()
	})
}
