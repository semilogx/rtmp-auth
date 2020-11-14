package main

import (
	"fmt"
	"log"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gorilla/csrf"

	"github.com/voc/rtmp-auth/storage"
)

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

			log.Printf("Rmove requested for stream %v (%v/%v)", id, app, name)
			if stream.Active {
				log.Println("Stream active. Trying to drop publisher.")
				var e *nginxControlError
				if err := ReqDropStreamPublisher(store, id); err == nil {
					log.Printf("Dropped publisher for stream id %v (%v/%v)", id, app, name)
				} else if errors.As(err, &e) {
					if e.RequestSent {
						log.Println(e)
					}
				}
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
		log.Printf("Block requested for stream %v (%v/%v)", id, app, name)
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
					log.Println("Stream active. Trying to drop publisher.")
					var e *nginxControlError
					if err := ReqDropStreamPublisher(store, id); err == nil {
						log.Printf("Dropped publisher for stream id %v (%v/%v)", id, app, name)
					} else if errors.As(err, &e) {
						if e.RequestSent {
							log.Println(e)
						}
					}
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
