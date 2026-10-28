package ops

import (
	"context"
	"fmt"
	"strings"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/schema"
)

// PreviewName is the namespace, and the name everywhere else, of the preview of a branch of an
// app. The branch may be given as its name or as its slug.
func PreviewName(app, branch string) string {
	return cluster.PreviewNamespace(app, schema.BranchSlug(branch))
}

// PreviewNotFoundError means an app has no preview for a branch; it names the ones it has.
type PreviewNotFoundError struct {
	App, Branch string
	// Previews are the branch slugs of the previews the app has.
	Previews []string
	// Off means the app has no previews switched on at all.
	Off bool
}

func (e *PreviewNotFoundError) Error() string {
	switch {
	case e.Off:
		return fmt.Sprintf("app %s has no previews; `shelf app credentials %s --previews` switches them on", e.App, e.App)
	case len(e.Previews) == 0:
		return fmt.Sprintf("app %s has no preview of branch %s, and no previews yet", e.App, e.Branch)
	}
	return fmt.Sprintf("app %s has no preview of branch %s; it has previews of %s", e.App, e.Branch,
		strings.Join(e.Previews, ", "))
}

// Preview returns the preview of a branch of an app, with its address and its components, as an
// App whose Name is the preview's namespace. A preview answers inside the cluster only.
func (o *Ops) Preview(ctx context.Context, name, branch string) (App, error) {
	a, err := o.App(ctx, name)
	if err != nil {
		return App{}, err
	}
	want := PreviewName(name, branch)
	for _, p := range a.Previews {
		if p.Name != want {
			continue
		}
		preview := App{AppState: p.AppState, Host: PreviewHost(p.Name)}
		list, err := o.Cluster.AppComponents(ctx, o.config(), p.Name)
		if err != nil {
			return App{}, err
		}
		preview.Components = components(list, preview.Host, false)
		settings, err := o.Cluster.Settings(ctx, o.config())
		if err != nil {
			return App{}, err
		}
		if preview.Verification, err = o.verify(ctx, p.Name, settings, true); err != nil {
			return App{}, err
		}
		// A preview never gets the app's LAN exceptions.
		if preview.Isolation, err = o.isolation(ctx, p.Name, nil); err != nil {
			return App{}, err
		}
		return preview, nil
	}
	missing := &PreviewNotFoundError{App: name, Branch: branch, Off: a.PreviewLimit == 0}
	for _, p := range a.Previews {
		missing.Previews = append(missing.Previews, p.Slug)
	}
	return App{}, missing
}

// PreviewHost is where a preview answers: <app>--<slug>.shelf.internal, never under the app's
// own domain, whatever that is.
func PreviewHost(namespace string) string { return namespace + "." + cluster.InternalDomain }
