package main

import (
	"fmt"
	"log"
	"errors"
	"net/http"
)

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
