package queue

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestEnqueueAssignsIDAndState(t *testing.T) {
	q := New()
	id := q.Enqueue(Job{Type: JobVerify, Slug: "demo"})
	if id == "" {
		t.Fatal("Enqueue returned an empty id")
	}
	job, ok := q.Get(id)
	if !ok {
		t.Fatalf("Get(%q) = not found, want the enqueued job", id)
	}
	if job.State != StateQueued {
		t.Errorf("State = %q, want queued", job.State)
	}
	if job.Type != JobVerify || job.Slug != "demo" {
		t.Errorf("job = %+v, want a verify job for demo", job)
	}
	if job.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set on enqueue")
	}
}

func TestEnqueueIDsAreUnique(t *testing.T) {
	q := New()
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := q.Enqueue(Job{Type: JobAsk, Slug: "demo"})
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestClaimReturnsQueuedJobFIFO(t *testing.T) {
	q := New()
	id1 := q.Enqueue(Job{Type: JobVerify, Slug: "a"})
	id2 := q.Enqueue(Job{Type: JobVerify, Slug: "b"})

	ctx := context.Background()
	got1, ok := q.Claim(ctx)
	if !ok || got1.ID != id1 {
		t.Fatalf("first Claim = %+v ok=%v, want id %q", got1, ok, id1)
	}
	if got1.State != StateClaimed {
		t.Errorf("claimed job State = %q, want claimed", got1.State)
	}
	got2, ok := q.Claim(ctx)
	if !ok || got2.ID != id2 {
		t.Fatalf("second Claim = %+v ok=%v, want id %q", got2, ok, id2)
	}
}

func TestClaimBlocksUntilEnqueue(t *testing.T) {
	q := New()
	done := make(chan Job, 1)
	go func() {
		job, ok := q.Claim(context.Background())
		if ok {
			done <- job
		}
	}()

	// Give the claimer a moment to block on the empty queue.
	select {
	case <-done:
		t.Fatal("Claim returned before any job was enqueued")
	case <-time.After(20 * time.Millisecond):
	}

	id := q.Enqueue(Job{Type: JobExtend, Slug: "later"})
	select {
	case job := <-done:
		if job.ID != id {
			t.Errorf("Claim returned %q, want the just-enqueued %q", job.ID, id)
		}
	case <-time.After(time.Second):
		t.Fatal("Claim did not wake up after Enqueue")
	}
}

func TestClaimRespectsContextTimeout(t *testing.T) {
	q := New()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, ok := q.Claim(ctx)
	if ok {
		t.Fatal("Claim should return ok=false when the context expires with no job")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Claim blocked %v after ctx timeout, want a prompt return", elapsed)
	}
}

func TestDoneMarksJobDone(t *testing.T) {
	q := New()
	id := q.Enqueue(Job{Type: JobVerify, Slug: "demo"})
	if _, ok := q.Claim(context.Background()); !ok {
		t.Fatal("Claim failed")
	}
	q.Done(id)
	job, ok := q.Get(id)
	if !ok {
		t.Fatal("Get after Done should still find the job")
	}
	if job.State != StateDone {
		t.Errorf("State = %q, want done", job.State)
	}
}

func TestSetAnswer(t *testing.T) {
	q := New()
	id := q.Enqueue(Job{Type: JobAsk, Slug: "demo", Part: "part-01.md", Question: "why?"})
	q.SetAnswer(id, "because.")
	job, ok := q.Get(id)
	if !ok {
		t.Fatal("Get failed")
	}
	if job.Answer != "because." {
		t.Errorf("Answer = %q, want %q", job.Answer, "because.")
	}
}

func TestReclaimRequeuesStaleClaim(t *testing.T) {
	q := New()
	now := time.Unix(1000, 0)
	q.now = func() time.Time { return now }
	q.ReclaimAfter = time.Minute

	id := q.Enqueue(Job{Type: JobVerify, Slug: "demo"})
	if _, ok := q.Claim(context.Background()); !ok {
		t.Fatal("first Claim failed")
	}

	// Advance past the reclaim window: the stale claim should be re-queued and
	// handed out again by the next Claim.
	now = now.Add(2 * time.Minute)
	got, ok := q.Claim(context.Background())
	if !ok {
		t.Fatal("expected the stale claim to be reclaimable")
	}
	if got.ID != id {
		t.Errorf("reclaimed id = %q, want %q", got.ID, id)
	}
}

func TestReclaimLeavesFreshClaimAlone(t *testing.T) {
	q := New()
	now := time.Unix(1000, 0)
	q.now = func() time.Time { return now }
	q.ReclaimAfter = time.Minute

	q.Enqueue(Job{Type: JobVerify, Slug: "demo"})
	if _, ok := q.Claim(context.Background()); !ok {
		t.Fatal("first Claim failed")
	}

	// Within the reclaim window, a second claim should find nothing.
	now = now.Add(30 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, ok := q.Claim(ctx); ok {
		t.Error("a fresh claim should not be reclaimed")
	}
}

func TestWorkerPresence(t *testing.T) {
	q := New()
	now := time.Unix(1000, 0)
	q.now = func() time.Time { return now }
	q.PresenceWindow = time.Minute

	if q.WorkerConnected() {
		t.Error("no worker has been seen yet; WorkerConnected should be false")
	}
	q.MarkWorkerSeen()
	if !q.WorkerConnected() {
		t.Error("WorkerConnected should be true right after MarkWorkerSeen")
	}
	now = now.Add(2 * time.Minute)
	if q.WorkerConnected() {
		t.Error("WorkerConnected should be false once the presence window lapses")
	}
}

func TestConcurrentEnqueueClaim(t *testing.T) {
	q := New()
	const n = 50
	var wg sync.WaitGroup
	claimed := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job, ok := q.Claim(context.Background())
			if ok {
				claimed <- job.ID
			}
		}()
	}
	for i := 0; i < n; i++ {
		q.Enqueue(Job{Type: JobVerify, Slug: "demo"})
	}
	wg.Wait()
	close(claimed)
	seen := map[string]bool{}
	for id := range claimed {
		if seen[id] {
			t.Fatalf("job %q was claimed by two workers", id)
		}
		seen[id] = true
	}
	if len(seen) != n {
		t.Errorf("claimed %d jobs, want %d", len(seen), n)
	}
}
