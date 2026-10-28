package cli

import (
	"strings"
	"testing"

	"github.com/tweinmann/shelf/internal/cluster"
)

// rolledOut is hello after the artifact of testCommit rolled out.
func rolledOut(pods cluster.Stage) cluster.Rollout {
	return cluster.Rollout{
		Diagnosis: cluster.Diagnosis{Name: "hello", Stages: chain(pods)},
		Revision:  "main@sha256:abc", Commit: testCommit,
		AppliedRevision: "main@sha256:abc", AttemptedRevision: "main@sha256:abc",
		ValuesDigest: "sha256:values", ReleasedDigest: "sha256:values", AttemptedDigest: "sha256:values",
		TestedDigest: "sha256:values",
	}
}

func waitHarness(t *testing.T, rollouts ...cluster.Rollout) *harness {
	t.Helper()
	h := newHarness(t).public()
	h.cluster.states = []cluster.AppState{helloState(cluster.PhaseReady)}
	h.cluster.rollouts = rollouts
	return h
}

func TestWaitVerdicts(t *testing.T) {
	t.Parallel()
	previous := rolledOut(podsReady)
	previous.Commit, previous.Revision = "9956993c371777310050f0c10d4f9b90aebcb969", "main@sha256:old"
	tests := []struct {
		name    string
		rollout cluster.Rollout
		args    []string
		code    int
		text    []string
	}{
		{name: "waitresult-ready", rollout: rolledOut(podsReady), args: []string{"--commit", "42a964e"}, code: 0,
			text: []string{"hello: Ready, running commit 42a964eb0b4b\n"}},
		{name: "waitresult-failed", rollout: rolledOut(podsFailed), code: 1, text: []string{
			"hello: Failed, running commit 42a964eb0b4b\n",
			"  the pods: ImagePullBackOff: Back-off pulling image\n",
			"  the image cannot be pulled; check the reference and whether the cluster may read that registry\n",
		}},
		{name: "waitresult-timedout", rollout: previous, args: []string{"--commit", "42a964e", "--timeout", "20ms"}, code: 2,
			text: []string{"hello: TimedOut, running commit 9956993c3717\n",
				"  the deploy artifact: waiting for commit 42a964e; running 9956993\n"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			args := append([]string{"app", "status", "hello", "--wait"}, tt.args...)
			goldenJSON(t, tt.name, runJSON(t, waitHarness(t, tt.rollout), tt.code, args...))

			stdout, stderr, code := waitHarness(t, tt.rollout).run(t, args...)
			if code != tt.code {
				t.Fatalf("exit code %d, want %d: %s", code, tt.code, stderr)
			}
			if want := strings.Join(tt.text, ""); stdout != want {
				t.Errorf("stdout:\n%s\nwant:\n%s", stdout, want)
			}
			if stderr != "" {
				t.Errorf("a verdict is not an error, but stderr says %q", stderr)
			}
		})
	}
}

func TestWaitFollowsTheRollout(t *testing.T) {
	t.Parallel()
	arriving := rolledOut(podsReady)
	arriving.Diagnosis.Stages[3].Phase = cluster.PhaseWorking
	h := waitHarness(t, arriving, rolledOut(podsReady))
	h.mustRun(t, "app", "status", "hello", "--wait")
	if h.cluster.rolloutCalls != 2 {
		t.Errorf("read the rollout %d times, want 2", h.cluster.rolloutCalls)
	}
	if len(h.cluster.added)+len(h.cluster.removed)+len(h.cluster.redeployed) != 0 {
		t.Errorf("waiting changed the cluster")
	}
}

func TestWaitRefusals(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		args []string
		want string
	}{
		"short commit":      {[]string{"hello", "--wait", "--commit", "42a96"}, "at least 7 hex digits"},
		"commit not hex":    {[]string{"hello", "--wait", "--commit", "main-branch"}, "is not a git commit"},
		"commit, no --wait": {[]string{"hello", "--commit", "42a964e"}, "--commit needs --wait"},
		"no app":            {[]string{"--wait"}, "--wait needs the name of an app"},
		"unknown app":       {[]string{"nope", "--wait"}, "app nope does not exist"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := waitHarness(t, rolledOut(podsReady))
			_, stderr, code := h.run(t, append([]string{"app", "status"}, tt.args...)...)
			if code != 1 || !strings.Contains(stderr, tt.want) {
				t.Errorf("exit code %d, stderr %q, want %q", code, stderr, tt.want)
			}
			if h.cluster.rolloutCalls != 0 && name != "unknown app" {
				t.Errorf("waited %d times before refusing", h.cluster.rolloutCalls)
			}
		})
	}
}
