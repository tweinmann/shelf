package ops_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/rest"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/ops"
)

const (
	oldCommit = "9956993c371777310050f0c10d4f9b90aebcb969"
	newCommit = "42a964eb0b4bd7248305d5f22233d6dbabcd56f3"
)

// The digests of the values: those of the awaited artifact, of the one before, and of a broken
// one in between.
const (
	newValues    = "sha256:new-values"
	oldValues    = "sha256:old-values"
	brokenValues = "sha256:broken-values"
)

func stage(name string, phase cluster.Phase) cluster.Stage {
	return cluster.Stage{Name: name, Object: "Object " + name, Phase: phase, Reason: "R", Message: "m", Hint: "h"}
}

// settled is an app whose new artifact has rolled out completely.
func settled() cluster.Rollout {
	return cluster.Rollout{
		Diagnosis: cluster.Diagnosis{Name: "hello", Stages: []cluster.Stage{
			stage("the deploy artifact", cluster.PhaseReady), stage("the app values", cluster.PhaseReady),
			stage("the chart", cluster.PhaseReady), stage("the app", cluster.PhaseReady),
			stage("the pods", cluster.PhaseReady),
		}},
		Revision: "main@sha256:new", Commit: newCommit,
		AppliedRevision: "main@sha256:new", AttemptedRevision: "main@sha256:new",
		ValuesDigest: newValues, ReleasedDigest: newValues, AttemptedDigest: newValues,
	}
}

func with(r cluster.Rollout, change func(*cluster.Rollout)) cluster.Rollout {
	r.Diagnosis.Stages = append([]cluster.Stage(nil), r.Diagnosis.Stages...)
	change(&r)
	return r
}

func phase(i int, p cluster.Phase) func(*cluster.Rollout) {
	return func(r *cluster.Rollout) { r.Diagnosis.Stages[i].Phase = p }
}

func TestVerdict(t *testing.T) {
	t.Parallel()
	// previous is the app still running the artifact before the awaited one.
	previous := func(r *cluster.Rollout) {
		r.Commit, r.Revision, r.AppliedRevision, r.AttemptedRevision = oldCommit, "main@sha256:old", "main@sha256:old", "main@sha256:old"
	}
	tests := []struct {
		name     string
		rollout  cluster.Rollout
		commit   string
		done     bool
		outcome  ops.Outcome
		stage    string
		phase    cluster.Phase
		contains string
	}{
		{name: "already Ready", rollout: settled(), done: true, outcome: ops.OutcomeReady},
		{name: "Ready at the awaited commit", rollout: settled(), commit: "42a964e", done: true, outcome: ops.OutcomeReady},
		{name: "full commit", rollout: settled(), commit: newCommit, done: true, outcome: ops.OutcomeReady},
		{name: "pods starting", rollout: with(settled(), phase(4, cluster.PhaseWorking)),
			stage: "the pods", phase: cluster.PhaseWorking},
		{name: "release upgrading", rollout: with(settled(), func(r *cluster.Rollout) {
			r.ReleasedDigest, r.Reconciling = oldValues, true
			phase(3, cluster.PhaseWorking)(r)
		}), stage: "the app", phase: cluster.PhaseWorking, contains: "m"},
		{name: "previous commit still running", rollout: with(settled(), previous), commit: "42a964e",
			stage: "the deploy artifact", phase: cluster.PhaseWorking, contains: "waiting for commit 42a964e; running 9956993"},
		{name: "previous artifact failed while waiting for the new one",
			rollout: with(settled(), func(r *cluster.Rollout) { previous(r); phase(4, cluster.PhaseFailed)(r) }),
			commit:  "42a964e", stage: "the deploy artifact", phase: cluster.PhaseWorking},
		{name: "registry refuses the artifact", rollout: with(settled(), func(r *cluster.Rollout) {
			previous(r)
			phase(0, cluster.PhaseFailed)(r)
		}), commit: "42a964e", done: true, outcome: ops.OutcomeFailed, stage: "the deploy artifact", phase: cluster.PhaseFailed},
		{name: "artifact without a commit", rollout: with(settled(), func(r *cluster.Rollout) { r.Commit = "" }),
			commit: "42a964e", stage: "the deploy artifact", contains: "names no commit"},
		{name: "values of the previous artifact", rollout: with(settled(), func(r *cluster.Rollout) {
			r.AppliedRevision, r.AttemptedRevision = "main@sha256:old", "main@sha256:old"
		}), stage: "the app values", phase: cluster.PhaseWorking, contains: "waiting for the values of main@sha256:new"},
		{name: "values failed for the previous artifact", rollout: with(settled(), func(r *cluster.Rollout) {
			r.AppliedRevision, r.AttemptedRevision = "main@sha256:older", "main@sha256:old"
			phase(1, cluster.PhaseFailed)(r)
		}), stage: "the app values", phase: cluster.PhaseWorking},
		{name: "values rejected for this artifact", rollout: with(settled(), func(r *cluster.Rollout) {
			r.AppliedRevision = "main@sha256:old"
			phase(1, cluster.PhaseFailed)(r)
		}), done: true, outcome: ops.OutcomeFailed, stage: "the app values", phase: cluster.PhaseFailed},
		// The spike of 2026-10-27, step by step: a fix arrives while Helm still waits for the
		// broken upgrade before it, then rolls it back and retries.
		{name: "previous values still released", rollout: with(settled(), func(r *cluster.Rollout) {
			r.ReleasedDigest, r.AttemptedDigest = oldValues, oldValues
		}), stage: "the app", phase: cluster.PhaseWorking, contains: "pick up the new values"},
		{name: "broken upgrade before the fix still running", rollout: with(settled(), func(r *cluster.Rollout) {
			r.ReleasedDigest, r.AttemptedDigest, r.Reconciling = oldValues, brokenValues, true
			phase(3, cluster.PhaseWorking)(r)
			phase(4, cluster.PhaseFailed)(r)
		}), stage: "the app", phase: cluster.PhaseWorking, contains: "pick up the new values"},
		{name: "broken upgrade before the fix failed", rollout: with(settled(), func(r *cluster.Rollout) {
			r.ReleasedDigest, r.AttemptedDigest, r.Reconciling = brokenValues, brokenValues, true
			phase(3, cluster.PhaseFailed)(r)
		}), stage: "the app", phase: cluster.PhaseWorking},
		{name: "rollback after the fix arrived", rollout: with(settled(), func(r *cluster.Rollout) {
			r.ReleasedDigest, r.AttemptedDigest, r.Reconciling = oldValues, brokenValues, true
			phase(3, cluster.PhaseFailed)(r)
		}), stage: "the app", phase: cluster.PhaseWorking},
		{name: "fix released without a new revision", rollout: with(settled(), func(r *cluster.Rollout) {
			r.ReleasedDigest, r.AttemptedDigest = newValues, newValues
		}), done: true, outcome: ops.OutcomeReady},
		{name: "released, but helm-controller is reconciling again", rollout: with(settled(), func(r *cluster.Rollout) {
			r.Reconciling = true
		}), stage: "the app", phase: cluster.PhaseWorking},
		{name: "values cannot be read", rollout: with(settled(), func(r *cluster.Rollout) { r.ValuesDigest = "" }),
			stage: "the app", phase: cluster.PhaseWorking},
		{name: "upgrade failed", rollout: with(settled(), phase(3, cluster.PhaseFailed)),
			done: true, outcome: ops.OutcomeFailed, stage: "the app", phase: cluster.PhaseFailed},
		{name: "image cannot be pulled while the release waits", rollout: with(settled(), func(r *cluster.Rollout) {
			r.ReleasedDigest, r.Reconciling = oldValues, true
			phase(3, cluster.PhaseWorking)(r)
			phase(4, cluster.PhaseFailed)(r)
		}), done: true, outcome: ops.OutcomeFailed, stage: "the pods", phase: cluster.PhaseFailed},
		{name: "pods failing after the release", rollout: with(settled(), phase(4, cluster.PhaseFailed)),
			done: true, outcome: ops.OutcomeFailed, stage: "the pods"},
		{name: "not created yet", rollout: with(settled(), phase(0, cluster.PhaseMissing)),
			stage: "the deploy artifact", phase: cluster.PhaseMissing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, done := ops.Verdict("hello", tt.rollout, tt.commit)
			if done != tt.done {
				t.Fatalf("done %v, want %v: %+v", done, tt.done, got)
			}
			if tt.done && got.Outcome != tt.outcome {
				t.Errorf("outcome %s, want %s", got.Outcome, tt.outcome)
			}
			if tt.stage == "" {
				if got.Stage != nil {
					t.Errorf("stage %+v, want none", got.Stage)
				}
				return
			}
			if got.Stage == nil || got.Stage.Name != tt.stage {
				t.Fatalf("stage %+v, want %s", got.Stage, tt.stage)
			}
			if tt.phase != "" && got.Stage.Phase != tt.phase {
				t.Errorf("phase %s, want %s", got.Stage.Phase, tt.phase)
			}
			if !strings.Contains(got.Stage.Message, tt.contains) {
				t.Errorf("message %q lacks %q", got.Stage.Message, tt.contains)
			}
		})
	}
}

// rolloutCluster answers AppRollout from a script. Every other method of ops.Cluster is the nil
// interface's, so a wait that tried to change anything would panic: waiting is read-only.
type rolloutCluster struct {
	ops.Cluster
	mu       sync.Mutex
	rollouts []cluster.Rollout
	calls    int
}

func (c *rolloutCluster) AppRollout(context.Context, *rest.Config, string) (cluster.Rollout, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.rollouts[min(c.calls, len(c.rollouts)-1)]
	c.calls++
	return r, nil
}

func TestWait(t *testing.T) {
	t.Parallel()
	before := with(settled(), func(r *cluster.Rollout) {
		r.Commit, r.Revision = oldCommit, "main@sha256:old"
	})
	arrived := with(settled(), func(r *cluster.Rollout) { r.ReleasedDigest = oldValues })
	tests := []struct {
		name     string
		rollouts []cluster.Rollout
		commit   string
		timeout  time.Duration
		outcome  ops.Outcome
		calls    int
	}{
		{name: "until the commit rolled out", rollouts: []cluster.Rollout{before, before, arrived, settled()},
			commit: "42a964e", timeout: time.Minute, outcome: ops.OutcomeReady, calls: 4},
		{name: "timeout while the previous commit runs", rollouts: []cluster.Rollout{before},
			commit: "42a964e", timeout: 50 * time.Millisecond, outcome: ops.OutcomeTimedOut},
		{name: "failure ends the wait", rollouts: []cluster.Rollout{arrived, with(settled(), phase(4, cluster.PhaseFailed))},
			timeout: time.Minute, outcome: ops.OutcomeFailed, calls: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fake := &rolloutCluster{rollouts: tt.rollouts}
			o := &ops.Ops{Target: ops.Target{Config: &rest.Config{}}, Cluster: fake}
			got, err := o.Wait(context.Background(), "hello",
				ops.WaitOptions{Commit: tt.commit, Timeout: tt.timeout, Interval: time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			if got.Outcome != tt.outcome {
				t.Errorf("outcome %s, want %s: %+v", got.Outcome, tt.outcome, got.Stage)
			}
			if tt.calls != 0 && fake.calls != tt.calls {
				t.Errorf("read the cluster %d times, want %d", fake.calls, tt.calls)
			}
			if tt.outcome == ops.OutcomeTimedOut && (got.Stage == nil || got.Stage.Name != "the deploy artifact") {
				t.Errorf("a timeout names the stage it waited at: %+v", got.Stage)
			}
		})
	}
}

func TestWaitRefusesABadCommit(t *testing.T) {
	t.Parallel()
	o := &ops.Ops{Cluster: &rolloutCluster{}}
	for _, commit := range []string{"42a96", "not-hex", "42A964EB"} {
		if _, err := o.Wait(context.Background(), "hello", ops.WaitOptions{Commit: commit}); err == nil ||
			!strings.Contains(err.Error(), "at least 7 hex digits") {
			t.Errorf("%q: %v", commit, err)
		}
	}
}
