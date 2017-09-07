package fs

import (
	"encoding/json"
	"fmt"
	"path"
	"time"

	"github.com/shurcooL/events/event"
	"github.com/shurcooL/users"
)

// Tree layout:
//
// 	root
// 	└── userSpec
// 	    ├── ring
// 	    ├── event-0
// 	    ├── event-1
// 	    ├── event-2
// 	    ├── ...
// 	    └── event-{{ringSize-1}}

func eventsDir(user users.UserSpec) string {
	return marshalUserSpec(user)
}

func ringPath(user users.UserSpec) string {
	return path.Join(eventsDir(user), "ring")
}

func eventPath(user users.UserSpec, idx int) string {
	return path.Join(eventsDir(user), fmt.Sprintf("event-%d", idx))
}

func marshalUserSpec(us users.UserSpec) string {
	return fmt.Sprintf("%d@%s", us.ID, us.Domain)
}

// ring has capacity of ringSize elements.
// Zero value is an empty ring.
type ring struct {
	Start  int // Index of first element in ring, in [0, ringSize-1] range.
	Length int // Number of elements within ring, in [0, ringSize] range.
}

const ringSize = 100 // Maximum capacity of the ring.

// At returns i-th index from start.
func (r ring) At(i int) int {
	return (r.Start + i) % ringSize
}

// Next returns a copy of ring with the next element added,
// and the index of that element.
func (r ring) Next() (ring ring, idx int) {
	ring = r
	if ring.Length < ringSize {
		ring.Length++
	} else {
		ring.Start = (ring.Start + 1) % ringSize
	}
	idx = (ring.Start + ring.Length - 1) % ringSize
	return ring, idx
}

// eventDisk is an on-disk representation of event.Event.
type eventDisk struct {
	Time      time.Time
	Actor     users.User
	Container string
	Payload   interface{}
}

func (e eventDisk) MarshalJSON() ([]byte, error) {
	v := struct {
		Time      time.Time
		Actor     user
		Container string
		Type      string
		Payload   interface{}
	}{
		Time:      e.Time,
		Actor:     fromUser(e.Actor),
		Container: e.Container,
	}
	switch p := e.Payload.(type) {
	case event.Issue:
		v.Type = "issue"
		v.Payload = fromIssue(p)
	case event.PullRequest:
		v.Type = "pullRequest"
		v.Payload = fromPullRequest(p)
	case event.IssueComment:
		v.Type = "issueComment"
		v.Payload = fromIssueComment(p)
	case event.PullRequestComment:
		v.Type = "pullRequestComment"
		v.Payload = fromPullRequestComment(p)
	case event.CommitComment:
		v.Type = "commitComment"
		v.Payload = fromCommitComment(p)
	case event.Push:
		v.Type = "push"
		v.Payload = fromPush(p)
	case event.Star:
		v.Type = "star"
		v.Payload = fromStar(p)
	case event.Create:
		v.Type = "create"
		v.Payload = fromCreate(p)
	case event.Fork:
		v.Type = "fork"
		v.Payload = fromFork(p)
	case event.Delete:
		v.Type = "delete"
		v.Payload = fromDelete(p)
	case event.Gollum:
		v.Type = "gollum"
		v.Payload = fromGollum(p)
	}
	return json.Marshal(v)
}

func (e *eventDisk) UnmarshalJSON(b []byte) error {
	// Ignore null, like in the main JSON package.
	if string(b) == "null" {
		return nil
	}
	var v struct {
		Time      time.Time
		Actor     user
		Container string
		Type      string
		Payload   json.RawMessage
	}
	err := json.Unmarshal(b, &v)
	if err != nil {
		return err
	}
	*e = eventDisk{}
	e.Time = v.Time
	e.Actor = v.Actor.User()
	e.Container = v.Container
	switch v.Type {
	case "issue":
		var p issue
		err := json.Unmarshal(v.Payload, &p)
		if err != nil {
			return err
		}
		e.Payload = p.Issue()
	case "pullRequest":
		var p pullRequest
		err := json.Unmarshal(v.Payload, &p)
		if err != nil {
			return err
		}
		e.Payload = p.PullRequest()
	case "issueComment":
		var p issueComment
		err := json.Unmarshal(v.Payload, &p)
		if err != nil {
			return err
		}
		e.Payload = p.IssueComment()
	case "pullRequestComment":
		var p pullRequestComment
		err := json.Unmarshal(v.Payload, &p)
		if err != nil {
			return err
		}
		e.Payload = p.PullRequestComment()
	case "commitComment":
		var p commitComment
		err := json.Unmarshal(v.Payload, &p)
		if err != nil {
			return err
		}
		e.Payload = p.CommitComment()
	case "push":
		var p push
		err := json.Unmarshal(v.Payload, &p)
		if err != nil {
			return err
		}
		e.Payload = p.Push()
	case "star":
		var p star
		err := json.Unmarshal(v.Payload, &p)
		if err != nil {
			return err
		}
		e.Payload = p.Star()
	case "create":
		var p create
		err := json.Unmarshal(v.Payload, &p)
		if err != nil {
			return err
		}
		e.Payload = p.Create()
	case "fork":
		var p fork
		err := json.Unmarshal(v.Payload, &p)
		if err != nil {
			return err
		}
		e.Payload = p.Fork()
	case "delete":
		var p delete
		err := json.Unmarshal(v.Payload, &p)
		if err != nil {
			return err
		}
		e.Payload = p.Delete()
	case "gollum":
		var p gollum
		err := json.Unmarshal(v.Payload, &p)
		if err != nil {
			return err
		}
		e.Payload = p.Gollum()
	}
	return nil
}

func fromEvent(e event.Event) eventDisk {
	return eventDisk(e)
}

func (e eventDisk) Event() event.Event {
	return event.Event(e)
}

// issue is an on-disk representation of event.Issue.
type issue struct {
	Action       string
	IssueTitle   string
	IssueHTMLURL string
}

func fromIssue(i event.Issue) issue {
	return issue(i)
}

func (i issue) Issue() event.Issue {
	return event.Issue(i)
}

// pullRequest is an on-disk representation of event.PullRequest.
type pullRequest struct {
	Action             string
	PullRequestTitle   string
	PullRequestHTMLURL string
}

func fromPullRequest(pr event.PullRequest) pullRequest {
	return pullRequest(pr)
}

func (pr pullRequest) PullRequest() event.PullRequest {
	return event.PullRequest(pr)
}

// issueComment is an on-disk representation of event.IssueComment.
type issueComment struct {
	IssueTitle           string
	IssueState           string
	CommentBody          string
	CommentUserAvatarURL string
	CommentHTMLURL       string
}

func fromIssueComment(c event.IssueComment) issueComment {
	return issueComment(c)
}

func (c issueComment) IssueComment() event.IssueComment {
	return event.IssueComment(c)
}

// pullRequestComment is an on-disk representation of event.PullRequestComment.
type pullRequestComment struct {
	PullRequestTitle     string
	PullRequestState     string
	CommentBody          string
	CommentUserAvatarURL string
	CommentHTMLURL       string
}

func fromPullRequestComment(c event.PullRequestComment) pullRequestComment {
	return pullRequestComment(c)
}

func (c pullRequestComment) PullRequestComment() event.PullRequestComment {
	return event.PullRequestComment(c)
}

// commitComment is an on-disk representation of event.CommitComment.
type commitComment struct {
	Commit               commit
	CommentBody          string
	CommentUserAvatarURL string
}

func fromCommitComment(c event.CommitComment) commitComment {
	return commitComment{
		Commit:               fromCommit(c.Commit),
		CommentBody:          c.CommentBody,
		CommentUserAvatarURL: c.CommentUserAvatarURL,
	}
}

func (c commitComment) CommitComment() event.CommitComment {
	return event.CommitComment{
		Commit:               c.Commit.Commit(),
		CommentBody:          c.CommentBody,
		CommentUserAvatarURL: c.CommentUserAvatarURL,
	}
}

// push is an on-disk representation of event.Push.
type push struct {
	Commits []commit
	// TODO: Add other push event fields as needed.
}

func fromPush(p event.Push) push {
	var commits []commit
	for _, c := range p.Commits {
		commits = append(commits, fromCommit(c))
	}
	return push{
		Commits: commits,
	}
}

func (p push) Push() event.Push {
	var commits []event.Commit
	for _, c := range p.Commits {
		commits = append(commits, c.Commit())
	}
	return event.Push{
		Commits: commits,
	}
}

// star is an on-disk representation of event.Star.
type star struct{}

func fromStar(s event.Star) star {
	return star(s)
}

func (s star) Star() event.Star {
	return event.Star(s)
}

// create is an on-disk representation of event.Create.
type create struct {
	Type        string
	Name        string
	Description string
}

func fromCreate(c event.Create) create {
	return create(c)
}

func (c create) Create() event.Create {
	return event.Create(c)
}

// fork is an on-disk representation of event.Fork.
type fork struct {
	Container string
}

func fromFork(f event.Fork) fork {
	return fork(f)
}

func (f fork) Fork() event.Fork {
	return event.Fork(f)
}

// delete is an on-disk representation of event.Delete.
type delete struct {
	Type string
	Name string
}

func fromDelete(d event.Delete) delete {
	return delete(d)
}

func (d delete) Delete() event.Delete {
	return event.Delete(d)
}

// gollum is an on-disk representation of event.Gollum.
type gollum struct {
	ActorAvatarURL string
	Pages          []page
}

func fromGollum(g event.Gollum) gollum {
	var pages []page
	for _, p := range g.Pages {
		pages = append(pages, fromPage(p))
	}
	return gollum{
		ActorAvatarURL: g.ActorAvatarURL,
		Pages:          pages,
	}
}

func (g gollum) Gollum() event.Gollum {
	var pages []event.Page
	for _, p := range g.Pages {
		pages = append(pages, p.Page())
	}
	return event.Gollum{
		ActorAvatarURL: g.ActorAvatarURL,
		Pages:          pages,
	}
}

// commit is an on-disk representation of event.Commit.
type commit struct {
	SHA             string
	CommitMessage   string
	AuthorAvatarURL string
	HTMLURL         string `json:",omitempty"`
}

func fromCommit(c event.Commit) commit {
	return commit(c)
}

func (c commit) Commit() event.Commit {
	return event.Commit(c)
}

// page is an on-disk representation of  event.Page.
type page struct {
	Action         string
	Title          string
	PageHTMLURL    string
	CompareHTMLURL string
}

func fromPage(p event.Page) page {
	return page(p)
}

func (p page) Page() event.Page {
	return event.Page(p)
}

// user is an on-disk representation of users.User.
type user struct {
	ID        uint64
	Domain    string     `json:",omitempty"`
	Elsewhere []userSpec `json:",omitempty"`

	Login     string
	Name      string `json:",omitempty"`
	Email     string `json:",omitempty"`
	AvatarURL string `json:",omitempty"`
	HTMLURL   string `json:",omitempty"`

	CreatedAt time.Time `json:",omitempty"`
	UpdatedAt time.Time `json:",omitempty"`

	SiteAdmin bool `json:",omitempty"`
}

func fromUser(u users.User) user {
	var elsewhere []userSpec
	for _, us := range u.Elsewhere {
		elsewhere = append(elsewhere, fromUserSpec(us))
	}
	return user{
		ID:        u.ID,
		Domain:    u.Domain,
		Elsewhere: elsewhere,

		Login:     u.Login,
		Name:      u.Name,
		Email:     u.Email,
		AvatarURL: u.AvatarURL,
		HTMLURL:   u.HTMLURL,

		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,

		SiteAdmin: u.SiteAdmin,
	}
}

func (u user) User() users.User {
	var elsewhere []users.UserSpec
	for _, us := range u.Elsewhere {
		elsewhere = append(elsewhere, us.UserSpec())
	}
	return users.User{
		UserSpec: users.UserSpec{
			ID:     u.ID,
			Domain: u.Domain,
		},
		Elsewhere: elsewhere,

		Login:     u.Login,
		Name:      u.Name,
		Email:     u.Email,
		AvatarURL: u.AvatarURL,
		HTMLURL:   u.HTMLURL,

		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,

		SiteAdmin: u.SiteAdmin,
	}
}

// userSpec is an on-disk representation of users.UserSpec.
type userSpec struct {
	ID     uint64
	Domain string `json:",omitempty"`
}

func fromUserSpec(us users.UserSpec) userSpec {
	return userSpec(us)
}

func (us userSpec) UserSpec() users.UserSpec {
	return users.UserSpec(us)
}
