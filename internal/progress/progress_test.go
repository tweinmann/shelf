package progress_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/tweinmann/shelf/internal/progress"
	"github.com/tweinmann/shelf/internal/testutil"
)

// TestWriter pins how every kind of event is rendered. These are the lines shelf has printed
// since Phase 3, and the golden file is what keeps them that way now that they are assembled
// from events instead of printed on the spot.
func TestWriter(t *testing.T) {
	t.Parallel()
	events := []progress.Event{
		progress.Info("Flux Operator v0.60.0: 42 objects, 40 created, 2 unchanged"),
		progress.Step("the operator"),
		progress.StepDone("the operator", 7*time.Second, ""),
		progress.Applied("Namespace shelf-system", "created", ""),
		progress.Applied("Secret shelf-system/registry", "configured", "for tobi@ghcr.io"),
		progress.Applied("Secret shelf-system/registry", "kept (no GHCR_TOKEN given)", ""),
		progress.Warning("The apps move from <app>.old.example to <app>.new.example.\n" +
			"Their records under the old name stay behind; delete them in Cloudflare:\n" +
			"  greeter.old.example\n"),
		progress.Secret("db-password", "generated"),
		progress.Secret("api-key", "restored from the backup"),
		progress.Info("secret backup: /home/tobi/.shelf/apps/greeter/secrets.yaml"),
		progress.Step("the deploy artifact"),
		progress.StepDone("the deploy artifact", 3*time.Second, "sha-905acce"),
		progress.Step("the app"),
		progress.StepFailed("the app"),
		progress.Record("greeter.example.com", "t-1.cfargotunnel.com", "created"),
		progress.Record("greeter.example.com", "", "deleted"),
	}
	var buf bytes.Buffer
	rep := progress.Writer(&buf)
	for _, e := range events {
		rep.Report(e)
	}
	testutil.Golden(t, "testdata/writer.txt", buf.Bytes())
}

// TestDiscardAndNil checks that an operation may report without a reporter.
func TestDiscardAndNil(t *testing.T) {
	t.Parallel()
	for name, rep := range map[string]progress.Reporter{
		"discard": progress.Discard,
		"nil":     progress.OrDiscard(nil),
	} {
		t.Run(name, func(t *testing.T) {
			rep.Report(progress.Info("nobody reads this"))
		})
	}
}

// TestOrDiscardKeepsAReporter makes sure OrDiscard does not swallow a real one.
func TestOrDiscardKeepsAReporter(t *testing.T) {
	t.Parallel()
	var got []progress.Event
	rep := progress.OrDiscard(progress.ReporterFunc(func(e progress.Event) { got = append(got, e) }))
	rep.Report(progress.Info("kept"))
	if len(got) != 1 || got[0].Message != "kept" {
		t.Errorf("events %+v", got)
	}
}
