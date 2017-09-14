// Package event defines event types.
package event

import (
	"time"

	"github.com/shurcooL/users"
)

// Event represents an event.
type Event struct {
	Time      time.Time
	Actor     users.User // UserSpec and Login fields populated.
	Container string     // URL of container without schema. E.g., "github.com/user/repo".

	// Payload specifies the event type. It's one of:
	// Issue, PullRequest, IssueComment, PullRequestComment, CommitComment,
	// Push, Star, Create, Fork, Delete, Gollum.
	Payload interface{}
}

// Issue is an issue event.
type Issue struct {
	Action       string // "opened", "closed", "reopened".
	IssueTitle   string
	IssueHTMLURL string
}

// PullRequest is a pull request event.
//
// THINK: Consider calling it Change? It should be generic enough to cover PRs, CLs, etc.
type PullRequest struct {
	Action             string // "opened", "closed", "merged", "reopened".
	PullRequestTitle   string
	PullRequestHTMLURL string
}

// IssueComment is an issue comment event.
type IssueComment struct {
	IssueTitle     string
	IssueState     string // "open", "closed".
	CommentBody    string
	CommentHTMLURL string
}

// PullRequestComment is a pull request comment event.
type PullRequestComment struct {
	PullRequestTitle string
	PullRequestState string // "open", "closed", "merged".
	CommentBody      string
	CommentHTMLURL   string
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

// Gollum is a Wiki edit event.
//
// TODO: Definitely rename this... either Wiki (specific), or Edit (general).
type Gollum struct {
	Pages []Page // Wiki pages that are affected.
}
