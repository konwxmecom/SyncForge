package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/konwxmecom/SyncForge/server/internal/access"
	"github.com/konwxmecom/SyncForge/server/internal/httpserver"
	"github.com/konwxmecom/SyncForge/server/internal/oplog"
)

func main() {
	address := os.Getenv("ADDR")
	if address == "" {
		address = ":8080"
	}

	dataDirectory := os.Getenv("DATA_DIR")
	if dataDirectory == "" {
		dataDirectory = "./data"
	}
	store, err := oplog.Open(dataDirectory)
	if err != nil {
		log.Fatal(err)
	}
	var authorizer access.Authorizer
	if authFile := os.Getenv("AUTH_FILE"); authFile != "" {
		authorizer, err = access.Load(authFile)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("Document authorization enabled using %s", authFile)
	} else {
		log.Printf("WARNING: AUTH_FILE is unset; all document rooms are publicly accessible")
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
