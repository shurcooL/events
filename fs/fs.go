// Package fs implements events.Service using a virtual filesystem.
package fs

import (
	"context"
	"os"
	"sync"

	"github.com/shurcooL/events"
	"github.com/shurcooL/events/event"
	"github.com/shurcooL/users"
	"golang.org/x/net/webdav"
)

// NewService creates a virtual filesystem-backed events.Service,
// using root for storage.
func NewService(root webdav.FileSystem, user users.UserSpec) (events.Service, error) {
	s := &service{
		fs:   root,
		user: user,
	}
	err := s.load()
	if err != nil {
		return nil, err
	}
	return s, nil
}

type service struct {
	fs   webdav.FileSystem
	user users.UserSpec

	mu     sync.Mutex
	ring   ring
	events [ringSize]event.Event // Latest events are added to the end.
}

func (s *service) load() error {
	err := jsonDecodeFile(s.fs, ringPath(s.user), &s.ring)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for i := 0; i < s.ring.N; i++ {
		idx := s.ring.FromStart(i)
		var event eventDisk
		err := jsonDecodeFile(s.fs, eventPath(s.user, idx), &event)
		if err != nil {
			return err
		}
		s.events[idx] = event.Event()
	}
	return nil
}

// List lists events.
func (s *service) List(_ context.Context) ([]event.Event, error) {
	var events []event.Event
	s.mu.Lock()
	for i := 0; i < s.ring.N; i++ {
		// Reverse order to get latest events first.
		events = append(events, s.events[s.ring.FromEnd(i)])
	}
	s.mu.Unlock()
	return events, nil
}

// Log logs the event.
func (s *service) Log(ctx context.Context, event event.Event) error {
	if event.Actor.UserSpec != s.user {
		// Skip other users.
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Commit to storage first, returning error on failure.
	ring, idx := s.ring.Next()
	err := jsonEncodeFile(s.fs, eventPath(s.user, idx), fromEvent(event))
	if err != nil {
		return err
	}
	// Write the ring file after writing the event file was successful,
	// so that partial failure is okay.
	err = jsonEncodeFile(s.fs, ringPath(s.user), ring)
	if err != nil {
		return err
	}

	// Commit to memory second.
	s.events[idx] = event
	s.ring = ring
	return nil
}
