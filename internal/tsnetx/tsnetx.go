// Package tsnetx runs HomeOwl as its own device on a Tailscale network (tailnet) using tsnet, so it gets a
// https://<name>.<tailnet>.ts.net address and a real certificate without the Tailscale app or `tailscale serve`.
// Builds made with `-tags notsnet` leave this out (see stub.go).
package tsnetx

import (
	"context"
	"net/http"
)

type ctxKey struct{}

// Identity of the tailnet user behind a request.
type Identity struct {
	Login  string
	Name   string
	Tagged bool
}

// FromTailnet reports whether the request arrived over the tailnet listener.
func FromTailnet(r *http.Request) bool {
	v, _ := r.Context().Value(ctxKey{}).(bool)
	return v
}

func mark(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, true)))
	})
}
