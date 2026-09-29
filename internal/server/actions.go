package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/ops"
	"github.com/tweinmann/shelf/internal/progress"
)

// mutate starts a change and sends the browser to the page that watches it. Everything that
// changes the cluster goes through here: the form token is checked, only one change runs at a
// time, and the change is written to the audit log before it starts.
func (s *Server) mutate(w http.ResponseWriter, r *http.Request, title, app string,
	run func(context.Context, progress.Reporter) error) {
	if !checkCSRF(r) {
		s.fail(w, r, http.StatusForbidden, "this form is out of date; load the page again")
		return
	}
	job, err := s.jobs.start(title, app, run)
	if errors.Is(err, ErrBusy) {
		s.render(w, "busy.html", http.StatusConflict, busyView{
			base:    s.base(r, "One at a time"),
			Running: job,
		})
		return
	}
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	// A change that cannot be recorded still happens; saying so is better than hiding it.
	if err := s.store.Audit(s.now(), title, "job "+job.ID); err != nil {
		s.jobs.append(job.ID, progress.Info("the audit log could not be written: %v", err))
	}
	http.Redirect(w, r, "/jobs/"+job.ID, http.StatusSeeOther)
}

// newAppForm is the page that asks for a name and an artifact.
func (s *Server) newAppForm(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r)
	defer cancel()
	status := s.platform.Status(ctx)
	s.render(w, "new.html", http.StatusOK, newAppView{
		base:        s.base(r, "Add an app"),
		Status:      status,
		Connections: s.connections(ctx),
		Insecure:    status.Settings.InsecureRegistry,
	})
}

// addApp registers an app from its deploy artifact.
func (s *Server) addApp(w http.ResponseWriter, r *http.Request) {
	form := newAppView{
		Name:       r.PostFormValue("name"),
		Artifact:   r.PostFormValue("artifact"),
		Insecure:   r.PostFormValue("insecure") != "",
		Domain:     strings.TrimSpace(r.PostFormValue("domain")),
		Registry:   r.PostFormValue("registry"),
		Cloudflare: r.PostFormValue("cloudflare"),
	}
	if err := ops.CheckAppName(form.Name); err != nil {
		s.failedForm(w, r, form, err)
		return
	}
	artifact, err := cluster.ParseArtifact(form.Artifact)
	if err != nil {
		s.failedForm(w, r, form, err)
		return
	}
	access, err := accessFromForm(r)
	if err != nil {
		s.failedForm(w, r, form, err)
		return
	}
	s.mutate(w, r, "Add "+form.Name, form.Name, func(ctx context.Context, rep progress.Reporter) error {
		return s.platform.AddApp(ctx, ops.AddOptions{
			Name:     form.Name,
			Artifact: artifact,
			Insecure: form.Insecure,
			Access:   access,
			Timeout:  jobTimeout,
		}, rep)
	})
}

// accessFromForm reads what a form chooses for an app: a domain, and a connection of each kind.
// The choice of a connection is the whole of it, so "none" takes one away. An empty domain keeps
// the one the app has.
func accessFromForm(r *http.Request) (ops.Access, error) {
	a := ops.Access{
		Domain:     strings.TrimSpace(r.PostFormValue("domain")),
		Registry:   r.PostFormValue("registry"),
		Cloudflare: r.PostFormValue("cloudflare"),
	}
	a.RemoveRegistry, a.RemoveCloudflare = a.Registry == "", a.Cloudflare == ""
	return a, a.Check()
}

// failedForm shows the add form again with what was typed and what is wrong with it.
func (s *Server) failedForm(w http.ResponseWriter, r *http.Request, form newAppView, err error) {
	ctx, cancel := withTimeout(r)
	defer cancel()
	form.base = s.base(r, "Add an app")
	form.Status = s.platform.Status(ctx)
	form.Connections = s.connections(ctx)
	form.Error = err.Error()
	s.render(w, "new.html", http.StatusBadRequest, form)
}

// connections are what a form offers to choose from. A cluster that cannot list them leaves
// the lists empty; the operation itself says what is wrong. What each connection offers in turn
// — packages, tags, zones — the page fetches from the api routes below once one is chosen.
func (s *Server) connections(ctx context.Context) ops.Connections {
	conns, err := s.platform.Connections(ctx)
	if err != nil {
		return ops.Connections{}
	}
	return conns
}

// setAccess changes an app's domain and connections, and deploys it again from the artifact it
// is registered with.
func (s *Server) setAccess(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := ops.CheckAppName(name); err != nil {
		s.fail(w, r, http.StatusNotFound, err.Error())
		return
	}
	access, err := accessFromForm(r)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, err.Error())
		return
	}
	s.mutate(w, r, "Change the connections of "+name, name, func(ctx context.Context, rep progress.Reporter) error {
		return s.platform.SetAccess(ctx, name, access, jobTimeout, rep)
	})
}

// deployApp points an app at another artifact or tag. It is the same operation as adding it,
// which is why a rollback is simply the tag of the version that worked.
func (s *Server) deployApp(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := ops.CheckAppName(name); err != nil {
		s.fail(w, r, http.StatusNotFound, err.Error())
		return
	}
	artifact, err := cluster.ParseArtifact(r.PostFormValue("artifact"))
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, err.Error())
		return
	}
	insecure := r.PostFormValue("insecure") != ""
	s.mutate(w, r, "Deploy "+name+" from "+artifact.String(), name,
		func(ctx context.Context, rep progress.Reporter) error {
			return s.platform.AddApp(ctx, ops.AddOptions{
				Name:     name,
				Artifact: artifact,
				Insecure: insecure,
				Timeout:  jobTimeout,
			}, rep)
		})
}

// redeployApp asks Flux to fetch the artifact again and roll out what it finds.
func (s *Server) redeployApp(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := ops.CheckAppName(name); err != nil {
		s.fail(w, r, http.StatusNotFound, err.Error())
		return
	}
	s.mutate(w, r, "Redeploy "+name, name, func(ctx context.Context, rep progress.Reporter) error {
		return s.platform.Redeploy(ctx, name, jobTimeout, rep)
	})
}

// deleteApp removes an app with its volumes. The form has to carry the name, typed out: this is
// the one action in the admin UI that destroys data.
func (s *Server) deleteApp(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := ops.CheckAppName(name); err != nil {
		s.fail(w, r, http.StatusNotFound, err.Error())
		return
	}
	if r.PostFormValue("confirm") != name {
		s.fail(w, r, http.StatusBadRequest,
			"to remove "+name+" with its volumes, type its name in the field next to the button")
		return
	}
	s.mutate(w, r, "Remove "+name, "", func(ctx context.Context, rep progress.Reporter) error {
		return s.platform.RemoveApp(ctx, name, jobTimeout, rep)
	})
}

// revealSecrets shows the generated values after the password is entered again. The values are
// generated, so this is the only place to read them; asking for the password again means a
// borrowed browser does not hand them over.
func (s *Server) revealSecrets(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !checkCSRF(r) {
		s.fail(w, r, http.StatusForbidden, "this form is out of date; load the page again")
		return
	}
	if !s.admin.checkPassword(r.PostFormValue("password")) {
		s.showApp(w, r, name, nil, "wrong password")
		return
	}
	ctx, cancel := withTimeout(r)
	defer cancel()
	values, err := s.platform.Secrets(ctx, name)
	if err != nil {
		s.fail(w, r, http.StatusBadGateway, err.Error())
		return
	}
	if err := s.store.Audit(s.now(), "Reveal the secrets of "+name, ""); err != nil {
		s.fail(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	s.showApp(w, r, name, values, "")
}

// jobPage shows what a change has reported so far. It renders the log it has and lets the
// browser attach to the rest, so a reload during a five-minute change loses nothing.
func (s *Server) jobPage(w http.ResponseWriter, r *http.Request) {
	job, ok := s.jobs.get(r.PathValue("id"))
	if !ok {
		s.fail(w, r, http.StatusNotFound, "that change is not in the list any more")
		return
	}
	s.render(w, "job.html", http.StatusOK, jobView{
		base: s.base(r, job.Title),
		Job:  job,
		Log:  job.Log(),
		From: len(job.Events),
	})
}

// jobEvents streams what a change reports, as server-sent events. Each event carries the piece
// of text the command line would have printed, so the log in the browser reads the same.
func (s *Server) jobEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.fail(w, r, http.StatusInternalServerError, "this connection cannot stream")
		return
	}
	from := 0
	if last := r.Header.Get("Last-Event-ID"); last != "" {
		if n, err := strconv.Atoi(last); err == nil {
			from = n + 1
		}
	} else if n, err := strconv.Atoi(r.URL.Query().Get("from")); err == nil {
		from = n
	}
	events, cancel, ok := s.jobs.watch(r.PathValue("id"), from)
	if !ok {
		http.Error(w, "no such change", http.StatusNotFound)
		return
	}
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	index := from
	for {
		select {
		case <-r.Context().Done():
			return
		case e, open := <-events:
			if !open {
				job, _ := s.jobs.get(r.PathValue("id"))
				writeEvent(w, "end", index, endData(job))
				flusher.Flush()
				return
			}
			writeEvent(w, "line", index, map[string]string{"text": progress.Text(e)})
			index++
			flusher.Flush()
		}
	}
}

func endData(job *Job) map[string]string {
	if job == nil {
		return map[string]string{}
	}
	return map[string]string{"error": job.Err}
}

// writeEvent sends one server-sent event. The payload is JSON, which cannot contain a raw line
// break, so a piece of text with one in it still arrives as a single event.
func writeEvent(w http.ResponseWriter, kind string, id int, data any) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\nid: %d\ndata: %s\n\n", kind, id, encoded)
}
