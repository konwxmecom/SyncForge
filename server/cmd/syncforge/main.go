package main

import (
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/konwxmecom/SyncForge/server/internal/access"
	"github.com/konwxmecom/SyncForge/server/internal/httpserver"
	"github.com/konwxmecom/SyncForge/server/internal/oplog"
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
		Handler:           httpserver.NewDurableHandler(store, authorizer),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("SyncForge sync server listening on %s", address)
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
