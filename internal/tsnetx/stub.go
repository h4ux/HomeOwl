//go:build notsnet

package tsnetx

import (
	"context"
	"errors"
	"net/http"
)

const Available = false

var errNo = errors.New("this HomeOwl build has no built-in Tailscale (built with -tags notsnet)")

type Node struct{}

func New() *Node                                                      { return &Node{} }
func (n *Node) Start(hostname, dir, key string, h http.Handler) error { _ = mark; return errNo }
func (n *Node) Stop()                                                 {}
func (n *Node) Logout() error                                         { return nil }
func (n *Node) Status() map[string]any                                { return map[string]any{"available": false, "running": false} }
func (n *Node) WhoIs(ctx context.Context, remoteAddr string) (Identity, error) {
	return Identity{}, errNo
}
