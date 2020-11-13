package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/gorilla/csrf"
	"github.com/gorilla/mux"
	"github.com/rakyll/statik/fs"

	_ "github.com/voc/rtmp-auth/statik"
)

func main() {
	var path = flag.String("store", "store.db", "Path to store file")
	var apps = flag.String("app", "stream", "Comma separated list of RTMP applications")
	var apiAddr = flag.String("apiAddr", "localhost:8080", "API bind address")
	var frontendAddr = flag.String("frontendAddr", "localhost:8082", "Frontend bind address")
	var insecure = flag.Bool("insecure", false, "Set to allow non-secure CSRF cookie")
	var prefix = flag.String("subpath", "", "Set to allow running behind reverse-proxy at that subpath")
	var ctrlurl = flag.String("ctrlurl", "",
		"Set nginx-rtmp control url to allow dropping currently published streams (needs additional configuration for the nginx-rtmp module)")
	flag.Parse()

	store, err := NewStore(*path, strings.Split(*apps, ","), *prefix)
	if err != nil {
		log.Fatal("noo", err)
	}

	statikFS, err := fs.New()
	if err != nil {
		log.Fatal(err)
	}

	CSRF := csrf.Protect(store.State.Secret, csrf.Secure(!*insecure))

	err = store.SetCtrlUrl(*ctrlurl)
	if err != nil {
		log.Println(err)
	}

	api := mux.NewRouter()
	api.Path("/publish").Methods("POST").HandlerFunc(PublishHandler(store))
	api.Path("/unpublish").Methods("POST").HandlerFunc(UnpublishHandler(store))

	frontend := mux.NewRouter()
	sub := frontend.PathPrefix(*prefix).Subrouter()
	sub.Path("/").Methods("GET").HandlerFunc(FormHandler(store))
	sub.Path("/add").Methods("POST").HandlerFunc(AddHandler(store))
	sub.Path("/remove").Methods("POST").HandlerFunc(RemoveHandler(store))
	sub.Path("/block").Methods("POST").HandlerFunc(BlockHandler(store))
	sub.Path("/dumpscript").Methods("GET").HandlerFunc(DumpscriptHandler(store))
	sub.PathPrefix("/public/").Handler(
		http.StripPrefix(*prefix+"/public/", http.FileServer(statikFS)))

	apiServer := &http.Server{
		Handler:      api,
		Addr:         *apiAddr,
		WriteTimeout: 15 * time.Second,
		ReadTimeout:  15 * time.Second,
	}

	frontendServer := &http.Server{
		Handler:      CSRF(frontend),
		Addr:         *frontendAddr,
		WriteTimeout: 15 * time.Second,
		ReadTimeout:  15 * time.Second,
	}

	// Periodically expire old streams
	ticker := time.NewTicker(10 * time.Second)
	stopPolling := make(chan struct{})
	go func() {
		for {
			select {
			case <-stopPolling:
				return
			case <-ticker.C:
				store.Expire()
			}
		}
	}()

	// Run http servers
	go func() {
		log.Println("API Listening on", apiServer.Addr)
		if err := apiServer.ListenAndServe(); err != nil {
			log.Println(err)
		}
	}()
	go func() {
		log.Println("Frontend Listening on", frontendServer.Addr)
		if err := frontendServer.ListenAndServe(); err != nil {
			log.Println(err)
		}
	}()

	// Handle signals
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Shut everything down
	close(stopPolling)
	go apiServer.Shutdown(ctx)
	go frontendServer.Shutdown(ctx)

	// Wait until timeout
	log.Println("Shutting down")
	<-ctx.Done()
}
