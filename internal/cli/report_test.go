package cli

import (
	"path/filepath"
	"testing"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/testutil"
)

// reportHarness is hello with a preview of feature/login that runs a new web image, and a test
// that passed on both.
func reportHarness(t *testing.T) *harness {
	t.Helper()
	h := previewHarness(t)
	h.cluster.tests = []cluster.TestResult{{Name: "smoke", Phase: cluster.PhaseReady}}
	h.cluster.verifications = map[string]cluster.Verification{"hello--feature-login": {
		Verified: true, Source: "https://github.com/acme/hello", Commit: "3f2a9c1e5b7d9f1a3c5e7b9d1f3a5c7e9b1d3f5a",
		Scan: &cluster.Scan{Scanner: "trivy 0.75.0", ScannedAt: "2026-10-28T13:00:00Z", Images: []cluster.ScannedImage{
			{Component: "smoke", Image: "curlimages/curl:8.10.1@sha256:c0ff", Digest: "sha256:c0ff", Scanned: true},
			{Component: "web", Image: "ghcr.io/o/hello/web:sha-3f2a9c1@sha256:3f2a", Digest: "sha256:3f2a", Scanned: true, High: 4},
		}},
	}}
	return h
}

func TestReport(t *testing.T) {
	t.Parallel()
	h := reportHarness(t)
	testutil.Golden(t, filepath.Join("testdata", "report-preview.md"),
		[]byte(h.mustRun(t, "app", "report", "hello", "--preview", "feature/login")))
	testutil.Golden(t, filepath.Join("testdata", "report-app.md"), []byte(h.mustRun(t, "app", "report", "hello")))
	goldenJSON(t, "report-preview", runJSON(t, h, 0, "app", "report", "hello", "--preview", "feature-login"))
}

// A failed test shows up in the report with the end of its log, and the first stage that is not
// Ready says where.
func TestReportOfAFailedTest(t *testing.T) {
	t.Parallel()
	testutil.Golden(t, filepath.Join("testdata", "report-test-failed.md"),
		[]byte(failedTestHarness(t).mustRun(t, "app", "report", "hello")))
}
