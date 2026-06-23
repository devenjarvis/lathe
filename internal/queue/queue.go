// Package queue is the in-memory job queue that bridges the lathe web UI and an
// interactive coding-agent session. The browser enqueues a job (ask, verify, or
// extend) and an agent running the /lathe-work loop long-polls Claim, does the
// model work in its interactive session, and reports back. The queue is the only
// piece of shared state between the two processes; jobs are ephemeral (server
// lifetime) because verify/extend persist their real state to disk via the CLI
// and ask is conversation-only.
//
// The strict boundary is preserved: the binary still never drives a model. This
// queue only removes the manual copy-paste hop between the browser and the agent.
package queue

import (
	"context"
	"strconv"
	"sync"
	"time"
)

// JobType is the kind of work the browser asked for. Each maps to one of the
// existing /lathe-* protocols the worker applies.
type JobType string

const (
	JobAsk    JobType = "ask"
	JobVerify JobType = "verify"
	JobExtend JobType = "extend"
)

// State tracks a job through its short life: queued (waiting for a worker),
// claimed (a worker is doing the work), done (finished — for ask, the answer is
// now readable). Done jobs are kept in the map so the browser can still read an
// ask answer after completion.
type State string

const (
	StateQueued  State = "queued"
	StateClaimed State = "claimed"
	StateDone    State = "done"
)

// Job is a unit of work enqueued by the browser and claimed by a worker. The
// fields populated depend on Type: verify needs only Slug; extend adds optional
// Guidance; ask adds Part, Question, and (on completion) Answer.
type Job struct {
	ID        string    `json:"id"`
	Type      JobType   `json:"type"`
	Slug      string    `json:"slug"`
	Part      string    `json:"part,omitempty"`
	Question  string    `json:"question,omitempty"`
	Guidance  string    `json:"guidance,omitempty"`
	State     State     `json:"state"`
	Answer    string    `json:"answer,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ClaimedAt time.Time `json:"claimed_at,omitempty"`
}

// Default tunables. ReclaimAfter re-queues a claimed job whose worker died
// without reporting; PresenceWindow is how long after the last /-/work poll a
// worker still counts as connected.
const (
	DefaultReclaimAfter   = 10 * time.Minute
	DefaultPresenceWindow = 60 * time.Second
)

// Queue is a concurrency-safe in-memory job queue with long-poll claiming. One
// is created per server; it has no persistence.
type Queue struct {
	mu      sync.Mutex
	cond    *sync.Cond
	jobs    map[string]*Job
	order   []string // ids of queued jobs, FIFO
	counter int64

	// ReclaimAfter and PresenceWindow are exported so tests (and callers) can
	// tune them; they default to the Default* constants in New.
	ReclaimAfter   time.Duration
	PresenceWindow time.Duration

	lastWorkerSeen time.Time

	// now is the clock, injectable for deterministic tests.
	now func() time.Time
}

func New() *Queue {
	q := &Queue{
		jobs:           make(map[string]*Job),
		ReclaimAfter:   DefaultReclaimAfter,
		PresenceWindow: DefaultPresenceWindow,
		now:            time.Now,
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Enqueue adds job to the queue, assigning it a fresh id and the queued state,
// and wakes any worker blocked in Claim. It returns the new id.
func (q *Queue) Enqueue(job Job) string {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.counter++
	job.ID = strconv.FormatInt(q.counter, 10)
	job.State = StateQueued
	job.CreatedAt = q.now()
	job.ClaimedAt = time.Time{}
	j := job
	q.jobs[j.ID] = &j
	q.order = append(q.order, j.ID)
	q.cond.Broadcast()
	return j.ID
}

// Claim blocks until a queued job is available (or ctx is done), marks it
// claimed, and returns a copy. ok is false when ctx ends first. Before each
// wait it re-queues any claim that has gone stale past ReclaimAfter, so a dead
// worker can never strand a job.
func (q *Queue) Claim(ctx context.Context) (Job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	// Wake the cond when ctx is cancelled so a blocked Claim returns promptly.
	// Acquiring the lock before broadcasting closes the lost-wakeup window
	// between our ctx.Err() check and cond.Wait().
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			q.mu.Lock()
			q.cond.Broadcast()
			q.mu.Unlock()
		case <-stop:
		}
	}()

	for {
		q.reclaimExpiredLocked()
		if id, ok := q.popQueuedLocked(); ok {
			job := q.jobs[id]
			job.State = StateClaimed
			job.ClaimedAt = q.now()
			return *job, true
		}
		if ctx.Err() != nil {
			return Job{}, false
		}
		q.cond.Wait()
	}
}

// popQueuedLocked removes and returns the next still-queued job id. It skips ids
// whose job is no longer queued (e.g. reclaimed then handled). Caller holds mu.
func (q *Queue) popQueuedLocked() (string, bool) {
	for len(q.order) > 0 {
		id := q.order[0]
		q.order = q.order[1:]
		if job, ok := q.jobs[id]; ok && job.State == StateQueued {
			return id, true
		}
	}
	return "", false
}

// reclaimExpiredLocked re-queues any claimed job whose ClaimedAt is older than
// ReclaimAfter. Caller holds mu.
func (q *Queue) reclaimExpiredLocked() {
	if q.ReclaimAfter <= 0 {
		return
	}
	cutoff := q.now().Add(-q.ReclaimAfter)
	for id, job := range q.jobs {
		if job.State == StateClaimed && job.ClaimedAt.Before(cutoff) {
			job.State = StateQueued
			job.ClaimedAt = time.Time{}
			q.order = append(q.order, id)
		}
	}
}

// Done marks a job finished. The job stays in the map so the browser can still
// read an ask answer after completion. Unknown ids are ignored.
func (q *Queue) Done(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if job, ok := q.jobs[id]; ok {
		job.State = StateDone
	}
}

// SetAnswer records the worker's answer for an ask job. Unknown ids are ignored.
func (q *Queue) SetAnswer(id, text string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if job, ok := q.jobs[id]; ok {
		job.Answer = text
	}
}

// Get returns a copy of the job with id, and whether it exists.
func (q *Queue) Get(id string) (Job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if job, ok := q.jobs[id]; ok {
		return *job, true
	}
	return Job{}, false
}

// MarkWorkerSeen records that a worker just polled /-/work, refreshing presence.
func (q *Queue) MarkWorkerSeen() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.lastWorkerSeen = q.now()
}

// WorkerConnected reports whether a worker has polled within PresenceWindow. It
// drives the enqueue-vs-handoff branch in the web handlers and the UI indicator.
func (q *Queue) WorkerConnected() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.lastWorkerSeen.IsZero() {
		return false
	}
	return q.now().Sub(q.lastWorkerSeen) < q.PresenceWindow
}
