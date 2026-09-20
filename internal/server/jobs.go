package server

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/tweinmann/shelf/internal/progress"
)

// ErrBusy means another change is already running. Only one at a time: two operations would
// fight over the same objects in the cluster, and the person watching would not be able to tell
// whose lines those were.
var ErrBusy = errors.New("another change is running")

// jobTimeout bounds one change. It is the same five minutes the command line waits by default.
const jobTimeout = 5 * time.Minute

// keptJobs is how many finished jobs stay readable. They live in memory only: the cluster is
// the source of truth, and the pages read it again after a restart.
const keptJobs = 20

// Job is one change the admin UI started, and everything it has reported so far.
type Job struct {
	ID    string
	Title string
	// App is the app the change is about, for linking back to it.
	App     string
	Started time.Time
	Ended   time.Time
	// Err is why the change failed, empty while it runs and when it succeeded.
	Err    string
	Events []progress.Event
}

// Running reports whether the change is still going on.
func (j *Job) Running() bool { return j.Ended.IsZero() }

// Log is everything reported so far, as the command line would have printed it.
func (j *Job) Log() string {
	var b strings.Builder
	for _, e := range j.Events {
		b.WriteString(progress.Text(e))
	}
	return b.String()
}

// jobs keeps the changes. Everything here is touched by a request goroutine and by the
// goroutine that runs the change, so everything goes through the mutex.
type jobs struct {
	now func() time.Time

	mu      sync.Mutex
	byID    map[string]*Job
	order   []string
	running string
	subs    map[string][]chan progress.Event
	// newID makes an identifier; a field so that tests get stable job links.
	newID func() string
}

func newJobs(now func() time.Time, newID func() string) *jobs {
	return &jobs{
		now:   now,
		byID:  map[string]*Job{},
		subs:  map[string][]chan progress.Event{},
		newID: newID,
	}
}

// start runs a change in the background and returns the job that watches it. When another
// change is already running it returns that one and ErrBusy, so the caller can point at it.
func (j *jobs) start(title, app string, run func(context.Context, progress.Reporter) error) (*Job, error) {
	j.mu.Lock()
	if j.running != "" {
		running := j.snapshot(j.byID[j.running])
		j.mu.Unlock()
		return running, ErrBusy
	}
	job := &Job{ID: j.newID(), Title: title, App: app, Started: j.now()}
	j.byID[job.ID] = job
	j.order = append(j.order, job.ID)
	j.running = job.ID
	j.evict()
	snapshot := j.snapshot(job)
	j.mu.Unlock()

	go func() {
		// The request that started this is long gone, so the change gets its own deadline
		// rather than the request's. A restart of the server ends it; the cluster keeps what
		// was applied, and running the same change again is how it is finished.
		ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
		defer cancel()
		err := run(ctx, progress.ReporterFunc(func(e progress.Event) { j.append(job.ID, e) }))
		j.finish(job.ID, err)
	}()
	return snapshot, nil
}

// append records an event and hands it to everyone watching.
func (j *jobs) append(id string, e progress.Event) {
	j.mu.Lock()
	defer j.mu.Unlock()
	job, ok := j.byID[id]
	if !ok {
		return
	}
	job.Events = append(job.Events, e)
	for _, ch := range j.subs[id] {
		select {
		case ch <- e:
		default:
			// A watcher that cannot keep up is dropped rather than slowing the change down;
			// reloading the page reads the whole log again.
		}
	}
}

func (j *jobs) finish(id string, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	job, ok := j.byID[id]
	if !ok {
		return
	}
	job.Ended = j.now()
	if err != nil {
		job.Err = err.Error()
	}
	if j.running == id {
		j.running = ""
	}
	for _, ch := range j.subs[id] {
		close(ch)
	}
	delete(j.subs, id)
}

// get returns a copy of a job, so that the caller can read it without holding the lock.
func (j *jobs) get(id string) (*Job, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	job, ok := j.byID[id]
	if !ok {
		return nil, false
	}
	return j.snapshot(job), true
}

// running returns the change that is going on, or nil.
func (j *jobs) runningJob() *Job {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.running == "" {
		return nil
	}
	return j.snapshot(j.byID[j.running])
}

// watch returns the events from index `from` on: the ones that already happened, and then the
// ones that follow. The page renders what it has and subscribes from there, so nothing is
// missed between rendering and subscribing. The channel is closed when the change ends.
func (j *jobs) watch(id string, from int) (<-chan progress.Event, func(), bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	job, ok := j.byID[id]
	if !ok {
		return nil, nil, false
	}
	missed := job.Events[min(from, len(job.Events)):]
	ch := make(chan progress.Event, len(missed)+64)
	for _, e := range missed {
		ch <- e
	}
	if !job.Ended.IsZero() {
		close(ch)
		return ch, func() {}, true
	}
	j.subs[id] = append(j.subs[id], ch)
	return ch, func() { j.unwatch(id, ch) }, true
}

func (j *jobs) unwatch(id string, ch chan progress.Event) {
	j.mu.Lock()
	defer j.mu.Unlock()
	watchers := j.subs[id]
	for i, c := range watchers {
		if c == ch {
			j.subs[id] = append(watchers[:i], watchers[i+1:]...)
			close(c)
			return
		}
	}
}

// snapshot copies a job; it is called with the mutex held.
func (j *jobs) snapshot(job *Job) *Job {
	if job == nil {
		return nil
	}
	copied := *job
	copied.Events = append([]progress.Event(nil), job.Events...)
	return &copied
}

// evict forgets the oldest finished jobs; it is called with the mutex held.
func (j *jobs) evict() {
	for len(j.order) > keptJobs {
		oldest := j.order[0]
		if oldest == j.running {
			return
		}
		j.order = j.order[1:]
		delete(j.byID, oldest)
		delete(j.subs, oldest)
	}
}
