package cmdsurface

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// slowEchoTree is a tree whose one command writes its --tag on several
// lines with a pause between them, so a second invocation on the same
// tree has every chance to overlap it. started, when non-nil, is
// signaled once the command is running; hold, when non-nil, keeps it
// running until closed.
func slowEchoTree(started chan<- struct{}, hold <-chan struct{}) *cobra.Command {
	root := &cobra.Command{Use: "root"}
	echo := &cobra.Command{
		Use: "echo",
		RunE: func(cmd *cobra.Command, _ []string) error {
			tag, _ := cmd.Flags().GetString("tag")
			if started != nil {
				started <- struct{}{}
			}
			if hold != nil {
				<-hold
			}
			for i := 0; i < 5; i++ {
				fmt.Fprintln(cmd.OutOrStdout(), tag)
				time.Sleep(time.Millisecond)
			}
			return nil
		},
	}
	echo.Flags().String("tag", "", "what to echo")
	root.AddCommand(echo)
	return root
}

// Runners built over one root are what the services of one instance
// hold: each bridge builds its own. They drive one tree, so they must
// serialize against each other, not only each against itself; if each
// has a lock of its own, one invocation's writers capture another's
// lines. Run under -race this also catches the unsynchronized access.
func TestInProcessRunners_OverOneRoot_NeverInterleave(t *testing.T) {
	root := slowEchoTree(nil, nil)
	runners := make([]Runner, 4)
	for i := range runners {
		runners[i] = InProcessRunner(root)
	}

	var wg sync.WaitGroup
	for i, r := range runners {
		for j := 0; j < 8; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				tag := fmt.Sprintf("svc%d-call%d", i, j)
				res, err := r.Run(context.Background(), Invocation{
					Path: []string{"echo"}, Flags: map[string]any{"tag": tag},
				})
				if err != nil {
					t.Errorf("%s: %v", tag, err)
					return
				}
				if want := strings.Repeat(tag+"\n", 5); res.Stdout != want {
					t.Errorf("%s read another call's output:\n got %q\nwant %q", tag, res.Stdout, want)
				}
			}()
		}
	}
	wg.Wait()
}

// The same guarantee for a streaming invocation, which takes the tree
// the same way and holds it for as long as it runs.
func TestInProcessRunners_OverOneRoot_StreamNeverInterleavesRun(t *testing.T) {
	root := slowEchoTree(nil, nil)
	a, b := InProcessRunner(root), InProcessRunner(root)

	var wg sync.WaitGroup
	for j := 0; j < 6; j++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			tag := fmt.Sprintf("stream-%d", j)
			events := make(chan Event, 64)
			done := make(chan error, 1)
			go func() {
				done <- a.Stream(context.Background(), Invocation{
					Path: []string{"echo"}, Flags: map[string]any{"tag": tag}}, events)
			}()
			var got []string
			for ev := range events {
				if ev.Kind == "stdout" {
					got = append(got, fmt.Sprint(ev.Data))
				}
			}
			if err := <-done; err != nil {
				t.Errorf("%s: %v", tag, err)
			}
			for _, line := range got {
				if strings.TrimSuffix(line, "\n") != tag {
					t.Errorf("%s streamed another call's line %q", tag, line)
				}
			}
		}()
		go func() {
			defer wg.Done()
			tag := fmt.Sprintf("run-%d", j)
			res, err := b.Run(context.Background(), Invocation{
				Path: []string{"echo"}, Flags: map[string]any{"tag": tag}})
			if err != nil {
				t.Errorf("%s: %v", tag, err)
				return
			}
			if want := strings.Repeat(tag+"\n", 5); res.Stdout != want {
				t.Errorf("%s read another call's output:\n got %q\nwant %q", tag, res.Stdout, want)
			}
		}()
	}
	wg.Wait()
}

// The lock belongs to a tree, not to the process: an invocation
// holding one tree must not hold up another tree's.
func TestInProcessRunners_DifferentRoots_RunInParallel(t *testing.T) {
	started := make(chan struct{}, 1)
	hold := make(chan struct{})
	held := InProcessRunner(slowEchoTree(started, hold))
	other := InProcessRunner(slowEchoTree(nil, nil))

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		_, _ = held.Run(context.Background(), Invocation{Path: []string{"echo"}})
	}()
	<-started

	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		_, _ = other.Run(context.Background(), Invocation{Path: []string{"echo"}})
	}()
	select {
	case <-secondDone:
	case <-time.After(5 * time.Second):
		t.Error("an invocation on another tree waited on the first tree's lock")
	}
	close(hold)
	<-firstDone
}

// A runner with no tree and no factory reports it rather than
// dereferencing a lock it never took.
func TestInProcessRunner_NoRoot_Errors(t *testing.T) {
	_, err := InProcessRunner(nil).Run(context.Background(), Invocation{Path: []string{"echo"}})
	if err == nil || !strings.Contains(err.Error(), "nil cobra root") {
		t.Fatalf("err = %v, want nil cobra root", err)
	}
}
