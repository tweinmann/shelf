// Package progress reports what a long-running operation is doing, as events rather than as
// text. The command line renders them with Writer, which prints exactly the lines shelf has
// always printed; the admin UI keeps them as a job log and streams them to a browser, where the
// parts of an event — which object, which action, how long a wait took — become markup instead
// of a line to read.
package progress

import (
	"fmt"
	"io"
	"time"
)

// Kind is what happened. It is a string so that an event survives a trip through JSON.
type Kind string

const (
	// KindInfo is a line of text about the operation as a whole.
	KindInfo Kind = "info"
	// KindWarning is a block of text that is printed as it is, newlines and all.
	KindWarning Kind = "warning"
	// KindApplied reports what happened to one object in the cluster.
	KindApplied Kind = "applied"
	// KindSecret reports where the value of one secret came from.
	KindSecret Kind = "secret"
	// KindRecord reports what happened to one DNS record.
	KindRecord Kind = "record"
	// KindStep begins a wait; exactly one KindStepDone or KindStepFailed follows.
	KindStep Kind = "step"
	// KindStepDone ends a wait that succeeded.
	KindStepDone Kind = "step-done"
	// KindStepFailed ends a wait that did not.
	KindStepFailed Kind = "step-failed"
)

// Event is one thing that happened. Which fields carry meaning depends on the kind; the
// constructors below are the supported combinations.
type Event struct {
	Kind Kind
	// Subject is what the event is about: an object, a secret name, a host name, or what a
	// step waits for.
	Subject string
	// Action is what happened to the subject, such as created, unchanged or restored.
	Action string
	// Detail explains the action: the registry login, the tunnel a record points at, the
	// message a controller reports.
	Detail string
	// Message is the text of an info or a warning.
	Message string
	// Elapsed is how long a step took.
	Elapsed time.Duration
}

// Info is a line about the operation.
func Info(format string, a ...any) Event {
	return Event{Kind: KindInfo, Message: fmt.Sprintf(format, a...)}
}

// Warning is a block of text, already formatted with its line breaks.
func Warning(text string) Event { return Event{Kind: KindWarning, Message: text} }

// Applied reports an object and what happened to it. The detail is optional.
func Applied(subject, action, detail string) Event {
	return Event{Kind: KindApplied, Subject: subject, Action: action, Detail: detail}
}

// Secret reports the name of a secret and where its value came from. The value itself is never
// part of an event.
func Secret(name, source string) Event {
	return Event{Kind: KindSecret, Subject: name, Action: source}
}

// Record reports a DNS record: the host name, what it points at, and what happened to it. For a
// deleted record the target is empty.
func Record(host, target, action string) Event {
	return Event{Kind: KindRecord, Subject: host, Detail: target, Action: action}
}

// Step announces a wait for something to become ready.
func Step(subject string) Event { return Event{Kind: KindStep, Subject: subject} }

// StepDone ends a successful wait. The detail is optional.
func StepDone(subject string, elapsed time.Duration, detail string) Event {
	return Event{Kind: KindStepDone, Subject: subject, Elapsed: elapsed, Detail: detail}
}

// StepFailed ends a wait that did not succeed. The error itself is returned to the caller, not
// reported, so that it is handled exactly once.
func StepFailed(subject string) Event { return Event{Kind: KindStepFailed, Subject: subject} }

// Reporter receives the events of an operation.
type Reporter interface {
	Report(Event)
}

// ReporterFunc makes a function a Reporter.
type ReporterFunc func(Event)

func (f ReporterFunc) Report(e Event) { f(e) }

// Discard reports nothing.
var Discard Reporter = ReporterFunc(func(Event) {})

// OrDiscard returns r, or Discard when r is nil, so that an operation never has to check.
func OrDiscard(r Reporter) Reporter {
	if r == nil {
		return Discard
	}
	return r
}

// Writer renders events as the lines the command line prints. A step prints the beginning of
// its line and the following event completes it, which is why the two belong together.
func Writer(w io.Writer) Reporter {
	return ReporterFunc(func(e Event) { write(w, e) })
}

func write(w io.Writer, e Event) {
	switch e.Kind {
	case KindInfo:
		fmt.Fprintln(w, e.Message)
	case KindWarning:
		fmt.Fprint(w, e.Message)
	case KindApplied:
		if e.Detail != "" {
			fmt.Fprintf(w, "%s: %s, %s\n", e.Subject, e.Action, e.Detail)
			return
		}
		fmt.Fprintf(w, "%s: %s\n", e.Subject, e.Action)
	case KindSecret:
		fmt.Fprintf(w, "secret %s: %s\n", e.Subject, e.Action)
	case KindRecord:
		if e.Detail == "" {
			fmt.Fprintf(w, "DNS %s: %s\n", e.Subject, e.Action)
			return
		}
		fmt.Fprintf(w, "DNS %s points at %s: %s\n", e.Subject, e.Detail, e.Action)
	case KindStep:
		fmt.Fprintf(w, "  waiting for %s ... ", e.Subject)
	case KindStepDone:
		if e.Detail != "" {
			fmt.Fprintf(w, "done after %s: %s\n", e.Elapsed, e.Detail)
			return
		}
		fmt.Fprintf(w, "done after %s\n", e.Elapsed)
	case KindStepFailed:
		fmt.Fprintln(w, "failed")
	}
}
