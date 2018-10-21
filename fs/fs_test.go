package fs_test

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/shurcooL/events/event"
	"github.com/shurcooL/events/fs"
	"github.com/shurcooL/users"
	"golang.org/x/net/webdav"
)

func Test(t *testing.T) {
	usersService := &mockUsers{Current: mockUser.UserSpec}
	s, err := fs.NewService(webdav.NewMemFS(), mockUser, usersService)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range mockEvents {
		err = s.Log(context.Background(), e)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Different user shouldn't be able to log.
	usersService.Current = users.UserSpec{ID: 2, Domain: "example.org"}
	logAsAnotherUserError := s.Log(context.Background(), mockEvents[0])
	if got, want := logAsAnotherUserError, os.ErrPermission; got != want {
		t.Errorf("Log: got error: %v, want: %v", got, want)
	}

	got, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []event.Event{mockEvents[2], mockEvents[1], mockEvents[0]}
	if !reflect.DeepEqual(got, want) {
		t.Error("List: got != want")
	}
}

var mockEvents = []event.Event{
	{
		Time:      time.Date(1, 1, 1, 0, 0, 63639271732, 105247415, time.UTC),
		Actor:     mockUser,
		Container: "example.org/some-app",
		Payload: event.Issue{
			Action:       "opened",
			IssueTitle:   "\"Create Issue\" button doesn't show up if user isn't logged in.",
			IssueHTMLURL: "https://example.org/some-app/issues/40",
		},
	},
	{
		Time:      time.Date(1, 1, 1, 0, 0, 63639144822, 841364328, time.UTC),
		Actor:     mockUser,
		Container: "example.org/another-app",
		Payload: event.IssueComment{
			IssueTitle:     "feature request: \"recently read\" notifications tab",
			IssueState:     "open",
			CommentBody:    "I am going to work on this and implement it soon.\n\nI want to prototype a different visualization/design...",
			CommentHTMLURL: "https://example.org/another-app/issues/3#comment-2",
		},
	},
	{
		Time:      time.Date(1, 1, 1, 0, 0, 63638372150, 799870036, time.UTC),
		Actor:     mockUser,
		Container: "example.org/starworthy",
		Payload:   event.Star{},
	},
}
var mockUser = users.User{
	UserSpec:  users.UserSpec{ID: 1, Domain: "example.org"},
	Login:     "gopher",
	Name:      "Sample Gopher",
	Email:     "gopher@example.org",
	AvatarURL: "https://avatars0.githubusercontent.com/u/8566911?v=4&s=32",
}

type mockUsers struct {
	Current users.UserSpec
	users.Service
}

func (mockUsers) Get(_ context.Context, user users.UserSpec) (users.User, error) {
	switch {
	case user == users.UserSpec{ID: 1, Domain: "example.org"}:
		return users.User{
			UserSpec: user,
			Login:    "gopher1",
			Name:     "Gopher One",
			Email:    "gopher1@example.org",
		}, nil
	case user == users.UserSpec{ID: 2, Domain: "example.org"}:
		return users.User{
			UserSpec: user,
			Login:    "gopher2",
			Name:     "Gopher Two",
			Email:    "gopher2@example.org",
		}, nil
	default:
		return users.User{}, fmt.Errorf("user %v not found", user)
	}
}

func (m mockUsers) GetAuthenticatedSpec(context.Context) (users.UserSpec, error) {
	return m.Current, nil
}

func (m mockUsers) GetAuthenticated(ctx context.Context) (users.User, error) {
	userSpec, err := m.GetAuthenticatedSpec(ctx)
	if err != nil {
		return users.User{}, err
	}
	if userSpec.ID == 0 {
		return users.User{}, nil
	}
	return m.Get(ctx, userSpec)
}
