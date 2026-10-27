package ops

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/tweinmann/shelf/internal/cluster"
)

// Outcome is how a wait for an app ended.
type Outcome string

const (
	// OutcomeReady means the app, at the awaited commit if one was named, is running.
	OutcomeReady Outcome = "Ready"
	// OutcomeFailed means a stage failed; it will not fix itself.
	OutcomeFailed Outcome = "Failed"
	// OutcomeTimedOut means the app was still on its way when the time ran out.
	OutcomeTimedOut Outcome = "TimedOut"
)

// DefaultWaitTimeout covers a CI build of a few minutes plus the minute the platform takes to
// notice the new artifact.
const DefaultWaitTimeout = 10 * time.Minute

// waitInterval is how often a wait reads the cluster.
const waitInterval = 2 * time.Second

// WaitOptions say what to wait for.
type WaitOptions struct {
	// Commit is the git commit, or a prefix of at least 7 hex digits, the app has to run. Empty
	// means whatever artifact the app has.
	Commit  string
	Timeout time.Duration
	// Interval overrides how often the cluster is read, for tests.
	Interval time.Duration
}

// WaitResult is the verdict of a wait.
type WaitResult struct {
	Outcome  Outcome
	App      string
	Commit   string
	Revision string
	// Stage is the first stage that is not Ready, nil when the outcome is Ready.
	Stage *cluster.Stage
}

var commitPrefix = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// CheckCommit checks the commit a wait is asked for.
func CheckCommit(commit string) error {
	if !commitPrefix.MatchString(commit) {
		return fmt.Errorf("%q is not a git commit: give at least 7 hex digits, as `git rev-parse HEAD` prints", commit)
	}
	return nil
}

// Wait reads the app's chain until it is Ready or a stage failed, or the timeout ends. It only
// reads: asking Flux to hurry would make a status query a change to the cluster.
func (o *Ops) Wait(ctx context.Context, app string, opts WaitOptions) (WaitResult, error) {
	if opts.Commit != "" {
		if err := CheckCommit(opts.Commit); err != nil {
			return WaitResult{}, err
		}
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = waitInterval
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultWaitTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var last WaitResult
	for {
		r, err := o.Cluster.AppRollout(ctx, o.config(), app)
		switch {
		case err == nil:
			var done bool
			if last, done = Verdict(app, r, opts.Commit); done {
				return last, nil
			}
		case ctx.Err() == nil:
			return WaitResult{}, err
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) && last.App != "" {
				last.Outcome = OutcomeTimedOut
				return last, nil
			}
			return WaitResult{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

// Verdict decides what one reading of an app's rollout means. It reports false while the app
// is still on its way, together with the stage that is holding it up.
//
// The stages are walked in order, as the diagnosis does, with one addition: a stage reporting
// on an older artifact, Ready or Failed, is still on its way. The deploy artifact has to be the
// awaited commit, the values have to be those of that artifact, and the release has to hold
// those values. Before the awaited commit has arrived, only a failing deploy artifact ends the
// wait: the later stages are still about the previous one.
func Verdict(app string, r cluster.Rollout, commit string) (WaitResult, bool) {
	result := WaitResult{App: app, Commit: r.Commit, Revision: r.Revision}
	stages := r.Diagnosis.Stages
	if len(stages) < 5 {
		return result, false
	}
	failed := func(s cluster.Stage) (WaitResult, bool) {
		result.Outcome, result.Stage = OutcomeFailed, &s
		return result, true
	}
	pending := func(s cluster.Stage) (WaitResult, bool) {
		result.Outcome, result.Stage = OutcomeTimedOut, &s
		return result, false
	}
	working := func(s cluster.Stage, message string) (WaitResult, bool) {
		s.Phase, s.Reason, s.Message, s.Hint = cluster.PhaseWorking, "", message, ""
		return pending(s)
	}

	source, values, chart, release, pods := stages[0], stages[1], stages[2], stages[3], stages[4]
	switch {
	case source.Phase == cluster.PhaseFailed:
		return failed(source)
	case source.Phase != cluster.PhaseReady:
		return pending(source)
	case commit != "" && r.Commit == "":
		return working(source, "waiting for commit "+short(commit)+"; the deploy artifact names no commit")
	case commit != "" && !strings.HasPrefix(r.Commit, commit):
		return working(source, "waiting for commit "+short(commit)+"; running "+short(r.Commit))
	}

	switch {
	case values.Phase == cluster.PhaseFailed && r.AttemptedRevision == r.Revision:
		return failed(values)
	case values.Phase == cluster.PhaseReady && r.AppliedRevision == r.Revision:
	case values.Phase == cluster.PhaseMissing || values.Phase == cluster.PhaseWorking:
		return pending(values)
	default:
		return working(values, "waiting for the values of "+r.Revision)
	}

	switch chart.Phase {
	case cluster.PhaseReady:
	case cluster.PhaseFailed:
		return failed(chart)
	default:
		return pending(chart)
	}

	// Which values a release holds only its digest says: after a failed upgrade, the failure
	// and the rollback both happen after newer values arrived, and still belong to the older.
	current := r.ValuesDigest != "" && r.ReleasedDigest == r.ValuesDigest && !r.Reconciling
	attempting := r.ValuesDigest != "" && r.AttemptedDigest == r.ValuesDigest
	switch {
	case current && release.Phase == cluster.PhaseReady:
	case current && release.Phase == cluster.PhaseFailed:
		return failed(release)
	case attempting && release.Phase != cluster.PhaseReady:
		// The release waits for its pods, so a pod that cannot start is known long before the
		// release gives up on it.
		if pods.Phase == cluster.PhaseFailed {
			return failed(pods)
		}
		return pending(release)
	default:
		return working(release, "waiting for the release to pick up the new values")
	}

	switch pods.Phase {
	case cluster.PhaseReady:
	case cluster.PhaseFailed:
		return failed(pods)
	default:
		return pending(pods)
	}
	result.Outcome = OutcomeReady
	return result, true
}

// short is how a commit is named in a message.
func short(commit string) string { return commit[:min(len(commit), 7)] }
