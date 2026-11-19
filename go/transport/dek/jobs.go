package dek

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"hop.top/kit/go/transport/api"
)

// Job is one background job, as the contract's Job. Statuses in use:
// queued, running, succeeded, failed.
type Job struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Subject     string `json:"subject"`
	Status      string `json:"status"`
	Attempts    uint32 `json:"attempts"`
	MaxAttempts uint32 `json:"max_attempts"`
	Outcome     any    `json:"outcome,omitempty"`
	UpdatedAt   string `json:"updated_at"`
}

func (j Job) final() bool {
	switch j.Status {
	case "succeeded", "failed", "expired", "cancelled": //nolint:misspell // dek contract job status
		return true
	}
	return false
}

func newJobID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "job_" + hex.EncodeToString(b[:])
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

var errTooManyJobs = errors.New("too many jobs running")

// startJob runs a long-running command in the background, owned by
// owner. It returns once the job is stored; the command runs on the
// core's own context, so a request that ends does not cancel it.
func (c *Core) startJob(owner string, cp *Capability, creq api.CommandRequest) (Job, error) {
	select {
	case c.slots <- struct{}{}:
	default:
		return Job{}, errTooManyJobs
	}
	subject := ""
	if len(creq.Args) > 0 {
		subject = creq.Args[0]
	}
	j := Job{
		ID: newJobID(), Kind: cp.ID, Subject: subject, Status: "queued",
		Attempts: 0, MaxAttempts: 1, UpdatedAt: now(),
	}
	if err := c.jobs.Put(c.base, owner, j); err != nil {
		<-c.slots
		return Job{}, errors.New("job not stored")
	}
	go func(j Job) { // its own copy: the caller holds the returned one
		defer func() { <-c.slots }()
		j.Status, j.Attempts, j.UpdatedAt = "running", 1, now()
		_ = c.jobs.Put(c.base, owner, j)
		body, status, why := c.execute(c.base, creq)
		if status != 0 {
			j.Status, j.Outcome = "failed", map[string]any{"error": why}
		} else {
			j.Status, j.Outcome = "succeeded", map[string]any{"result": body}
		}
		j.UpdatedAt = now()
		_ = c.jobs.Put(c.base, owner, j)
	}(j)
	return j, nil
}

func (c *Core) listJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := c.jobs.Active(r.Context(), ownerOf(r))
	if err != nil {
		refuse(w, http.StatusInternalServerError, "job store failed")
		return
	}
	api.JSON(w, http.StatusOK, jobs)
}

func (c *Core) getJob(w http.ResponseWriter, r *http.Request) {
	j, ok, err := c.jobs.Get(r.Context(), ownerOf(r), r.PathValue("id"))
	if err != nil {
		refuse(w, http.StatusInternalServerError, "job store failed")
		return
	}
	if !ok {
		refuse(w, http.StatusNotFound, "no such job")
		return
	}
	api.JSON(w, http.StatusOK, j)
}
