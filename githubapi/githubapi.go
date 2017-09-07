// Package githubapi implements events.Service using GitHub API client.
package githubapi

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/github"
	"github.com/shurcooL/events"
	"github.com/shurcooL/events/event"
	"github.com/shurcooL/users"
)

// NewService creates a GitHub-backed events.Service using given GitHub client.
// It fetches the events for the specified user. user.Domain must be "github.com".
func NewService(client *github.Client, user users.User) (events.Service, error) {
	if user.Domain != "github.com" {
		return nil, fmt.Errorf(`user.Domain is %q, it must be "github.com"`, user.Domain)
	}
	s := &service{
		cl:   client,
		user: user,
	}
	go s.poll()
	return s, nil
}

type service struct {
	cl   *github.Client
	user users.User

	mu         sync.Mutex
	events     []*github.Event
	commits    map[string]*github.RepositoryCommit // SHA -> Commit.
	prs        map[string]bool                     // PR API URL -> Pull Request merged.
	fetchError error
}

// List lists events.
func (s *service) List(_ context.Context) ([]event.Event, error) {
	s.mu.Lock()
	events, commits, prs, fetchError := s.events, s.commits, s.prs, s.fetchError
	s.mu.Unlock()
	return convert(events, commits, prs, s.user), fetchError
}

// Log logs the event.
func (s *service) Log(_ context.Context, event event.Event) error {
	// Nothing to do. GitHub takes care of this on their end, even when performing actions via API.
	return nil
}

func (s *service) poll() {
	for {
		events, commits, prs, pollInterval, fetchError := s.fetchEvents(context.Background())
		if fetchError != nil {
			log.Println("fetchEvents:", fetchError)
		}
		s.mu.Lock()
		if fetchError == nil {
			s.events, s.commits, s.prs = events, commits, prs
		}
		s.fetchError = fetchError
		s.mu.Unlock()

		if pollInterval < time.Minute {
			pollInterval = time.Minute
		}
		time.Sleep(pollInterval)
	}
}

// fetchEvents fetches events, as well as mentioned commits and PRs from GitHub.
func (s *service) fetchEvents(ctx context.Context) (
	events []*github.Event,
	commits map[string]*github.RepositoryCommit,
	prs map[string]bool,
	pollInterval time.Duration,
	err error,
) {
	// TODO: Investigate this:
	//       Events support pagination, however the per_page option is unsupported. The fixed page size is 30 items. Fetching up to ten pages is supported, for a total of 300 events.
	events, resp, err := s.cl.Activity.ListEventsPerformedByUser(ctx, s.user.Login, true, &github.ListOptions{PerPage: 100})
	if err != nil {
		return nil, nil, nil, 0, err
	}
	if pi, err := strconv.Atoi(resp.Header.Get("X-Poll-Interval")); err == nil {
		pollInterval = time.Duration(pi) * time.Second
	}
	commits = make(map[string]*github.RepositoryCommit)
	prs = make(map[string]bool)
	for _, e := range events {
		payload, err := e.ParsePayload()
		if err != nil {
			return nil, nil, nil, 0, fmt.Errorf("fetchEvents: ParsePayload failed: %v", err)
		}
		switch p := payload.(type) {
		case *github.PushEvent:
			for _, c := range p.Commits {
				if _, ok := commits[*c.SHA]; ok {
					continue
				}
				rc, err := s.fetchCommit(ctx, *c.URL)
				if e, ok := err.(*github.ErrorResponse); ok && e.Response.StatusCode == http.StatusNotFound {
					continue
				} else if err != nil {
					return nil, nil, nil, 0, fmt.Errorf("fetchCommit: %v", err)
				}
				commits[*c.SHA] = rc
			}
		case *github.CommitCommentEvent:
			if _, ok := commits[*p.Comment.CommitID]; ok {
				continue
			}
			commitURL := *e.Repo.URL + "/commits/" + *p.Comment.CommitID // commitURL is "{repoURL}/commits/{commitID}".
			rc, err := s.fetchCommit(ctx, commitURL)
			if e, ok := err.(*github.ErrorResponse); ok && e.Response.StatusCode == http.StatusNotFound {
				continue
			} else if err != nil {
				return nil, nil, nil, 0, fmt.Errorf("fetchCommit: %v", err)
			}
			commits[*p.Comment.CommitID] = rc

		case *github.IssueCommentEvent:
			if p.Issue.PullRequestLinks == nil {
				continue
			}
			if _, ok := prs[*p.Issue.PullRequestLinks.URL]; ok {
				continue
			}
			merged, err := s.fetchPullRequestMerged(ctx, *p.Issue.PullRequestLinks.URL)
			if err != nil {
				return nil, nil, nil, 0, fmt.Errorf("fetchPullRequestMerged: %v", err)
			}
			prs[*p.Issue.PullRequestLinks.URL] = merged
		}
	}
	return events, commits, prs, pollInterval, nil
}

// fetchCommit fetches the commit at the API URL.
func (s *service) fetchCommit(ctx context.Context, commitURL string) (*github.RepositoryCommit, error) {
	req, err := s.cl.NewRequest("GET", commitURL, nil)
	if err != nil {
		return nil, err
	}
	var commit github.RepositoryCommit
	_, err = s.cl.Do(ctx, req, &commit)
	if err != nil {
		return nil, err
	}
	return &commit, nil
}

// fetchPullRequestMerged fetches whether the Pull Request at the API URL is merged
// at current time.
func (s *service) fetchPullRequestMerged(ctx context.Context, prURL string) (bool, error) {
	// https://developer.github.com/v3/pulls/#get-if-a-pull-request-has-been-merged.
	req, err := s.cl.NewRequest("GET", prURL+"/merge", nil)
	if err != nil {
		return false, err
	}
	resp, err := s.cl.Do(ctx, req, nil)
	switch e, ok := err.(*github.ErrorResponse); {
	case err == nil && resp.StatusCode == http.StatusNoContent:
		// PR merged.
		return true, nil
	case ok && e.Response.StatusCode == http.StatusNotFound:
		// PR not merged.
		return false, nil
	case err != nil:
		return false, err
	default:
		body, _ := ioutil.ReadAll(resp.Body)
		return false, fmt.Errorf("unexpected status code: %v body: %q", resp.Status, body)
	}
}

// convert converts GitHub events. Events must contain valid payloads,
// otherwise convert panics. commits key is SHA.
// knownUser is a known user, whose email and avatar URL
// can be used when full commit details are unavailable.
func convert(
	events []*github.Event,
	commits map[string]*github.RepositoryCommit,
	prs map[string]bool,
	knownUser users.User,
) []event.Event {
	var es []event.Event
	for _, e := range events {
		ee := event.Event{
			Time: *e.CreatedAt,
			Actor: users.User{
				UserSpec: users.UserSpec{ID: uint64(*e.Actor.ID), Domain: "github.com"},
				Login:    *e.Actor.Login,
			},
			Container: "github.com/" + *e.Repo.Name,
		}

		payload, err := e.ParsePayload()
		if err != nil {
			panic(fmt.Errorf("internal error: convert given a github.Event with in invalid payload: %v", err))
		}
		switch p := payload.(type) {
		case *github.IssuesEvent:
			switch *p.Action {
			case "opened", "closed", "reopened":

				//default:
				//log.Println("convert: unsupported *github.IssuesEvent action:", *p.Action)
			}
			ee.Payload = event.Issue{
				Action:       *p.Action,
				IssueTitle:   *p.Issue.Title,
				IssueHTMLURL: *p.Issue.HTMLURL,
			}
		case *github.PullRequestEvent:
			var action string
			switch {
			case !*p.PullRequest.Merged && *p.PullRequest.State == "open":
				action = "opened"
			case !*p.PullRequest.Merged && *p.PullRequest.State == "closed":
				action = "closed"
			case *p.PullRequest.Merged:
				action = "merged"

				//default:
				//log.Println("convert: unsupported *github.PullRequestEvent PullRequest.State:", *p.PullRequest.State, "PullRequest.Merged:", *p.PullRequest.Merged)
			}
			ee.Payload = event.PullRequest{
				Action:             action,
				PullRequestTitle:   *p.PullRequest.Title,
				PullRequestHTMLURL: *p.PullRequest.HTMLURL,
			}

		case *github.IssueCommentEvent:
			switch p.Issue.PullRequestLinks {
			case nil: // Issue.
				switch *p.Action {
				case "created":
					ee.Payload = event.IssueComment{
						IssueTitle:           *p.Issue.Title,
						IssueState:           *p.Issue.State, // TODO: Verify "open", "closed"?
						CommentBody:          *p.Comment.Body,
						CommentUserAvatarURL: *p.Comment.User.AvatarURL,
						CommentHTMLURL:       *p.Comment.HTMLURL,
					}

					//default:
					//e.WIP = true
					//e.Action = component.Text(fmt.Sprintf("%v on an issue in", *p.Action))
				}
			default: // Pull Request.
				switch *p.Action {
				case "created":
					state := *p.Issue.State
					if merged := prs[*p.Issue.PullRequestLinks.URL]; state == "closed" && merged {
						state = "merged"
					}
					ee.Payload = event.PullRequestComment{
						PullRequestTitle:     *p.Issue.Title,
						PullRequestState:     state, // TODO: Verify "open", "closed", "merged"?
						CommentBody:          *p.Comment.Body,
						CommentUserAvatarURL: *p.Comment.User.AvatarURL,
						CommentHTMLURL:       *p.Comment.HTMLURL,
					}

					//default:
					//e.WIP = true
					//e.Action = component.Text(fmt.Sprintf("%v on a pull request in", *p.Action))
				}
			}
		case *github.PullRequestReviewCommentEvent:
			switch *p.Action {
			case "created":
				var state string
				switch {
				case p.PullRequest.MergedAt == nil && *p.PullRequest.State == "open":
					state = "open"
				case p.PullRequest.MergedAt == nil && *p.PullRequest.State == "closed":
					state = "closed"
				case p.PullRequest.MergedAt != nil:
					state = "merged"

					//default:
					//log.Println("convert: unsupported *github.PullRequestReviewCommentEvent PullRequest.State:", *p.PullRequest.State)
				}

				ee.Payload = event.PullRequestComment{
					PullRequestTitle:     *p.PullRequest.Title,
					PullRequestState:     state,
					CommentBody:          *p.Comment.Body,
					CommentUserAvatarURL: *p.Comment.User.AvatarURL,
					CommentHTMLURL:       *p.Comment.HTMLURL,
				}

				//default:
				//basicEvent.WIP = true
				//e.Action = component.Text(fmt.Sprintf("%v on a pull request in", *p.Action))
			}
		case *github.CommitCommentEvent:
			var commit event.Commit
			if c := commits[*p.Comment.CommitID]; c != nil {
				commit = event.Commit{
					SHA:             *c.SHA,
					CommitMessage:   *c.Commit.Message,
					AuthorAvatarURL: *c.Author.AvatarURL,
					HTMLURL:         *c.HTMLURL,
				}
			}
			// THINK: Is it worth to include partial information, if all we have is commit ID?
			//} else {
			//	commit = event.Commit{
			//		SHA: *p.Comment.CommitID,
			//	}
			//}
			ee.Payload = event.CommitComment{
				Commit:               commit,
				CommentBody:          *p.Comment.Body,
				CommentUserAvatarURL: *p.Comment.User.AvatarURL,
			}

		case *github.PushEvent:
			var cs []event.Commit
			for _, c := range p.Commits {
				commit := commits[*c.SHA]
				if commit == nil {
					avatarURL := "https://secure.gravatar.com/avatar?d=mm&f=y&s=96"
					if *c.Author.Email == knownUser.Email {
						avatarURL = knownUser.AvatarURL
					}
					cs = append(cs, event.Commit{
						SHA:             *c.SHA,
						CommitMessage:   *c.Message,
						AuthorAvatarURL: avatarURL,
					})
					continue
				}
				if commit.Author == nil {
					// Commit does not have a GitHub user associated.
					cs = append(cs, event.Commit{
						SHA:             *c.SHA,
						CommitMessage:   *c.Message,
						AuthorAvatarURL: "https://secure.gravatar.com/avatar?d=mm&f=y&s=96",
						HTMLURL:         *commit.HTMLURL,
					})
					continue
				}
				cs = append(cs, event.Commit{
					SHA:             *c.SHA,
					CommitMessage:   *c.Message,
					AuthorAvatarURL: *commit.Author.AvatarURL,
					HTMLURL:         *commit.HTMLURL,
				})
			}

			ee.Payload = event.Push{
				Branch:        strings.TrimPrefix(*p.Ref, "refs/heads/"),
				Head:          *p.Head,
				Before:        *p.Before,
				Commits:       cs,
				HeadHTMLURL:   "https://github.com/" + *e.Repo.Name + "/commit/" + *p.Head,
				BeforeHTMLURL: "https://github.com/" + *e.Repo.Name + "/commit/" + *p.Before,
			}

		case *github.WatchEvent:
			ee.Payload = event.Star{}

		case *github.CreateEvent:
			switch *p.RefType {
			case "repository":
				ee.Payload = event.Create{
					Type:        "repository",
					Description: *p.Description,
				}
			case "branch", "tag":
				ee.Payload = event.Create{
					Type: *p.RefType,
					Name: *p.Ref,
				}

				//default:
				//basicEvent.WIP = true
				//e.Action = component.Text(fmt.Sprintf("created %v in", *p.RefType))
				//e.Details = code{
				//	Text: *p.Ref,
				//}
			}
		case *github.ForkEvent:
			ee.Payload = event.Fork{
				Container: "github.com/" + *p.Forkee.FullName,
			}
		case *github.DeleteEvent:
			ee.Payload = event.Delete{
				Type: *p.RefType, // TODO: Verify *p.RefType?
				Name: *p.Ref,
			}

		case *github.GollumEvent:
			var pages []event.Page
			for _, p := range p.Pages {
				pages = append(pages, event.Page{
					Action:         *p.Action,
					Title:          *p.Title,
					PageHTMLURL:    *p.HTMLURL,
					CompareHTMLURL: *p.HTMLURL + "/_compare/" + *p.SHA + "^..." + *p.SHA,
				})
			}
			ee.Payload = event.Gollum{
				ActorAvatarURL: *e.Actor.AvatarURL,
				Pages:          pages,
			}
		}

		es = append(es, ee)
	}
	return es
}
