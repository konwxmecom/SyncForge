package httpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/konwxmecom/SyncForge/server/internal/access"
	"github.com/konwxmecom/SyncForge/server/internal/oplog"
)

func TestHealthEndpoint(t *testing.T) {
	handler := NewHandler()
	for _, path := range []string{"/healthz", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
			}
			if got, want := recorder.Body.String(), "{\"status\":\"ok\"}\n"; got != want {
				t.Fatalf("body = %q, want %q", got, want)
			}
		})
	}
}

func TestMetricsReportConnectionAndRoomGauges(t *testing.T) {
	handler := NewHandler()
	server := httptest.NewServer(handler)
	defer server.Close()

	connection := connectAndJoin(t, "ws"+server.URL[len("http"):]+"/sync", "metrics-room", "replica-a")
	defer connection.Close()

	response, err := http.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("metrics status = %d, body = %q", response.StatusCode, raw)
	}
	for _, metric := range []string{
		"syncforge_active_connections 1",
		"syncforge_room_connections 1",
		"syncforge_active_rooms 1",
	} {
		if !strings.Contains(string(raw), metric) {
			t.Errorf("metrics body does not contain %q: %s", metric, raw)
		}
	}
}

func TestHandlerClosesWebSocketsOnShutdown(t *testing.T) {
	handler := NewHandler()
	server := httptest.NewServer(handler)
	defer server.Close()

	connection := connectAndJoin(t, "ws"+server.URL[len("http"):]+"/sync", "shutdown-room", "replica-a")
	defer connection.Close()

	handler.CloseWebSockets()
	if err := connection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := connection.ReadMessage(); err == nil {
		t.Fatal("websocket remained open after handler shutdown")
	}
}

func TestWebSocketRoutesOperationsWithinRoomOnly(t *testing.T) {
	server := httptest.NewServer(NewHandler())
	defer server.Close()
	url := "ws" + server.URL[len("http"):] + "/sync"

	replicaA := connectAndJoin(t, url, "shared-doc", "replica-a")
	defer replicaA.Close()
	replicaB := connectAndJoin(t, url, "shared-doc", "replica-b")
	defer replicaB.Close()
	otherRoom := connectAndJoin(t, url, "other-doc", "replica-c")
	defer otherRoom.Close()

	operationMessage := map[string]any{
		"protocolVersion": 1,
		"type":            "operation",
		"documentId":      "shared-doc",
		"replicaId":       "replica-a",
		"operationId":     "insert:replica-a:replica-a:1",
		"operation": map[string]any{
			"kind":  "insert",
			"id":    map[string]any{"replicaId": "replica-a", "counter": 1},
			"after": nil,
			"value": "x",
		},
	}
	if err := replicaA.WriteJSON(operationMessage); err != nil {
		t.Fatal(err)
	}

	var routed map[string]any
	if err := replicaB.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := replicaB.ReadJSON(&routed); err != nil {
		t.Fatalf("read routed operation: %v", err)
	}
	if routed["type"] != "operation" || routed["documentId"] != "shared-doc" || routed["operationId"] != "insert:replica-a:replica-a:1" {
		t.Fatalf("unexpected routed message: %#v", routed)
	}
	routedOperation, ok := routed["operation"].(map[string]any)
	if !ok {
		t.Fatalf("operation payload has unexpected type: %#v", routed["operation"])
	}
	if after, exists := routedOperation["after"]; !exists || after != nil {
		t.Fatalf("root insert must preserve after: null, got %#v", routedOperation)
	}

	var acknowledgement map[string]any
	if err := replicaA.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := replicaA.ReadJSON(&acknowledgement); err != nil {
		t.Fatalf("read acknowledgement: %v", err)
	}
	if acknowledgement["type"] != "ack" || acknowledgement["operationId"] != "insert:replica-a:replica-a:1" {
		t.Fatalf("unexpected acknowledgement: %#v", acknowledgement)
	}

	if err := replicaA.WriteJSON(operationMessage); err != nil {
		t.Fatal(err)
	}
	var duplicateAcknowledgement map[string]any
	if err := replicaA.ReadJSON(&duplicateAcknowledgement); err != nil {
		t.Fatalf("read duplicate acknowledgement: %v", err)
	}
	if duplicateAcknowledgement["type"] != "ack" {
		t.Fatalf("unexpected duplicate acknowledgement: %#v", duplicateAcknowledgement)
	}
	if err := replicaB.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := replicaB.ReadMessage(); err == nil {
		t.Fatal("duplicate operation was broadcast more than once")
	}

	latePeer, response, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial late peer: %v (response: %v)", err, response)
	}
	defer latePeer.Close()
	if err := latePeer.WriteJSON(map[string]any{
		"protocolVersion": 1,
		"type":            "join",
		"documentId":      "shared-doc",
		"replicaId":       "replica-d",
	}); err != nil {
		t.Fatal(err)
	}
	var replayed map[string]any
	if err := latePeer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := latePeer.ReadJSON(&replayed); err != nil {
		t.Fatalf("read replayed operation: %v", err)
	}
	if replayed["type"] != "operation" || replayed["operationId"] != "insert:replica-a:replica-a:1" {
		t.Fatalf("unexpected replay message: %#v", replayed)
	}
	var joined map[string]any
	if err := latePeer.ReadJSON(&joined); err != nil {
		t.Fatalf("read joined after replay: %v", err)
	}
	if joined["type"] != "joined" {
		t.Fatalf("expected joined after history replay, got %#v", joined)
	}

	if err := otherRoom.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := otherRoom.ReadMessage(); err == nil {
		t.Fatal("received a message from a different document room")
	}
}

func TestWebSocketRejectsReplicaIdentityMismatch(t *testing.T) {
	server := httptest.NewServer(NewHandler())
	defer server.Close()
	url := "ws" + server.URL[len("http"):] + "/sync"
	connection := connectAndJoin(t, url, "shared-doc", "replica-a")
	defer connection.Close()

	err := connection.WriteJSON(map[string]any{
		"protocolVersion": 1,
		"type":            "operation",
		"documentId":      "shared-doc",
		"replicaId":       "replica-b",
		"operationId":     "insert:replica-b:replica-b:1",
		"operation": map[string]any{
			"kind":  "insert",
			"id":    map[string]any{"replicaId": "replica-b", "counter": 1},
			"after": nil,
			"value": "x",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var response struct {
		Type string `json:"type"`
		Code string `json:"code"`
	}
	if err := connection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := connection.ReadJSON(&response); err != nil {
		t.Fatal(err)
	}
	if response.Type != "error" || response.Code != "identity_mismatch" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestWebSocketHistoryIsLostWhenServerRestarts(t *testing.T) {
	firstServer := httptest.NewServer(NewHandler())
	firstURL := "ws" + firstServer.URL[len("http"):] + "/sync"
	writer := connectAndJoin(t, firstURL, "restart-doc", "writer")
	if err := writer.WriteJSON(map[string]any{
		"protocolVersion": 1,
		"type":            "operation",
		"documentId":      "restart-doc",
		"replicaId":       "writer",
		"operationId":     "insert:writer:writer:1",
		"operation": map[string]any{
			"kind":  "insert",
			"id":    map[string]any{"replicaId": "writer", "counter": 1},
			"after": nil,
			"value": "x",
		},
	}); err != nil {
		t.Fatal(err)
	}
	var acknowledgement map[string]any
	if err := writer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := writer.ReadJSON(&acknowledgement); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	firstServer.Close()

	restartedServer := httptest.NewServer(NewHandler())
	defer restartedServer.Close()
	restartedURL := "ws" + restartedServer.URL[len("http"):] + "/sync"
	staleClient, response, err := websocket.DefaultDialer.Dial(restartedURL, nil)
	if err != nil {
		t.Fatalf("dial after restart: %v (response: %v)", err, response)
	}
	defer staleClient.Close()
	if err := staleClient.WriteJSON(map[string]any{
		"protocolVersion": 1,
		"type":            "join",
		"documentId":      "restart-doc",
		"replicaId":       "stale-client",
	}); err != nil {
		t.Fatal(err)
	}
	var joined map[string]any
	if err := staleClient.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := staleClient.ReadJSON(&joined); err != nil {
		t.Fatal(err)
	}
	if joined["type"] != "joined" {
		t.Fatalf("expected joined response after restart, got %#v", joined)
	}
	if err := staleClient.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := staleClient.ReadMessage(); err == nil {
		t.Fatal("expected restarted server to have no history for the stale client")
	}
}

func TestDurableWebSocketRestoresHistoryAndEnforcesDocumentAccess(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "data")
	firstStore, err := oplog.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	firstServer := httptest.NewServer(NewDurableHandler(firstStore, nil))
	firstURL := "ws" + firstServer.URL[len("http"):] + "/sync"
	writer := connectAndJoin(t, firstURL, "durable-doc", "writer")
	if err := writer.WriteJSON(map[string]any{
		"protocolVersion": 1,
		"type":            "operation",
		"documentId":      "durable-doc",
		"replicaId":       "writer",
		"operationId":     "insert:writer:writer:1",
		"operation": map[string]any{
			"kind":  "insert",
			"id":    map[string]any{"replicaId": "writer", "counter": 1},
			"after": nil,
			"value": "x",
		},
	}); err != nil {
		t.Fatal(err)
	}
	var acknowledgement map[string]any
	if err := writer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := writer.ReadJSON(&acknowledgement); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	firstServer.Close()

	authPath := filepath.Join(t.TempDir(), "auth.json")
	token := "01234567890123456789012345678901"
	authJSON := `{"principals":[{"token":"` + token + `","documents":["durable-doc"]}]}`
	if err := os.WriteFile(authPath, []byte(authJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	authorizer, err := access.Load(authPath)
	if err != nil {
		t.Fatal(err)
	}
	restartedStore, err := oplog.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	restartedServer := httptest.NewServer(NewDurableHandler(restartedStore, authorizer))
	defer restartedServer.Close()
	restartedURL := "ws" + restartedServer.URL[len("http"):] + "/sync"

	unauthorized, response, err := websocket.DefaultDialer.Dial(restartedURL, nil)
	if err != nil {
		t.Fatalf("dial unauthorized peer: %v (response: %v)", err, response)
	}
	if err := unauthorized.WriteJSON(map[string]any{
		"protocolVersion": 1,
		"type":            "join",
		"documentId":      "durable-doc",
		"replicaId":       "unauthorized",
	}); err != nil {
		t.Fatal(err)
	}
	var rejected map[string]any
	if err := unauthorized.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := unauthorized.ReadJSON(&rejected); err != nil {
		t.Fatal(err)
	}
	if rejected["code"] != "unauthorized" {
		t.Fatalf("unexpected authorization result: %#v", rejected)
	}
	_ = unauthorized.Close()

	authorized, response, err := websocket.DefaultDialer.Dial(restartedURL, nil)
	if err != nil {
		t.Fatalf("dial authorized peer: %v (response: %v)", err, response)
	}
	defer authorized.Close()
	if err := authorized.WriteJSON(map[string]any{
		"protocolVersion": 1,
		"type":            "join",
		"documentId":      "durable-doc",
		"replicaId":       "authorized",
		"authToken":       token,
	}); err != nil {
		t.Fatal(err)
	}
	var replayed map[string]any
	if err := authorized.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := authorized.ReadJSON(&replayed); err != nil {
		t.Fatal(err)
	}
	if replayed["type"] != "operation" || replayed["operationId"] != "insert:writer:writer:1" {
		t.Fatalf("durable operation was not restored: %#v", replayed)
	}
	var joined map[string]any
	if err := authorized.ReadJSON(&joined); err != nil {
		t.Fatal(err)
	}
	if joined["type"] != "joined" {
		t.Fatalf("unexpected final join response: %#v", joined)
	}
}

func TestWebSocketRejectsInvalidJoinAndOversizedFrame(t *testing.T) {
	server := httptest.NewServer(NewHandler())
	defer server.Close()
	url := "ws" + server.URL[len("http"):] + "/sync"

	invalidJoin, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := invalidJoin.WriteJSON(map[string]any{
		"protocolVersion": 1,
		"type":            "operation",
	}); err != nil {
		t.Fatal(err)
	}
	var errorResponse struct {
		Type string `json:"type"`
		Code string `json:"code"`
	}
	if err := invalidJoin.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := invalidJoin.ReadJSON(&errorResponse); err != nil {
		t.Fatal(err)
	}
	if errorResponse.Type != "error" || errorResponse.Code != "invalid_join" {
		t.Fatalf("unexpected invalid-join response: %+v", errorResponse)
	}
	_ = invalidJoin.Close()

	oversized, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	oversizedPayload := `{"protocolVersion":1,"type":"join","documentId":"` + strings.Repeat("x", 65<<10) + `","replicaId":"replica-a"}`
	if err := oversized.WriteMessage(websocket.TextMessage, []byte(oversizedPayload)); err != nil {
		oversized.Close()
		t.Fatal(err)
	}
	if err := oversized.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		oversized.Close()
		t.Fatal(err)
	}
	if _, _, err := oversized.ReadMessage(); err == nil {
		oversized.Close()
		t.Fatal("oversized frame was accepted")
	}
	_ = oversized.Close()
}

func connectAndJoin(t *testing.T, url, documentID, replicaID string) *websocket.Conn {
	t.Helper()
	connection, response, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v (response: %v)", err, response)
	}
	if err := connection.WriteJSON(map[string]any{
		"protocolVersion": 1,
		"type":            "join",
		"documentId":      documentID,
		"replicaId":       replicaID,
	}); err != nil {
		connection.Close()
		t.Fatalf("send join: %v", err)
	}
	var joined map[string]json.RawMessage
	if err := connection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		connection.Close()
		t.Fatal(err)
	}
	if err := connection.ReadJSON(&joined); err != nil {
		connection.Close()
		t.Fatalf("read joined message: %v", err)
	}
	var messageType string
	if err := json.Unmarshal(joined["type"], &messageType); err != nil || messageType != "joined" {
		connection.Close()
		t.Fatalf("unexpected join response: %s", joined["type"])
	}
	return connection
}
