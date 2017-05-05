package event

// Commit describes a commit in an event.
type Commit struct {
	SHA             string
	CommitMessage   string
	AuthorAvatarURL string
	HTMLURL         string // Optional.
}

// Page describes an edit of a Wiki page in an event.
type Page struct {
	Action         string
	Title          string
	PageHTMLURL    string
	CompareHTMLURL string
}
