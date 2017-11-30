package fs_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/shurcooL/events/event"
	"github.com/shurcooL/events/fs"
	"github.com/shurcooL/users"
	"golang.org/x/net/webdav"
)

func Test(t *testing.T) {
	mem := webdav.NewMemFS()
	err := mem.Mkdir(context.Background(), "1@example.org", 0755)
	if err != nil {
		t.Fatal(err)
	}
	s, err := fs.NewService(mem, mockUser)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range mockEvents {
		err = s.Log(context.Background(), e)
		if err != nil {
			t.Fatal(err)
		}
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
