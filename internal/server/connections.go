package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/hostcfg"
	"github.com/tweinmann/shelf/internal/ops"
	"github.com/tweinmann/shelf/internal/progress"
)

// connectionsView is the page that defines the connections apps use. It is the only page with
// token fields, and it never fills one in.
type connectionsView struct {
	base
	Connections ops.Connections
	// Error is why a form was refused, or why the connections cannot be listed.
	Error string
	// Kind is the form the error belongs to, so it is shown next to it.
	Kind string
	// Registry and Cloudflare keep what was typed into a refused form, without the token.
	Registry   cluster.RegistryConnection
	Cloudflare ops.CloudflareConnection
	Running    *Job
}

// connectionsPage lists the connections with the apps that use them.
func (s *Server) connectionsPage(w http.ResponseWriter, r *http.Request) {
	s.showConnections(w, r, http.StatusOK, connectionsView{})
}

func (s *Server) showConnections(w http.ResponseWriter, r *http.Request, status int, view connectionsView) {
	ctx, cancel := withTimeout(r)
	defer cancel()
	view.base = s.base(r, "Connections")
	view.Running = s.jobs.runningJob()
	conns, err := s.platform.Connections(ctx)
	if err != nil && view.Error == "" {
		view.Error = err.Error()
	}
	view.Connections = conns
	s.render(w, "connections.html", status, view)
}

// saveRegistryConnection defines a registry connection, or gives one a new login.
func (s *Server) saveRegistryConnection(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PostFormValue("name"))
	auth := cluster.RegistryAuth{
		Username: strings.TrimSpace(r.PostFormValue("username")),
		Token:    strings.TrimSpace(r.PostFormValue("token")),
	}
	err := ops.CheckConnectionName(name)
	if err == nil && (auth.Username == "" || auth.Token == "") {
		err = errors.New("a registry connection needs a user name and a token")
	}
	if err != nil {
		s.showConnections(w, r, http.StatusBadRequest, connectionsView{
			Error: err.Error(), Kind: ops.ConnectionRegistry,
			Registry: cluster.RegistryConnection{Name: name, Username: auth.Username},
		})
		return
	}
	s.mutate(w, r, "Save the registry connection "+name, "", func(ctx context.Context, rep progress.Reporter) error {
		return s.platform.SaveRegistryConnection(ctx, name, auth, rep)
	})
}

// saveCloudflareConnection defines a Cloudflare connection, or gives one a new token.
func (s *Server) saveCloudflareConnection(w http.ResponseWriter, r *http.Request) {
	conn := hostcfg.Cloudflare{
		Name:    strings.TrimSpace(r.PostFormValue("name")),
		Token:   strings.TrimSpace(r.PostFormValue("token")),
		Account: strings.TrimSpace(r.PostFormValue("account")),
	}
	err := ops.CheckConnectionName(conn.Name)
	if err == nil && conn.Token == "" {
		err = errors.New("a Cloudflare connection needs an API token")
	}
	if err != nil {
		s.showConnections(w, r, http.StatusBadRequest, connectionsView{
			Error: err.Error(), Kind: ops.ConnectionCloudflare,
			Cloudflare: ops.CloudflareConnection{Name: conn.Name, Account: conn.Account},
		})
		return
	}
	s.mutate(w, r, "Save the Cloudflare connection "+conn.Name, "", func(ctx context.Context, rep progress.Reporter) error {
		return s.platform.SaveCloudflareConnection(ctx, conn, rep)
	})
}

// removeConnection deletes a connection. The operation refuses one that apps still use.
func (s *Server) removeConnection(w http.ResponseWriter, r *http.Request) {
	kind, name := r.PathValue("kind"), r.PathValue("name")
	if kind != ops.ConnectionRegistry && kind != ops.ConnectionCloudflare {
		s.fail(w, r, http.StatusNotFound, "there is no kind of connection called "+kind)
		return
	}
	if err := ops.CheckConnectionName(name); err != nil {
		s.fail(w, r, http.StatusNotFound, err.Error())
		return
	}
	s.mutate(w, r, "Remove the "+kind+" connection "+name, "", func(ctx context.Context, rep progress.Reporter) error {
		return s.platform.RemoveConnection(ctx, kind, name, rep)
	})
}
