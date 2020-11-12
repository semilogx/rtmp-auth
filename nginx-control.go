package main

import (
	"fmt"
	"log"
	"net/http"
)

func DropStreamPublisher(store *Store, id string) {
	ctrlurl := store.State.CtrlUrl
	if len(ctrlurl) == 0 {
		return
	}

	// Get streams application and name
	stream := store.GetStreamById(id)
	if stream == nil {
		log.Printf("DropStreamPublisher: Stream %v not found", id)
		return
	}
	app := stream.Application
	name := stream.Name

	// Check if stream is published
	if stream.Active == false {
		return
	}

	// Check if another stream is published on app/name
	for _, stream := range store.State.Streams {
		if stream.Application == app && stream.Name == name && stream.Active == true && stream.Id != id {
			log.Printf(
				"DropStreamPublisher: Not dropping publisher for %v/%v. Access for another stream id was granted",
				app, name)
			return
		}
	}

	// Drop current publisher on app/name
	resp, err := http.Get(fmt.Sprintf("%v/control/drop/publisher?app=%v&name=%v", ctrlurl, app, name))
	if err != nil {
		log.Println("Failed to send nginx-rtmp control request:", err)
	}
	if resp != nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			store.SetInactive(app, name)
			log.Printf("Dropped stream %v %v/%v", id, app, name)
		}
	}
}
