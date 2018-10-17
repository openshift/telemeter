package authorize

import (
	"fmt"
	"net/http"
	"strings"
)

func NewAuthorizeClientHandler(authorizer ClientAuthorizer, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		auth := strings.SplitN(req.Header.Get("Authorization"), " ", 2)
		if strings.ToLower(auth[0]) != "bearer" {
			http.Error(w, "Only bearer authorization allowed", http.StatusUnauthorized)
			return
		}
		if len(auth) != 2 || len(strings.TrimSpace(auth[1])) == 0 {
			http.Error(w, "Invalid Authorization header", http.StatusUnauthorized)
			return
		}

		client, ok, err := authorizer.AuthorizeClient(auth[1])
		if err != nil {
			http.Error(w, fmt.Sprintf("Not authorized: %v", err), http.StatusUnauthorized)
			return
		}
		if !ok {
			http.Error(w, "Not authorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, req.WithContext(WithClient(req.Context(), client)))
	})
}
