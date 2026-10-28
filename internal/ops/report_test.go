package ops

import (
	"testing"

	"github.com/tweinmann/shelf/internal/cluster"
)

func scanned(images ...cluster.ScannedImage) App {
	return App{
		AppState:     cluster.AppState{Name: "hello", Commit: "9956993c371777310050f0c10d4f9b90aebcb969"},
		Verification: &Verification{Verification: cluster.Verification{Scan: &cluster.Scan{Images: images}}},
	}
}

// TestCompare covers what a report says a preview runs differently from its app: the components
// whose image changed, those only one side has, and the findings of both.
func TestCompare(t *testing.T) {
	t.Parallel()
	app := scanned(
		cluster.ScannedImage{Component: "db", Digest: "sha256:db", Critical: 2, High: 86},
		cluster.ScannedImage{Component: "web", Digest: "sha256:web-old", Critical: 1, High: 26},
		cluster.ScannedImage{Component: "worker", Digest: "sha256:worker", High: 3},
	)
	preview := scanned(
		cluster.ScannedImage{Component: "db", Digest: "sha256:db", Critical: 2, High: 86},
		cluster.ScannedImage{Component: "smoke", Digest: "sha256:curl"},
		cluster.ScannedImage{Component: "web", Digest: "sha256:web-new", High: 4},
	)
	c := compare(preview, app)
	if !c.Comparable || c.App != "hello" || c.Commit != "9956993c371777310050f0c10d4f9b90aebcb969" {
		t.Fatalf("comparison %+v", c)
	}
	want := []ImageChange{
		{Component: "smoke", Digest: "sha256:curl"},
		{Component: "web", Digest: "sha256:web-new", High: 4, AppDigest: "sha256:web-old", AppCritical: 1, AppHigh: 26},
		{Component: "worker", AppDigest: "sha256:worker", AppHigh: 3},
	}
	if len(c.Changed) != len(want) {
		t.Fatalf("changed %+v, want %+v", c.Changed, want)
	}
	for i := range want {
		if c.Changed[i] != want[i] {
			t.Errorf("changed[%d] = %+v, want %+v", i, c.Changed[i], want[i])
		}
	}
	if c.Critical != 2 || c.High != 90 || c.AppCritical != 3 || c.AppHigh != 115 {
		t.Errorf("findings %d/%d against %d/%d", c.Critical, c.High, c.AppCritical, c.AppHigh)
	}

	unscanned := App{AppState: cluster.AppState{Name: "hello--x"}, Verification: &Verification{}}
	if c := compare(unscanned, app); c.Comparable || c.Reason != "the preview has no scan to read its images from" {
		t.Errorf("comparison without a scan %+v", c)
	}
	if c := compare(preview, unscanned); c.Comparable || c.Reason != "the app has no scan to read its images from" {
		t.Errorf("comparison without the app's scan %+v", c)
	}
}
