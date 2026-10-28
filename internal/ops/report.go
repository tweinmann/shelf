package ops

import (
	"context"

	"github.com/tweinmann/shelf/internal/cluster"
)

// Report is what an agent puts into its pull request: how the branch's preview, or the app,
// is doing, with its tests, its verification and its isolation, and for a preview what it runs
// differently from the app. Everything comes from the cluster, never from Git.
type Report struct {
	// App is the app or the preview, with its verification and its isolation.
	App App
	// Branch is the preview's branch, empty for the app.
	Branch string
	// Diagnosis is the chain with the tests.
	Diagnosis cluster.Diagnosis
	// Comparison is what the preview runs differently from its app, nil for the app.
	Comparison *Comparison
}

// Comparison sets the images of a preview against those of its app, by component.
type Comparison struct {
	// App is the app the preview belongs to, and Commit the commit the app runs.
	App    string
	Commit string
	// Comparable is false when one side has no scan to read its images from; Reason says which.
	Comparable bool
	Reason     string
	// Changed are the components whose image differs, or that only one side has, by name.
	Changed []ImageChange
	// Critical and High are the findings of all of the preview's images, AppCritical and AppHigh
	// those of the app's.
	Critical, High       int
	AppCritical, AppHigh int
}

// ImageChange is one component the preview runs another image of than its app. A component only
// one side has leaves the other side's digest empty.
type ImageChange struct {
	Component            string
	Digest, AppDigest    string
	Critical, High       int
	AppCritical, AppHigh int
}

// Report puts together the report of an app, or of the preview of one of its branches. It
// changes nothing.
func (o *Ops) Report(ctx context.Context, name, branch string) (Report, error) {
	app, err := o.App(ctx, name)
	if err != nil {
		return Report{}, err
	}
	r := Report{App: app, Branch: branch}
	if branch != "" {
		preview, err := o.Preview(ctx, name, branch)
		if err != nil {
			return Report{}, err
		}
		r.App = preview
		r.Comparison = compare(preview, app)
	}
	if r.Diagnosis, err = o.Cluster.AppDiagnosis(ctx, o.config(), r.App.Name); err != nil {
		return Report{}, err
	}
	return r, nil
}

// compare sets the scanned images of a preview against those of its app.
func compare(preview, app App) *Comparison {
	c := &Comparison{App: app.Name, Commit: app.Commit}
	scan := func(a App) *cluster.Scan {
		if a.Verification == nil {
			return nil
		}
		return a.Verification.Scan
	}
	ps, as := scan(preview), scan(app)
	switch {
	case ps == nil:
		c.Reason = "the preview has no scan to read its images from"
		return c
	case as == nil:
		c.Reason = "the app has no scan to read its images from"
		return c
	}
	c.Comparable = true
	theirs := map[string]cluster.ScannedImage{}
	for _, img := range as.Images {
		theirs[img.Component] = img
		c.AppCritical += img.Critical
		c.AppHigh += img.High
	}
	ours := map[string]bool{}
	for _, img := range ps.Images {
		ours[img.Component] = true
		c.Critical += img.Critical
		c.High += img.High
		other, ok := theirs[img.Component]
		if ok && other.Digest == img.Digest {
			continue
		}
		change := ImageChange{Component: img.Component, Digest: img.Digest, Critical: img.Critical, High: img.High}
		if ok {
			change.AppDigest, change.AppCritical, change.AppHigh = other.Digest, other.Critical, other.High
		}
		c.Changed = append(c.Changed, change)
	}
	for _, img := range as.Images {
		if !ours[img.Component] {
			c.Changed = append(c.Changed, ImageChange{Component: img.Component, AppDigest: img.Digest,
				AppCritical: img.Critical, AppHigh: img.High})
		}
	}
	return c
}
