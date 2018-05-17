package fs

import (
	"encoding/json"
	"fmt"
	"path"
	"time"

	"dmitri.shuralyov.com/state"
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
// Actor is omitted from struct because it's encoded as part of event file path.
type eventDisk struct {
	Time      time.Time
	Container string
	Payload   interface{}
}

func (e eventDisk) MarshalJSON() ([]byte, error) {
	v := struct {
		Time      time.Time
		Container string
		Type      string
		Payload   interface{}
	}{
		Time:      e.Time,
		Container: e.Container,
	}
	switch p := e.Payload.(type) {
	case event.Issue:
		v.Type = "issue"
		v.Payload = fromIssue(p)
	case event.Change:
		v.Type = "change"
		v.Payload = fromChange(p)
	case event.IssueComment:
		v.Type = "issueComment"
		v.Payload = fromIssueComment(p)
	case event.ChangeComment:
		v.Type = "changeComment"
		v.Payload = fromChangeComment(p)
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
	case event.Wiki:
		v.Type = "wiki"
		v.Payload = fromWiki(p)
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
		Container string
		Type      string
		Payload   json.RawMessage
	}
	err := json.Unmarshal(b, &v)
	if err != nil {
		return err
	}
	*e = eventDisk{
		Time:      v.Time,
		Container: v.Container,
	}
	switch v.Type {
	case "issue":
		var p issue
		err := json.Unmarshal(v.Payload, &p)
		if err != nil {
			return err
		}
		e.Payload = p.Issue()
	case "change":
		var p change
		err := json.Unmarshal(v.Payload, &p)
		if err != nil {
			return err
		}
		e.Payload = p.Change()
	case "issueComment":
		var p issueComment
		err := json.Unmarshal(v.Payload, &p)
		if err != nil {
			return err
		}
		e.Payload = p.IssueComment()
	case "changeComment":
		var p changeComment
		err := json.Unmarshal(v.Payload, &p)
		if err != nil {
			return err
		}
		e.Payload = p.ChangeComment()
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
	case "wiki":
		var p wiki
		err := json.Unmarshal(v.Payload, &p)
		if err != nil {
			return err
		}
		e.Payload = p.Wiki()
	}
	return nil
}

func fromEvent(e event.Event) eventDisk {
	return eventDisk{
		Time: e.Time,
		// Omit Actor because it's encoded as part of event file path.
		Container: e.Container,
		Payload:   e.Payload,
	}
}

// Event converts eventDisk to event.Event, using actor
// inferred from event file path.
func (e eventDisk) Event(actor users.User) event.Event {
	return event.Event{
		Time:      e.Time,
		Actor:     actor,
		Container: e.Container,
		Payload:   e.Payload,
	}
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

// change is an on-disk representation of event.Change.
type change struct {
	Action        string
	ChangeTitle   string
	ChangeHTMLURL string
}

func fromChange(c event.Change) change {
	return change(c)
}

func (c change) Change() event.Change {
	return event.Change(c)
}

// issueComment is an on-disk representation of event.IssueComment.
type issueComment struct {
	IssueTitle     string
	IssueState     string
	CommentBody    string
	CommentHTMLURL string
}

func fromIssueComment(c event.IssueComment) issueComment {
	var issueState string
	switch c.IssueState {
	case state.IssueOpen:
		issueState = "open"
	case state.IssueClosed:
		issueState = "closed"
	}
	return issueComment{
		IssueTitle:     c.IssueTitle,
		IssueState:     issueState,
		CommentBody:    c.CommentBody,
		CommentHTMLURL: c.CommentHTMLURL,
	}
}

func (c issueComment) IssueComment() event.IssueComment {
	var issueState state.Issue
	switch c.IssueState {
	case "open":
		issueState = state.IssueOpen
	case "closed":
		issueState = state.IssueClosed
	}
	return event.IssueComment{
		IssueTitle:     c.IssueTitle,
		IssueState:     issueState,
		CommentBody:    c.CommentBody,
		CommentHTMLURL: c.CommentHTMLURL,
	}
}

// changeComment is an on-disk representation of event.ChangeComment.
type changeComment struct {
	ChangeTitle    string
	ChangeState    string
	CommentBody    string
	CommentHTMLURL string
}

func fromChangeComment(c event.ChangeComment) changeComment {
	var changeState string
	switch c.ChangeState {
	case state.ChangeOpen:
		changeState = "open"
	case state.ChangeClosed:
		changeState = "closed"
	case state.ChangeMerged:
		changeState = "merged"
	}
	return changeComment{
		ChangeTitle:    c.ChangeTitle,
		ChangeState:    changeState,
		CommentBody:    c.CommentBody,
		CommentHTMLURL: c.CommentHTMLURL,
	}
}

func (c changeComment) ChangeComment() event.ChangeComment {
	var changeState state.Change
	switch c.ChangeState {
	case "open":
		changeState = state.ChangeOpen
	case "closed":
		changeState = state.ChangeClosed
	case "merged":
		changeState = state.ChangeMerged
	}
	return event.ChangeComment{
		ChangeTitle:    c.ChangeTitle,
		ChangeState:    changeState,
		CommentBody:    c.CommentBody,
		CommentHTMLURL: c.CommentHTMLURL,
	}
}

// commitComment is an on-disk representation of event.CommitComment.
type commitComment struct {
	Commit      commit
	CommentBody string
}

func fromCommitComment(c event.CommitComment) commitComment {
	return commitComment{
		Commit:      fromCommit(c.Commit),
		CommentBody: c.CommentBody,
	}
}

func (c commitComment) CommitComment() event.CommitComment {
	return event.CommitComment{
		Commit:      c.Commit.Commit(),
		CommentBody: c.CommentBody,
	}
}

// push is an on-disk representation of event.Push.
type push struct {
	Branch        string
	Head          string
	Before        string
	Commits       []commit
	HeadHTMLURL   string `json:",omitempty"`
	BeforeHTMLURL string `json:",omitempty"`
}

func fromPush(p event.Push) push {
	var commits []commit
	for _, c := range p.Commits {
		commits = append(commits, fromCommit(c))
	}
	return push{
		Branch:        p.Branch,
		Head:          p.Head,
		Before:        p.Before,
		Commits:       commits,
		HeadHTMLURL:   p.HeadHTMLURL,
		BeforeHTMLURL: p.BeforeHTMLURL,
	}
}

func (p push) Push() event.Push {
	var commits []event.Commit
	for _, c := range p.Commits {
		commits = append(commits, c.Commit())
	}
	return event.Push{
		Branch:        p.Branch,
		Head:          p.Head,
		Before:        p.Before,
		Commits:       commits,
		HeadHTMLURL:   p.HeadHTMLURL,
		BeforeHTMLURL: p.BeforeHTMLURL,
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

// wiki is an on-disk representation of event.Wiki.
type wiki struct {
	Pages []page
}

func fromWiki(w event.Wiki) wiki {
	var pages []page
	for _, p := range w.Pages {
		pages = append(pages, fromPage(p))
	}
	return wiki{
		Pages: pages,
	}
}

func (w wiki) Wiki() event.Wiki {
	var pages []event.Page
	for _, p := range w.Pages {
		pages = append(pages, p.Page())
	}
	return event.Wiki{
		Pages: pages,
	}
}

// commit is an on-disk representation of event.Commit.
type commit struct {
	SHA             string
	Message         string `json:"CommitMessage"`
	AuthorAvatarURL string
	HTMLURL         string `json:",omitempty"`
}

func fromCommit(c event.Commit) commit {
	return commit(c)
}

func (c commit) Commit() event.Commit {
	return event.Commit(c)
}

// page is an on-disk representation of event.Page.
type page struct {
	Action         string
	SHA            string
	Title          string
	HTMLURL        string
	CompareHTMLURL string
}

func fromPage(p event.Page) page {
	return page(p)
}

func (p page) Page() event.Page {
	return event.Page(p)
}
