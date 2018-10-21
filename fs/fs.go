// Package fs implements events.Service using a virtual filesystem.
package fs

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/shurcooL/events"
	"github.com/shurcooL/events/event"
	"github.com/shurcooL/users"
	"golang.org/x/net/webdav"
)

// NewService creates a virtual filesystem-backed events.Service,
// using root for storage. It logs and fetches events only for the specified user.
func NewService(root webdav.FileSystem, user users.User, users users.Service) (events.Service, error) {
	s := &service{
		fs:    root,
		user:  user,
		users: users,
	}
	err := s.load()
	if err != nil {
		return nil, err
	}
	return s, nil
}

type service struct {
	mu     sync.Mutex
	fs     webdav.FileSystem
	ring   ring
	events [ringSize]event.Event // Latest events are added to the end.

	user  users.User
	users users.Service
}

func (s *service) load() error {
	err := jsonDecodeFile(context.Background(), s.fs, ringPath(s.user.UserSpec), &s.ring)
	if os.IsNotExist(err) {
		s.ring = ring{}
	} else if err != nil {
		return err
	}
	for i := 0; i < s.ring.Length; i++ {
		idx := s.ring.At(i)
		var event eventDisk
		err := jsonDecodeFile(context.Background(), s.fs, eventPath(s.user.UserSpec, idx), &event)
		if err != nil {
			return err
		}
		s.events[idx] = event.Event(s.user)
	}
	return nil
}

// List lists events.
func (s *service) List(_ context.Context) ([]event.Event, error) {
	var events []event.Event
	s.mu.Lock()
	for i := s.ring.Length - 1; i >= 0; i-- { // Reverse order to get latest events first.
		events = append(events, s.events[s.ring.At(i)])
	}
	s.mu.Unlock()
	return events, nil
}

// Log logs the event.
// event.Time time zone must be UTC.
func (s *service) Log(ctx context.Context, event event.Event) error {
	if event.Time.Location() != time.UTC {
		return errors.New("event.Time time zone must be UTC")
	}

	if event.Actor.UserSpec != s.user.UserSpec {
		// Skip other users.
		return nil
	}

	authenticatedSpec, err := s.users.GetAuthenticatedSpec(ctx)
	if err != nil {
		return err
	}
	if authenticatedSpec != s.user.UserSpec {
		return os.ErrPermission
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ring, idx := s.ring.Next()

	// Commit to storage first, returning error on failure.
	// Write the event file, then write the ring file, so that partial failure is less bad.
	err = jsonEncodeFileWithMkdirAll(ctx, s.fs, eventPath(s.user.UserSpec, idx), fromEvent(event))
	if err != nil {
		return err
	}
	err = jsonEncodeFile(ctx, s.fs, ringPath(s.user.UserSpec), ring)
	if err != nil {
		return err
	}

	// Commit to memory second.
	s.events[idx] = event
	s.ring = ring
	return nil
}
