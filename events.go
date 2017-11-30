// Package events provides an events service definition.
package events

import (
	"context"

	"github.com/shurcooL/events/event"
)

// Service for events.
type Service interface {
	// List lists events.
	List(ctx context.Context) ([]event.Event, error)

	ExternalService
}

// ExternalService for events.
type ExternalService interface {
	// Log logs the event.
	// event.Time time zone must be UTC.
	Log(ctx context.Context, event event.Event) error
}

// CONSIDER: Store for events.
/*type Store interface {
	// Append adds event to the end.
	Append(ctx context.Context, event event.Event) error

	// Get fetches ...
	//Get(ctx context.Context) (event.Event, error)

	// CONSIDER: Delete outdated events from beginning.
	//Delete(ctx context.Context, ...) error
}*/
