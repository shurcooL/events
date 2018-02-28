// Package event defines event types.
package event

import (
	"time"

	"dmitri.shuralyov.com/state"
	"github.com/shurcooL/users"
)

// Event represents an event.
type Event struct {
	Time      time.Time
	Actor     users.User // UserSpec and Login fields populated.
	Container string     // URL of container without schema. E.g., "github.com/user/repo".

	// Payload specifies the event type. It's one of:
	// Issue, Change, IssueComment, ChangeComment, CommitComment,
	// Push, Star, Create, Fork, Delete, Wiki.
	Payload interface{}
}

// Issue is an issue event.
type Issue struct {
	Action       string // "opened", "closed", "reopened".
	IssueTitle   string
	IssueHTMLURL string
}

// Change is a change event.
type Change struct {
	Action        string // "opened", "closed", "merged", "reopened".
	ChangeTitle   string
	ChangeHTMLURL string
}

// IssueComment is an issue comment event.
type IssueComment struct {
	IssueTitle     string
	IssueState     state.Issue
	CommentBody    string
	CommentHTMLURL string
}

// ChangeComment is a change comment event.
type ChangeComment struct {
	ChangeTitle    string
	ChangeState    state.Change
	CommentBody    string
	CommentHTMLURL string
}

// CommitComment is a commit comment event.
type CommitComment struct {
	Commit      Commit
	CommentBody string
}

// Push is a push event.
type Push struct {
	Branch  string   // Name of branch pushed to. E.g., "master".
	Head    string   // SHA of the most recent commit after the push.
	Before  string   // SHA of the most recent commit before the push.
	Commits []Commit // Ordered from earliest to most recent (head).

	HeadHTMLURL   string // Optional.
	BeforeHTMLURL string // Optional.
}

// Star is a star event.
type Star struct{}

// Create is a create event.
type Create struct {
	Type        string // "repository", "branch", "tag".
	Name        string // Only for "branch", "tag" types.
	Description string // Only for "repository" type.
}

// Fork is a fork event.
type Fork struct {
	Container string // URL of forkee container without schema. E.g., "github.com/user/repo".
}

// Delete is a delete event.
type Delete struct {
	Type string // "branch", "tag".
	Name string
}

// Wiki is a wiki event. It happens when an actor updates a wiki.
type Wiki struct {
	Pages []Page // Wiki pages that are affected.
}
