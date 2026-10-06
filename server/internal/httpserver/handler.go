package httpserver

import (
	"encoding/json"
	"net/http"

	"github.com/konwxmecom/SyncForge/server/internal/access"
	"github.com/konwxmecom/SyncForge/server/internal/oplog"
	"github.com/konwxmecom/SyncForge/server/internal/syncserver"
)

func NewHandler() http.Handler {
	return newHandler(syncserver.NewHub())
}

func NewDurableHandler(store *oplog.Store, authorizer access.Authorizer) http.Handler {
	return newHandler(syncserver.NewHubWithServices(store, authorizer))
}

func newHandler(hub *syncserver.Hub) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Status string `json:"status"`
		}{Status: "ok"})
	})
	mux.HandleFunc("/sync", hub.HandleWebSocket)
	return mux
}
