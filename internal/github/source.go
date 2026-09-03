package github

const (
	Host          = "github.com"
	DefaultGitRef = "HEAD"
)

type Source struct {
	Owner  string
	Repo   string
	GitRef string
	Path   string
}
