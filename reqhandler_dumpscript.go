package main

import (
	"fmt"
	"strings"
	"strconv"
	"io"
	"time"
	"net/http"
	"net/url"
)

func DumpscriptHandler(store *Store) handleFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store.RLock()
		defer store.RUnlock()

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		io.WriteString(w, fmt.Sprintf(
			"#!/bin/bash\n\nif [ $# -lt 1 ]; then\n\techo \"Usage: $0 frontend_url\"\n\texit 1\nfi\n\n" +
			"url=${1}\ncj=$(mktemp)\n\nfunction GetToken {\n\tcurl -s -c ${cj} -b ${cj} ${url} | " +
			"grep -m1 gorilla.csrf.Token | sed -e 's/.*value=\"\\(.*\\)\".*/gorilla.csrf.Token=\\1/'\n}\n\n"))

		for _, stream := range store.State.Streams {
			var settings []string
			settings = append(settings, "application=" + url.QueryEscape(stream.Application))
			settings = append(settings, "name=" + url.QueryEscape(stream.Name))
			if len(stream.AuthKey) > 0 {
				settings = append(settings, "auth_key=" + url.QueryEscape(stream.AuthKey))
			}
			if stream.AuthExpire != -1 {
				expiry := time.Unix(stream.AuthExpire, 0)
				settings = append(settings, "auth_expire=" + url.QueryEscape(expiry.Format(time.RFC3339)))
			} else {
				settings = append(settings, "auth_expire=")
			}
			settings = append(settings, "blocked=" + strconv.FormatBool(stream.Blocked))
			if len(stream.Notes) > 0 {
				settings = append(settings, "notes=" + url.QueryEscape(stream.Notes))
			}

			io.WriteString(w, fmt.Sprintf(
				"token=\"$(GetToken)\"; curl -s -o /dev/null -c ${cj} -b ${cj} -d \"%v\" --data-urlencode \"${token}\" ${url}/add\n",
				strings.Join(settings, "&")))
		}
		io.WriteString(w, fmt.Sprintf("\nrm ${cj}"))
	}
}
