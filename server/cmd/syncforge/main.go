package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/konwxmecom/SyncForge/server/internal/access"
	"github.com/konwxmecom/SyncForge/server/internal/httpserver"
	"github.com/konwxmecom/SyncForge/server/internal/oplog"
	"github.com/konwxmecom/SyncForge/server/internal/syncserver"
)

func main() {
	address := os.Getenv("ADDR")
	if address == "" {
		address = "127.0.0.1:8080"
	}

	authorizer, err := loadAuthorizer(os.Getenv("AUTH_FILE"))
	if err != nil {
		log.Fatal(err)
	}
	limits, err := loadLimits(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}

	dataDirectory := os.Getenv("DATA_DIR")
	if dataDirectory == "" {
		dataDirectory = "./data"
	}
	store, err := oplog.Open(dataDirectory)
	if err != nil {
		log.Fatal(err)
	}

	server := &http.Server{
		Addr:              address,
		Handler:           httpserver.NewDurableHandlerWithLimits(store, authorizer, limits),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("SyncForge sync server listening on %s (connections=%d, connections-per-document=%d, messages-per-minute=%d)",
		address, limits.MaxConnections, limits.MaxConnectionsPerDocument, limits.MaxMessagesPerMinute)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func loadAuthorizer(authFile string) (access.Authorizer, error) {
	if strings.TrimSpace(authFile) == "" {
		return nil, errors.New("AUTH_FILE is required; refusing to start without document authorization")
	}
	return access.Load(authFile)
}

func loadLimits(lookup func(string) string) (syncserver.Limits, error) {
	limits := syncserver.DefaultLimits()
	settings := []struct {
		name   string
		target *int
	}{
		{name: "MAX_CONNECTIONS", target: &limits.MaxConnections},
		{name: "MAX_CONNECTIONS_PER_DOCUMENT", target: &limits.MaxConnectionsPerDocument},
		{name: "MAX_MESSAGES_PER_MINUTE", target: &limits.MaxMessagesPerMinute},
	}
	for _, setting := range settings {
		value := lookup(setting.name)
		if value == "" {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 {
			return syncserver.Limits{}, fmt.Errorf("%s must be a positive integer", setting.name)
		}
		*setting.target = parsed
	}
	return limits, nil
}
