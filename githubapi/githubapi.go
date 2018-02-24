// Package githubapi implements events.Service using GitHub API client.
package githubapi

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"dmitri.shuralyov.com/route/github"
	githubV3 "github.com/google/go-github/github"
	"github.com/shurcooL/events"
	"github.com/shurcooL/events/event"
	"github.com/shurcooL/users"
)

// NewService creates a GitHub-backed events.Service using given GitHub client.
// It fetches events only for the specified user. user.Domain must be "github.com".
//
// If router is nil, github.DotCom router is used, which links to subjects on github.com.
func NewService(client *githubV3.Client, user users.User, router github.Router) (events.Service, error) {
	if user.Domain != "github.com" {
		return nil, fmt.Errorf(`user.Domain is %q, it must be "github.com"`, user.Domain)
	}
	if router == nil {
		router = github.DotCom{}
	}
	s := &service{
		cl:   client,
		user: user,
		rtr:  router,
	}
	go s.poll()
	return s, nil
}

type service struct {
	cl   *githubV3.Client
	user users.User
	rtr  github.Router

	mu         sync.Mutex
	events     []*githubV3.Event
	commits    map[string]*githubV3.RepositoryCommit // SHA -> Commit.
	prs        map[string]bool                       // PR API URL -> Pull Request merged.
	fetchError error
}

// List lists events.
func (s *service) List(ctx context.Context) ([]event.Event, error) {
	s.mu.Lock()
	events, commits, prs, fetchError := s.events, s.commits, s.prs, s.fetchError
	s.mu.Unlock()
	return convert(ctx, events, commits, prs, s.user, s.rtr), fetchError
}

// Log logs the event.
// event.Time time zone must be UTC.
func (s *service) Log(_ context.Context, event event.Event) error {
	if event.Time.Location() != time.UTC {
		return errors.New("event.Time time zone must be UTC")
	}
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
	events []*githubV3.Event,
	commits map[string]*githubV3.RepositoryCommit,
	prs map[string]bool,
	pollInterval time.Duration,
	err error,
) {
	// TODO: Investigate this:
	//       Events support pagination, however the per_page option is unsupported. The fixed page size is 30 items. Fetching up to ten pages is supported, for a total of 300 events.
	events, resp, err := s.cl.Activity.ListEventsPerformedByUser(ctx, s.user.Login, true, &githubV3.ListOptions{PerPage: 100})
	if err != nil {
		return nil, nil, nil, 0, err
	}
	if pi, err := strconv.Atoi(resp.Header.Get("X-Poll-Interval")); err == nil {
		pollInterval = time.Duration(pi) * time.Second
	}
	commits = make(map[string]*githubV3.RepositoryCommit)
	prs = make(map[string]bool)
	for _, e := range events {
		payload, err := e.ParsePayload()
		if err != nil {
			return nil, nil, nil, 0, fmt.Errorf("fetchEvents: ParsePayload failed: %v", err)
		}
		switch p := payload.(type) {
		case *githubV3.PushEvent:
			for _, c := range p.Commits {
				if _, ok := commits[*c.SHA]; ok {
					continue
				}
				rc, err := s.fetchCommit(ctx, *c.URL)
				if e, ok := err.(*githubV3.ErrorResponse); ok && e.Response.StatusCode == http.StatusNotFound {
					continue
				} else if err != nil {
					return nil, nil, nil, 0, fmt.Errorf("fetchCommit: %v", err)
				}
				commits[*c.SHA] = rc
			}
		case *githubV3.CommitCommentEvent:
			if _, ok := commits[*p.Comment.CommitID]; ok {
				continue
			}
			commitURL := *e.Repo.URL + "/commits/" + *p.Comment.CommitID // commitURL is "{repoURL}/commits/{commitID}".
			rc, err := s.fetchCommit(ctx, commitURL)
			if e, ok := err.(*githubV3.ErrorResponse); ok && e.Response.StatusCode == http.StatusNotFound {
				continue
			} else if err != nil {
				return nil, nil, nil, 0, fmt.Errorf("fetchCommit: %v", err)
			}
			commits[*p.Comment.CommitID] = rc

		case *githubV3.IssueCommentEvent:
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
func (s *service) fetchCommit(ctx context.Context, commitURL string) (*githubV3.RepositoryCommit, error) {
	req, err := s.cl.NewRequest("GET", commitURL, nil)
	if err != nil {
		return nil, err
	}
	var commit githubV3.RepositoryCommit
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
	switch e, ok := err.(*githubV3.ErrorResponse); {
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
	ctx context.Context,
	events []*githubV3.Event,
	commits map[string]*githubV3.RepositoryCommit,
	prs map[string]bool,
	knownUser users.User,
	router github.Router,
) []event.Event {
	var es []event.Event
	for _, e := range events {
		ee := event.Event{
			Time: *e.CreatedAt,
			Actor: users.User{
				UserSpec:  users.UserSpec{ID: uint64(*e.Actor.ID), Domain: "github.com"},
				Login:     *e.Actor.Login,
				AvatarURL: *e.Actor.AvatarURL,
			},
			Container: "github.com/" + *e.Repo.Name,
		}

		owner, repo := splitOwnerRepo(*e.Repo.Name)
		payload, err := e.ParsePayload()
		if err != nil {
			panic(fmt.Errorf("internal error: convert given a githubV3.Event with in invalid payload: %v", err))
		}
		switch p := payload.(type) {
		case *githubV3.IssuesEvent:
			switch *p.Action {
			case "opened", "closed", "reopened":

				//default:
				//log.Println("convert: unsupported *githubV3.IssuesEvent action:", *p.Action)
			}
			ee.Payload = event.Issue{
				Action:       *p.Action,
				IssueTitle:   *p.Issue.Title,
				IssueHTMLURL: router.IssueURL(ctx, owner, repo, uint64(*p.Issue.Number), 0),
			}
		case *githubV3.PullRequestEvent:
			var action string
			switch {
			case *p.Action == "opened":
				action = "opened"
			case *p.Action == "closed" && !*p.PullRequest.Merged:
				action = "closed"
			case *p.Action == "closed" && *p.PullRequest.Merged:
				action = "merged"
			case *p.Action == "reopened":
				action = "reopened"

				//default:
				//log.Println("convert: unsupported *githubV3.PullRequestEvent PullRequest.State:", *p.PullRequest.State, "PullRequest.Merged:", *p.PullRequest.Merged)
			}
			ee.Payload = event.Change{
				Action:        action,
				ChangeTitle:   *p.PullRequest.Title,
				ChangeHTMLURL: router.PullRequestURL(ctx, owner, repo, uint64(*p.PullRequest.Number), 0),
			}

		case *githubV3.IssueCommentEvent:
			switch p.Issue.PullRequestLinks {
			case nil: // Issue.
				switch *p.Action {
				case "created":
					ee.Payload = event.IssueComment{
						IssueTitle:     *p.Issue.Title,
						IssueState:     *p.Issue.State, // TODO: Verify "open", "closed"?
						CommentBody:    *p.Comment.Body,
						CommentHTMLURL: router.IssueURL(ctx, owner, repo, uint64(*p.Issue.Number), uint64(*p.Comment.ID)),
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
					ee.Payload = event.ChangeComment{
						ChangeTitle:    *p.Issue.Title,
						ChangeState:    state, // TODO: Verify "open", "closed", "merged"?
						CommentBody:    *p.Comment.Body,
						CommentHTMLURL: router.PullRequestURL(ctx, owner, repo, uint64(*p.Issue.Number), uint64(*p.Comment.ID)),
					}

					//default:
					//e.WIP = true
					//e.Action = component.Text(fmt.Sprintf("%v on a pull request in", *p.Action))
				}
			}
		case *githubV3.PullRequestReviewCommentEvent:
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
					//log.Println("convert: unsupported *githubV3.PullRequestReviewCommentEvent PullRequest.State:", *p.PullRequest.State)
				}

				ee.Payload = event.ChangeComment{
					ChangeTitle:    *p.PullRequest.Title,
					ChangeState:    state,
					CommentBody:    *p.Comment.Body,
					CommentHTMLURL: router.PullRequestURL(ctx, owner, repo, uint64(*p.PullRequest.Number), uint64(*p.Comment.ID)),
				}

				//default:
				//basicEvent.WIP = true
				//e.Action = component.Text(fmt.Sprintf("%v on a pull request in", *p.Action))
			}
		case *githubV3.CommitCommentEvent:
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
				Commit:      commit,
				CommentBody: *p.Comment.Body,
			}

		case *githubV3.PushEvent:
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

		case *githubV3.WatchEvent:
			ee.Payload = event.Star{}

		case *githubV3.CreateEvent:
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
		case *githubV3.ForkEvent:
			ee.Payload = event.Fork{
				Container: "github.com/" + *p.Forkee.FullName,
			}
		case *githubV3.DeleteEvent:
			ee.Payload = event.Delete{
				Type: *p.RefType, // TODO: Verify *p.RefType?
				Name: *p.Ref,
			}

		case *githubV3.GollumEvent:
			var pages []event.Page
			for _, p := range p.Pages {
				pages = append(pages, event.Page{
					Action:         *p.Action,
					SHA:            *p.SHA,
					Title:          *p.Title,
					HTMLURL:        *p.HTMLURL + "/" + *p.SHA,
					CompareHTMLURL: *p.HTMLURL + "/_compare/" + *p.SHA + "^..." + *p.SHA,
				})
			}
			ee.Payload = event.Wiki{
				Pages: pages,
			}
		}

		es = append(es, ee)
	}
	return es
}

// splitOwnerRepo splits "owner/repo" into "owner" and "repo".
func splitOwnerRepo(ownerRepo string) (owner, repo string) {
	i := strings.IndexByte(ownerRepo, '/')
	return ownerRepo[:i], ownerRepo[i+1:]
}
