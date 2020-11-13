package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/csrf"
	"github.com/gorilla/mux"
	"github.com/rakyll/statik/fs"

	_ "github.com/voc/rtmp-auth/statik"
	"github.com/voc/rtmp-auth/storage"
)

type handleFunc func(http.ResponseWriter, *http.Request)

var durationRegex = regexp.MustCompile(`P([\d\.]+Y)?([\d\.]+M)?([\d\.]+D)?T?([\d\.]+H)?([\d\.]+M)?([\d\.]+?S)?`)

func parseDurationPart(value string, unit time.Duration) time.Duration {
	if len(value) != 0 {
		if parsed, err := strconv.ParseFloat(value[:len(value)-1], 64); err == nil {
			return time.Duration(float64(unit) * parsed)
		}
	}
	return 0
}

// Parse expiration time
func ParseExpiry(str string) *int64 {
	// Allow empty string for "never"
	if str == "" {
		never := int64(-1)
		return &never
	}

	// Try to parse as ISO8601 duration
	matches := durationRegex.FindStringSubmatch(str)
	if matches != nil {
		years := parseDurationPart(matches[1], time.Hour*24*365)
		months := parseDurationPart(matches[2], time.Hour*24*30)
		days := parseDurationPart(matches[3], time.Hour*24)
		hours := parseDurationPart(matches[4], time.Hour)
		minutes := parseDurationPart(matches[5], time.Second*60)
		seconds := parseDurationPart(matches[6], time.Second)
		d := time.Duration(years + months + days + hours + minutes + seconds)
		if d == 0 {
			return nil
		}

		expiry := time.Now().Add(d).Unix()
		return &expiry
	}

	// Try to parse as absolute time
	t, err := time.Parse(time.RFC3339, str)
	if err != nil {
		return nil
	}
	expiry := t.Unix()
	return &expiry
}

func PublishHandler(store *Store) handleFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := r.ParseForm()
		if err != nil {
			log.Println("Failed to parse publish data:", err)
			http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
			return
		}

		app := r.PostForm.Get("app")
		name := r.PostForm.Get("name")
		auth := r.PostForm.Get("auth")

		log.Printf("Request to publish %v/%v auth: '%v'\n", app, name, auth)

		id, err := store.Auth(app, name, auth)
		if err != nil {
			var e *authError
			if errors.As(err, &e) {
				switch e.Reason() {
				case "unauthorized":
					log.Printf("Authentication for %v/%v failed. %v\n", app, name, e)
					http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
					return
				case "busy":
					log.Printf("Authentication for stream %v on %v/%v succeeded. %v\n", id, app, name, e)
					http.Error(w, "409 Conflict", http.StatusConflict)
					return
				case "blocked":
					log.Printf("Authentication for stream %v on %v/%v succeeded. %v\n", id, app, name, e)
					http.Error(w, "403 Forbidden", http.StatusForbidden)
					return
				}
			}
		}

		if err = store.SetActive(id); err != nil {
			log.Println(err)
			return
		}

		log.Printf("Authentication for stream %v on %v/%v succeeded. Publish ok.\n", id, app, name)
	}
}

func UnpublishHandler(store *Store) handleFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			log.Println("Failed to parse unpublish data:", err)
			http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
			return
		}

		app := r.PostForm.Get("app")
		name := r.PostForm.Get("name")

		if err := store.SetInactive(app, name); err != nil {
			fmt.Println("Unpublish failed:", err)
			http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
			return
		}

		log.Printf("Unpublish %v/%v ok\n", app, name)
	}
}

func FormHandler(store *Store) handleFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := TemplateData{
			Store:        store.Get(),
			CsrfTemplate: csrf.TemplateField(r),
		}

		if err := templates.ExecuteTemplate(w, "form.html", data); err != nil {
			log.Println("FormHandler: Template failed", err)
		}
	}
}

func AddHandler(store *Store) handleFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var errs []error

		expiry := ParseExpiry(r.PostFormValue("auth_expire"))
		if expiry == nil {
			errs = append(errs, fmt.Errorf("Invalid auth expiry: '%v'", r.PostFormValue("auth_expire")))
		}

		name := r.PostFormValue("name")
		if len(name) == 0 {
			errs = append(errs, fmt.Errorf("Stream name must be set"))
		} else if name != url.PathEscape(name) {
			errs = append(errs, fmt.Errorf("Stream name contains unsafe characters"))
		}

		// used by dumpscript, not user visible
		blocked, err := strconv.ParseBool(r.PostFormValue("blocked"));
		if err != nil {
			blocked = false
		}

		// TODO: more validation
		if len(errs) == 0 {
			stream := &storage.Stream{
				Name:        name,
				Application: r.PostFormValue("application"),
				AuthKey:     r.PostFormValue("auth_key"),
				AuthExpire:  *expiry,
				Notes:       r.PostFormValue("notes"),
				Blocked:     blocked,
			}

			if err := store.AddStream(stream); err != nil {
				errs = append(errs, fmt.Errorf("Failed to add stream."))
				log.Printf("AddHandler: Failed to add stream. %v", err)
			} else {
				log.Println("New stream added:", stream)
				// log.Println("Store add", stream, store.State)
			}
		}

		if len(errs) > 0 {
			data := TemplateData{
				Store:        store.Get(),
				CsrfTemplate: csrf.TemplateField(r),
				Errors:       errs,
			}
			if err := templates.ExecuteTemplate(w, "form.html", data); err != nil {
				log.Println("AddHandler: Template failed", err)
			}
		} else {
			http.Redirect(w, r, "/", http.StatusSeeOther)
		}
	}
}

func RemoveHandler(store *Store) handleFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var errs []error
		id := r.PostFormValue("id")

		stream, err := store.GetStreamById(id)
		if err != nil {
			errs = append(errs, fmt.Errorf("Couldn't remove stream"))
			log.Printf("RemoveHandler: Stream id not found. %v", err)
		} else {
			app := stream.Application
			name := stream.Name

			if stream.Active {
				DropStreamPublisher(store, id)
			}

			if err := store.RemoveStream(id); err != nil {
				errs = append(errs, fmt.Errorf("Failed to remove stream."))
				log.Printf("RemoveHandler: Failed to remove stream %v (%v/%v). %v", id, app, name, err)
			} else {
				// TODO: var stream is dangling at this point
				// check what to do... stream = nil?
				log.Printf("Removed stream %v (%v/%v)", id, app, name)
			}
		}

		if len(errs) > 0 {
			data := TemplateData{
				Store:        store.Get(),
				CsrfTemplate: csrf.TemplateField(r),
				Errors:       errs,
			}

			if err := templates.ExecuteTemplate(w, "form.html", data); err != nil {
				log.Println("RemoveHandler: Template failed", err)
			}
		} else {
			http.Redirect(w, r, "/", http.StatusSeeOther)
		}
	}
}

func BlockHandler(store *Store) handleFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var errs []error
		id := r.PostFormValue("id")

		app, name, err := store.GetAppNameById(id)
		if err != nil {
			errs = append(errs, fmt.Errorf("Couldn't change stream block status."))
			log.Printf("BlockHandler: %v", err)
		} else {
			state, _ := strconv.ParseBool(r.PostFormValue("blocked"))
			newstate, action := func(bool) (bool, string) {
				if state == true { return false, "unblock"} else {return true, "block"}
			}(state)

			if err := store.SetBlocked(id, newstate); err != nil {
				errs = append(errs, fmt.Errorf("Failed to %v stream.", action))
				log.Printf("BlockHandler: Failed to %v stream %v (%v/%v). %v", action, id, app, name, err)
			} else {
				if newstate == true {
					DropStreamPublisher(store, id)
				}
				log.Printf("Stream %v (%v/%v) %ved", id, app, name, action)
			}
		}

		if len(errs) > 0 {
			data := TemplateData{
				Store:        store.Get(),
				CsrfTemplate: csrf.TemplateField(r),
				Errors:       errs,
			}
			if err := templates.ExecuteTemplate(w, "form.html", data); err != nil {
				log.Println("BlockHandler: Template failed", err)
			}
		} else {
			http.Redirect(w, r, "/", http.StatusSeeOther)
		}
	}
}

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
