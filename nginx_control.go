package main

import (
	"fmt"
	"log"
	"net/http"
)

type nginxControlError struct {
	Msg string
	RequestSent bool
	GotValidResponse bool
	Returncode int
	Err error
}

func (e *nginxControlError) Error() string {
	if e.GotValidResponse {
		return fmt.Sprintf("%v Status: %v %v", e.Msg, e.Returncode, http.StatusText(e.Returncode))
	}
	return fmt.Sprintf("%v %v", e.Msg, e.Err)
}

func ReqDropStreamPublisher(store *Store, id string) error {
	ctrlurl := store.State.CtrlUrl
	if len(ctrlurl) == 0 {
		return &nginxControlError{
			"ReqDropStreamPublisher: Didn't request to drop stream publisher. Control URL not set.",
			false, false, -1, nil,
		}
	}

	// Get streams application and name
	stream, err := store.GetStreamById(id)
	if err != nil {
		return &nginxControlError{
			fmt.Sprintf("ReqDropStreamPublisher: Stream id %v not found.", id),
			false, false, -1, nil,
		}
	}
	app := stream.Application
	name := stream.Name

	// Check if stream is published
	if stream.Active == false {
		return &nginxControlError{
			fmt.Sprintf("ReqDropStreamPublisher: Didn't request to drop stream publisher. Stream id %v not active.", id),
			false, false, -1, nil,
		}
	}

	// Check if another stream is published on app/name
	for _, stream := range store.State.Streams {
		if stream.Application == app && stream.Name == name && stream.Active == true && stream.Id != id {
			return &nginxControlError{
				fmt.Sprintf(
					"ReqDropStreamPublisher: Not dropping publisher. Publish for another stream id on %v/%v was granted.",
					app, name,
				),
				false, false, -1, nil,
			}
		}
	}

	// Drop current publisher on app/name
	resp, err := http.Get(fmt.Sprintf("%v/control/drop/publisher?app=%v&name=%v", ctrlurl, app, name))
	if err != nil {
		// log.Printf("ReqDropStreamPublisher req response: %T, %v", resp, resp)
		return &nginxControlError{"ReqDropStreamPublisher: nginx-rtmp control request failed.", true, false, 0, err}
	}

	if resp != nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			err := store.SetInactive(app, name)
			if err != nil {
				log.Println(err)
			}
			return nil
		} else {
			return &nginxControlError{fmt.Sprintf("ReqDropStreamPublisher: nginx-rtmp control request denied."), true, true, resp.StatusCode, nil}
		}
	}

	// shouldn't happen
	return &nginxControlError{fmt.Sprintf("ReqDropStreamPublisher: nginx-rtmp control request failed."), true, false, -1, nil}
}
