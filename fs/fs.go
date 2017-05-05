// Package fs implements events.Service using a virtual filesystem.
package fs

import (
	"context"
	"sync"

	"github.com/shurcooL/events"
	"github.com/shurcooL/events/event"
	"github.com/shurcooL/users"
	"golang.org/x/net/webdav"
)

// NewService creates a virtual filesystem-backed events.Service,
// using root for storage.
func NewService(root webdav.FileSystem, user users.UserSpec) events.Service {
	s := &service{
		fs:   root,
		user: user,
	}
	return s
}

type service struct {
	fs   webdav.FileSystem
	user users.UserSpec

	mu     sync.Mutex
	events []event.Event // Latest events are added to the end.
}

// List lists events.
func (s *service) List(_ context.Context) ([]event.Event, error) {
	s.mu.Lock()
	events := s.events
	s.mu.Unlock()
	// Reverse s.events order to get chronological order.
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return events, nil
}

// Log logs the event.
func (s *service) Log(_ context.Context, event event.Event) error {
	if event.Actor.UserSpec != s.user {
		// Skip other users.
		return nil
	}
	s.mu.Lock()
	s.events = append(s.events, event)
	s.mu.Unlock()
	return nil
}
