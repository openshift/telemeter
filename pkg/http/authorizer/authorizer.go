package authorizer

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/smarterclayton/telemeter/pkg/authorizer"
)

type Authorizer struct {
	parent     http.Handler
	authorizer authorizer.Interface
}

func New(parent http.Handler, authorizer authorizer.Interface) http.Handler {
	return &Authorizer{
		parent:     parent,
		authorizer: authorizer,
	}
}

func (a *Authorizer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	auth := strings.SplitN(req.Header.Get("Authorization"), " ", 2)
	if strings.ToLower(auth[0]) != "bearer" {
		http.Error(w, "Only bearer authorization allowed", http.StatusUnauthorized)
		return
	}
	if len(auth) != 2 || len(strings.TrimSpace(auth[1])) == 0 {
		http.Error(w, "Invalid Authorization header", http.StatusUnauthorized)
		return
	}

	user, ok, err := a.authorizer.AuthorizeToken(auth[1])
	if err != nil {
		http.Error(w, fmt.Sprintf("Not authorized: %v", err), http.StatusUnauthorized)
		return
	}
	if !ok {
		http.Error(w, "Not authorized", http.StatusUnauthorized)
		return
	}
	req = req.WithContext(authorizer.WithUser(req.Context(), user))
	a.parent.ServeHTTP(w, req)
}
