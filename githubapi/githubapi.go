// Package githubapi implements events.Service using GitHub API client.
package githubapi

import (
	"context"
	"encoding/base64"
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
	"dmitri.shuralyov.com/state"
	githubv3 "github.com/google/go-github/github"
	"github.com/shurcooL/events"
	"github.com/shurcooL/events/event"
	"github.com/shurcooL/githubv4"
	"github.com/shurcooL/users"
)

// NewService creates a GitHub-backed events.Service using given GitHub client.
// It fetches events only for the specified user. user.Domain must be "github.com".
//
// If router is nil, github.DotCom router is used, which links to subjects on github.com.
func NewService(clientV3 *githubv3.Client, clientV4 *githubv4.Client, user users.User, router github.Router) (events.Service, error) {
	if user.Domain != "github.com" {
		return nil, fmt.Errorf(`user.Domain is %q, it must be "github.com"`, user.Domain)
	}
	if router == nil {
		router = github.DotCom{}
	}
	s := &service{
		clV3: clientV3,
		clV4: clientV4,
		user: user,
		rtr:  router,
	}
	go s.poll()
	return s, nil
}

type service struct {
	clV3 *githubv3.Client // GitHub REST API v3 client.
	clV4 *githubv4.Client // GitHub GraphQL API v4 client.
	user users.User
	rtr  github.Router

	mu         sync.Mutex
	events     []*githubv3.Event
	commits    map[string]event.Commit // SHA -> Commit.
	prs        map[string]bool         // PR API URL -> Pull Request merged.
	fetchError error
}

// List lists events.
func (s *service) List(ctx context.Context) ([]event.Event, error) {
	s.mu.Lock()
	events, commits, prs, fetchError := s.events, s.commits, s.prs, s.fetchError
	s.mu.Unlock()
	return convert(ctx, events, commits, prs, s.rtr), fetchError
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
		s.mu.Lock()
		commits := make(map[string]event.Commit, len(s.commits))
		for sha, c := range s.commits {
			commits[sha] = c
		}
		s.mu.Unlock()
		events, commits, prs, pollInterval, fetchError := s.fetchEvents(context.Background(), commits)
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
// Provided commits must be non-nil, and it's used as a starting point.
// Only missing commits are fetched, and unused commits are removed at the end.
func (s *service) fetchEvents(
	ctx context.Context,
	commits map[string]event.Commit,
) (
	events []*githubv3.Event,
	_ map[string]event.Commit,
	prs map[string]bool,
	pollInterval time.Duration,
	err error,
) {
	// TODO: Investigate this:
	//       Events support pagination, however the per_page option is unsupported. The fixed page size is 30 items. Fetching up to ten pages is supported, for a total of 300 events.
	events, resp, err := s.clV3.Activity.ListEventsPerformedByUser(ctx, s.user.Login, true, &githubv3.ListOptions{PerPage: 100})
	if err != nil {
		return nil, nil, nil, 0, err
	}
	if pi, err := strconv.Atoi(resp.Header.Get("X-Poll-Interval")); err == nil {
		pollInterval = time.Duration(pi) * time.Second
	}
	prs = make(map[string]bool)
	used := make(map[string]bool) // A set of used commit SHAs.
	for _, e := range events {
		payload, err := e.ParsePayload()
		if err != nil {
			return nil, nil, nil, 0, fmt.Errorf("fetchEvents: ParsePayload failed: %v", err)
		}
		switch p := payload.(type) {
		case *githubv3.PushEvent:
			for _, c := range p.Commits {
				used[*c.SHA] = true
				if _, ok := commits[*c.SHA]; ok {
					continue
				}
				commit, err := s.fetchCommit(ctx, *e.Repo.ID, *c.SHA)
				if err != nil && strings.HasPrefix(err.Error(), "Could not resolve to a node ") { // E.g., because the repo was deleted.
					log.Printf("fetchEvents: commit %s@%s was not found: %v\n", *e.Repo.Name, *c.SHA, err)

					avatarURL := "https://secure.gravatar.com/avatar?d=mm&f=y&s=96"
					if *c.Author.Email == s.user.Email {
						avatarURL = s.user.AvatarURL
					}
					commit = event.Commit{
						SHA:             *c.SHA,
						Message:         *c.Message,
						AuthorAvatarURL: avatarURL,
					}
				} else if err != nil {
					return nil, nil, nil, 0, fmt.Errorf("fetchCommit: %v", err)
				}
				commits[*c.SHA] = commit
			}
		case *githubv3.CommitCommentEvent:
			used[*p.Comment.CommitID] = true
			if _, ok := commits[*p.Comment.CommitID]; ok {
				continue
			}
			commit, err := s.fetchCommit(ctx, *e.Repo.ID, *p.Comment.CommitID)
			if err != nil && strings.HasPrefix(err.Error(), "Could not resolve to a node ") { // E.g., because the repo was deleted.
				log.Printf("fetchEvents: commit %s@%s was not found: %v\n", *e.Repo.Name, *p.Comment.CommitID, err)

				commit = event.Commit{
					SHA:             *p.Comment.CommitID,
					AuthorAvatarURL: "https://secure.gravatar.com/avatar?d=mm&f=y&s=96",
				}
			} else if err != nil {
				return nil, nil, nil, 0, fmt.Errorf("fetchCommit: %v", err)
			}
			commits[*p.Comment.CommitID] = commit

		case *githubv3.IssueCommentEvent:
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
	for sha := range commits {
		if !used[sha] {
			delete(commits, sha)
		}
	}
	return events, commits, prs, pollInterval, nil
}

// fetchCommit fetches the specified commit.
func (s *service) fetchCommit(ctx context.Context, repoID int64, sha string) (event.Commit, error) {
	// TODO: It'd be better to be able to batch and fetch all commits at once (in fetchEvents loop),
	//       rather than making an individual query for each.
	//       See https://github.com/shurcooL/githubv4/issues/17.

	commitID := fmt.Sprintf("06:Commit%d:%s", repoID, sha)
	var q struct {
		Node struct {
			Commit struct {
				OID     string
				Message string
				Author  struct {
					AvatarURL string `graphql:"avatarUrl(size:96)"`
				}
				URL string
			} `graphql:"...on Commit"`
		} `graphql:"node(id:$commitID)"`
	}
	variables := map[string]interface{}{
		"commitID": githubv4.ID(base64.StdEncoding.EncodeToString([]byte(commitID))), // HACK, TODO: Confirm StdEncoding vs URLEncoding.
	}
	err := s.clV4.Query(ctx, &q, variables)
	if err != nil {
		return event.Commit{}, err
	}
	return event.Commit{
		SHA:             q.Node.Commit.OID,
		Message:         q.Node.Commit.Message,
		AuthorAvatarURL: q.Node.Commit.Author.AvatarURL,
		HTMLURL:         q.Node.Commit.URL,
	}, nil
}

// fetchPullRequestMerged fetches whether the Pull Request at the API URL is merged
// at current time.
func (s *service) fetchPullRequestMerged(ctx context.Context, prURL string) (bool, error) {
	// https://developer.github.com/v3/pulls/#get-if-a-pull-request-has-been-merged.
	req, err := s.clV3.NewRequest("GET", prURL+"/merge", nil)
	if err != nil {
		return false, err
	}
	resp, err := s.clV3.Do(ctx, req, nil)
	switch e, ok := err.(*githubv3.ErrorResponse); {
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
func convert(
	ctx context.Context,
	events []*githubv3.Event,
	commits map[string]event.Commit,
	prs map[string]bool,
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
			panic(fmt.Errorf("internal error: convert given a githubv3.Event with an invalid payload: %v", err))
		}
		switch p := payload.(type) {
		case *githubv3.IssuesEvent:
			switch *p.Action {
			case "opened", "closed", "reopened":

				//default:
				//log.Println("convert: unsupported *githubv3.IssuesEvent action:", *p.Action)
			}
			ee.Payload = event.Issue{
				Action:       *p.Action,
				IssueTitle:   *p.Issue.Title,
				IssueHTMLURL: router.IssueURL(ctx, owner, repo, uint64(*p.Issue.Number)),
			}
		case *githubv3.PullRequestEvent:
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
				//log.Println("convert: unsupported *githubv3.PullRequestEvent PullRequest.State:", *p.PullRequest.State, "PullRequest.Merged:", *p.PullRequest.Merged)
			}
			ee.Payload = event.Change{
				Action:        action,
				ChangeTitle:   *p.PullRequest.Title,
				ChangeHTMLURL: router.PullRequestURL(ctx, owner, repo, uint64(*p.PullRequest.Number)),
			}

		case *githubv3.IssueCommentEvent:
			switch p.Issue.PullRequestLinks {
			case nil: // Issue.
				switch *p.Action {
				case "created":
					var issueState state.Issue
					switch *p.Issue.State {
					case "open":
						issueState = state.IssueOpen
					case "closed":
						issueState = state.IssueClosed
					default:
						log.Printf("convert: unsupported *githubv3.IssueCommentEvent (issue): Issue.State=%v\n", *p.Issue.State)
						continue
					}
					ee.Payload = event.IssueComment{
						IssueTitle:     *p.Issue.Title,
						IssueState:     issueState,
						CommentBody:    *p.Comment.Body,
						CommentHTMLURL: router.IssueCommentURL(ctx, owner, repo, uint64(*p.Issue.Number), uint64(*p.Comment.ID)),
					}

					//default:
					//e.WIP = true
					//e.Action = component.Text(fmt.Sprintf("%v on an issue in", *p.Action))
				}
			default: // Pull Request.
				switch *p.Action {
				case "created":
					var changeState state.Change
					// Note, State is PR state at the time of event, but merged is PR merged at current time.
					// So, only check merged when State is closed. It's an approximation, but good enough in majority of cases.
					switch merged := prs[*p.Issue.PullRequestLinks.URL]; {
					case *p.Issue.State == "open":
						changeState = state.ChangeOpen
					case *p.Issue.State == "closed" && !merged:
						changeState = state.ChangeClosed
					case *p.Issue.State == "closed" && merged:
						changeState = state.ChangeMerged
					default:
						log.Printf("convert: unsupported *githubv3.IssueCommentEvent (pr): merged=%v Issue.State=%v\n", prs[*p.Issue.PullRequestLinks.URL], *p.Issue.State)
						continue
					}
					ee.Payload = event.ChangeComment{
						ChangeTitle:    *p.Issue.Title,
						ChangeState:    changeState,
						CommentBody:    *p.Comment.Body,
						CommentHTMLURL: router.PullRequestCommentURL(ctx, owner, repo, uint64(*p.Issue.Number), uint64(*p.Comment.ID)),
					}

					//default:
					//e.WIP = true
					//e.Action = component.Text(fmt.Sprintf("%v on a pull request in", *p.Action))
				}
			}
		case *githubv3.PullRequestReviewCommentEvent:
			switch *p.Action {
			case "created":
				var changeState state.Change
				switch {
				case p.PullRequest.MergedAt == nil && *p.PullRequest.State == "open":
					changeState = state.ChangeOpen
				case p.PullRequest.MergedAt == nil && *p.PullRequest.State == "closed":
					changeState = state.ChangeClosed
				case p.PullRequest.MergedAt != nil:
					changeState = state.ChangeMerged
				default:
					log.Printf("convert: unsupported *githubv3.PullRequestReviewCommentEvent: PullRequest.MergedAt=%v PullRequest.State=%v\n", p.PullRequest.MergedAt, *p.PullRequest.State)
					continue
				}
				ee.Payload = event.ChangeComment{
					ChangeTitle:    *p.PullRequest.Title,
					ChangeState:    changeState,
					CommentBody:    *p.Comment.Body,
					CommentHTMLURL: router.PullRequestReviewCommentURL(ctx, owner, repo, uint64(*p.PullRequest.Number), uint64(*p.Comment.ID)),
				}

				//default:
				//basicEvent.WIP = true
				//e.Action = component.Text(fmt.Sprintf("%v on a pull request in", *p.Action))
			}
		case *githubv3.CommitCommentEvent:
			ee.Payload = event.CommitComment{
				Commit:      commits[*p.Comment.CommitID],
				CommentBody: *p.Comment.Body,
			}

		case *githubv3.PushEvent:
			var cs []event.Commit
			for _, c := range p.Commits {
				cs = append(cs, commits[*c.SHA])
			}
			ee.Payload = event.Push{
				Branch:        strings.TrimPrefix(*p.Ref, "refs/heads/"),
				Head:          *p.Head,
				Before:        *p.Before,
				Commits:       cs,
				HeadHTMLURL:   "https://github.com/" + *e.Repo.Name + "/commit/" + *p.Head,
				BeforeHTMLURL: "https://github.com/" + *e.Repo.Name + "/commit/" + *p.Before,
			}

		case *githubv3.WatchEvent:
			ee.Payload = event.Star{}

		case *githubv3.CreateEvent:
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
		case *githubv3.ForkEvent:
			ee.Payload = event.Fork{
				Container: "github.com/" + *p.Forkee.FullName,
			}
		case *githubv3.DeleteEvent:
			ee.Payload = event.Delete{
				Type: *p.RefType, // TODO: Verify *p.RefType?
				Name: *p.Ref,
			}

		case *githubv3.GollumEvent:
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

		case *githubv3.MemberEvent:
			// Unsupported event type, skip it.
			continue

		default:
			log.Printf("convert: unexpected event type: %T\n", p)
			continue
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
