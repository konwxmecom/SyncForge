package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/konwxmecom/SyncForge/server/internal/access"
	"github.com/konwxmecom/SyncForge/server/internal/oplog"
	"github.com/konwxmecom/SyncForge/server/internal/syncserver"
)

type Handler struct {
	hub *syncserver.Hub
	mux *http.ServeMux
}

func NewHandler() *Handler {
	return newHandler(syncserver.NewHub())
}

func NewDurableHandler(store *oplog.Store, authorizer access.Authorizer) *Handler {
	return NewDurableHandlerWithLimits(store, authorizer, syncserver.DefaultLimits())
}

func NewDurableHandlerWithLimits(store *oplog.Store, authorizer access.Authorizer, limits syncserver.Limits) *Handler {
	return newHandler(syncserver.NewHubWithLimits(store, authorizer, limits))
}

func newHandler(hub *syncserver.Hub) *Handler {
	mux := http.NewServeMux()
	healthHandler := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Status string `json:"status"`
		}{Status: "ok"})
	}
	mux.HandleFunc("GET /healthz", healthHandler)
	mux.HandleFunc("GET /readyz", healthHandler)
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		stats := hub.Stats()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = fmt.Fprintf(w,
			"# HELP syncforge_active_connections Current WebSocket connections including handshakes.\n"+
				"# TYPE syncforge_active_connections gauge\nsyncforge_active_connections %d\n"+
				"# HELP syncforge_room_connections WebSocket connections joined to document rooms.\n"+
				"# TYPE syncforge_room_connections gauge\nsyncforge_room_connections %d\n"+
				"# HELP syncforge_active_rooms Document rooms currently loaded in memory.\n"+
				"# TYPE syncforge_active_rooms gauge\nsyncforge_active_rooms %d\n",
			stats.ActiveConnections, stats.RoomConnections, stats.ActiveRooms)
	})
	mux.HandleFunc("/sync", hub.HandleWebSocket)
	return &Handler{hub: hub, mux: mux}
}

func (handler *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	handler.mux.ServeHTTP(w, r)
}

func (handler *Handler) CloseWebSockets() {
	handler.hub.CloseConnections()
}
