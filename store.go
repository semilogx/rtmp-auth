package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"sync"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/google/uuid"

	"github.com/voc/rtmp-auth/storage"
)

type Store struct {
	State        storage.State
	Applications []string
	Path         string
	Prefix       string
	sync.RWMutex
}

func NewStore(path string, apps []string, prefix string) (*Store, error) {
	store := &Store{Path: path, Applications: apps, Prefix: prefix}
	if err := store.read(); err != nil {
		return nil, err
	}

	if len(store.State.Secret) == 0 {
		store.State.Secret = make([]byte, 32)
		rand.Read(store.State.Secret)
		store.save()
	}

	return store, nil
}

type authError struct {
	Msg string
	Reason string
}

func (e *authError) Error() string {
	return fmt.Sprintf("%v", e.Msg)
}

func (e *authError) getReason() string {
	return e.Reason
}

// Auth looks up if a given app/name/key tuple is allowed to publish.
// It returns the matched streams id and an error value of type authError
// in case of failure or nil if authentication was successfull
func (store *Store) Auth(app string, name string, auth string) (id string, err error) {
	store.RLock()
	defer store.RUnlock()

	for _, stream := range store.State.Streams {
		if stream.Application == app && stream.Name == name && stream.AuthKey == auth {
			if stream.Blocked == false {
				var conflict bool
				if stream.Active == true {
					conflict = false
				} else {
					conflict = store.IsActiveByAppName(app, name)
				}
				if conflict == false {
					return  stream.Id, nil
				} else {
					// TODO: Check what nginx does if publisher dissapears. If
					// it doesn't unpublish the stream, other connection attempts
					// shouldn't be blocked here.
					// Alternative code (deactivate other streams, no error):
					// if err := store.SetInactive(app, name); err != nil {
					//		log.Println(err)
					// }
					// return nil
					return stream.Id, &authError{"Publish denied. Resource busy.", "busy"}
				}
			} else {
				return stream.Id, &authError{"Publish denied. Stream blocked.", "blocked"}
			}
		}
	}
	return "", &authError{"Publish denied. Access unauthorized", "unauthorized"}
}

// GetStreamById searches the store for a given stream id.
// It returns the matching *storage.Stream Object or an error if the id is not found.
func (store *Store) GetStreamById(id string) (*storage.Stream, error) {
	for _, stream := range store.State.Streams {
		if stream.Id == id {
			return stream, nil
		}
	}
	return nil, fmt.Errorf("GetStreamById: Couldn't find stream matching id %v.", id)
}

// GetAppNameById searches the store for the Application and Name of a given stream id.
// It returns app, name if the stream id was found or empty strings and an error otherwise.
func (store *Store) GetAppNameById(id string) (app string, name string, err error) {
	found := false
	for _, stream := range store.State.Streams {
		if stream.Id == id {
			app = stream.Application
			name = stream.Name
			found = true
		}
	}
	if !found {
		return "", "", fmt.Errorf("Stream %v not found.", id)
	}

	return app, name, nil
}

// IsActiveByAppName returns true if there is an active stream on app/name
func (store *Store) IsActiveByAppName(app string, name string) bool {
	active := false
	for _, stream := range store.State.Streams {
		if stream.Application == app && stream.Name == name && stream.Active == true {
			active = true
		}
	}
	return active
}

// SetActive sets a stream to active state by its id.
func (store *Store) SetActive(id string) error {
	store.Lock()
	defer store.Unlock()

	for _, stream := range store.State.Streams {
		if stream.Id == id {
			stream.Active = true
			if err := store.save(); err != nil {
				return fmt.Errorf("Couldn't save active state for Stream %v (%v/%v)", id, stream.Application, stream.Name)
			} else {
				return nil
			}
		}
	}
	return fmt.Errorf("SetActive failed: Stream id %v not found.", id)
}

// SetInactive unsets the active state for all streams defined for app/name.
func (store *Store) SetInactive(app string, name string) error {
	store.Lock()
	defer store.Unlock()

	stateChange := false
	for _, stream := range store.State.Streams {
		if stream.Application == app && stream.Name == name && stream.Active == true {
			stream.Active = false
			stateChange = true
		}
	}
	if stateChange == false {
		return fmt.Errorf("SetInactive: Couldn't find active steams for %v/%v", app, name)
	}

	if err := store.save(); err != nil {
		return fmt.Errorf("Couldn't save inactive state for %v/%v", app, name)
	}

	return nil
}

// SetBlocked changes a streams blocked state
func (store *Store) SetBlocked(id string, state bool) error {
	store.Lock()
	defer store.Unlock()

	for _, stream := range store.State.Streams {
		if stream.Id == id {
			stream.Blocked = state
			if err := store.save(); err != nil {
				return err
			}
			return nil
		}
	}
	return nil
}

func (store *Store) AddStream(stream *storage.Stream) error {
	store.Lock()
	defer store.Unlock()

	id, err := uuid.NewUUID()
	if err != nil {
		return err
	}

	stream.Id = id.String()
	store.State.Streams = append(store.State.Streams, stream)

	if err := store.save(); err != nil {
		return err
	}

	return nil
}

func (store *Store) SetCtrlUrl(url string) error {
	store.Lock()
	defer store.Unlock()

	store.State.CtrlUrl = url

	if err := store.save(); err != nil {
		return err
	}

	return nil
}

func (store *Store) RemoveStream(id string) error {
	store.Lock()
	defer store.Unlock()

	s := store.State.Streams
	found := false
	var index int
	var stream *storage.Stream
	for index, stream = range s {
		if stream.Id == id {
			found = true
			break
		}
	}

	if found {
		copy(s[index:], s[index+1:])       // Shift a[i+1:] left one index
		s[len(s)-1] = nil                  // Erase last element (write zero value)
		store.State.Streams = s[:len(s)-1] // Truncate slice
	}

	if err := store.save(); err != nil {
		return err
	}

	return nil
}

// Expire old streams
func (store *Store) Expire() {
	var toDelete []string
	now := time.Now().Unix()

	store.RLock()
	for _, stream := range store.State.Streams {
		if stream.AuthExpire != -1 && stream.AuthExpire < now {
			log.Printf("Expiring %s/%s\n", stream.Application, stream.Name)
			toDelete = append(toDelete, stream.Id)
		}
	}
	store.RUnlock()

	for _, id := range toDelete {
		DropStreamPublisher(store, id)
		store.RemoveStream(id)
	}
}

func (store *Store) Get() Store {
	store.RLock()
	defer store.RUnlock()
	return *store
}

// Read parses the store state from a file
func (store *Store) read() error {
	store.Lock()
	defer store.Unlock()
	data, err := ioutil.ReadFile(store.Path)
	if err != nil {
		// Non-existing state is ok
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("No previous file read: %v", err)
	}
	if err := proto.Unmarshal(data, &store.State); err != nil {
		return fmt.Errorf("Failed to parse stream state: %v", err)
	}
	log.Println("State restored from", store.Path)
	return nil
}

// Save stores the store state in a file
// Requires Lock
func (store *Store) save() error {
	out, err := proto.Marshal(&store.State)
	if err != nil {
		return fmt.Errorf("Failed to encode state: %v", err)
	}
	tmp := fmt.Sprintf(store.Path+".%v", time.Now())
	if err := ioutil.WriteFile(tmp, out, 0600); err != nil {
		return fmt.Errorf("Failed to write state: %v", err)
	}
	err = os.Rename(tmp, store.Path)
	if err != nil {
		return fmt.Errorf("Failed to move state: %v", err)
	}
	return nil
}
