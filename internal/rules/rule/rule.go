package rule

import (
	"net/url"

	"github.com/dadrus/heimdall/internal/heimdall"
)

type Rule interface {
	ID() string
	SrcID() string
	Execute(heimdall.Context) (*url.URL, error)
	MatchesURL(*url.URL) bool
	MatchesMethod(string) bool
}
