// kit-bus-emit entrypoint. Reads inputs from env (set by the workflow
// from `${{ github.event.* }}`), builds a payload, POSTs to ingress.
//
// Exit semantics (spec §3):
//   - Default: fail-open. Non-2xx → log, exit 0. CI does not break.
//   - KIT_BUS_STRICT="true" → fail-closed. Non-2xx → exit 1.
//
// Logs go to stderr; stdout is reserved for the JSON payload so the
// workflow log captures it for debugging without ANSI noise.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		// run already chose between stderr-and-exit-0 (fail-open)
		// and the error path; only true binary-misuse errors come
		// through here.
		fmt.Fprintf(os.Stderr, "kit-bus-emit: %v\n", err)
		os.Exit(2)
	}
}

// run is the testable entrypoint. Returns non-nil only for usage
// errors (unknown --kind, etc); delivery failures are handled inside.
func run(args []string) error {
	fs := flag.NewFlagSet("kit-bus-emit", flag.ContinueOnError)
	var kind string
	fs.StringVar(&kind, "kind", "", "event kind: run.completed|comment.created|pull.merged|pull.closed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if kind == "" {
		return fmt.Errorf("--kind is required")
	}

	in := readInputs(kind)
	topic, body, err := BuildPayload(in)
	if err != nil {
		return err
	}

	// Always print the payload to stdout so the workflow log captures
	// it (handy for debugging). The bus ingress also receives it via
	// the POST.
	fmt.Println(string(body))

	ingress := strings.TrimRight(os.Getenv("KIT_BUS_INGRESS_URL"), "/")
	if ingress == "" {
		// The workflow `if:` guard should have prevented this job
		// from running, but defense in depth: log + exit 0 so we
		// don't break CI.
		fmt.Fprintln(os.Stderr, "kit-bus-emit: KIT_BUS_INGRESS_URL is empty; skipping emit")
		return nil
	}

	strict := strings.EqualFold(strings.TrimSpace(os.Getenv("KIT_BUS_STRICT")), "true")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	status, _, postErr := Post(ctx, PostOpts{
		IngressURL: ingress,
		SigningKey: os.Getenv("KIT_BUS_SIGNING_KEY"),
		Token:      os.Getenv("KIT_BUS_TOKEN"),
		Strict:     strict,
		Topic:      string(topic),
	}, body)

	if postErr != nil {
		if strict {
			// Fail-closed: surface as a real error.
			return fmt.Errorf("strict mode: %w", postErr)
		}
		fmt.Fprintf(os.Stderr, "kit-bus-emit: %v (status=%d); exiting 0 (fail-open)\n",
			postErr, status)
		return nil
	}
	fmt.Fprintf(os.Stderr, "kit-bus-emit: ingress accepted (status=%d)\n", status)
	return nil
}

// readInputs pulls the per-event env vars into the Inputs struct. The
// workflow sets only the vars relevant to its kind; the rest are empty
// strings (so payload.go sees zero values for them, which it tolerates).
func readInputs(kind string) Inputs {
	return Inputs{
		Kind:  kind,
		Repo:  os.Getenv("KIT_BUS_REPO"),
		Actor: os.Getenv("KIT_BUS_ACTOR"),

		PRNumber:  os.Getenv("KIT_BUS_PR_NUMBER"),
		PRURL:     os.Getenv("KIT_BUS_PR_URL"),
		PRBranch:  os.Getenv("KIT_BUS_PR_BRANCH"),
		PRHeadSHA: os.Getenv("KIT_BUS_PR_HEAD_SHA"),
		PRBaseSHA: os.Getenv("KIT_BUS_PR_BASE_SHA"),

		RunID:         os.Getenv("KIT_BUS_RUN_ID"),
		RunName:       os.Getenv("KIT_BUS_RUN_NAME"),
		RunConclusion: os.Getenv("KIT_BUS_RUN_CONCLUSION"),
		RunURL:        os.Getenv("KIT_BUS_RUN_URL"),
		RunLogsURL:    os.Getenv("KIT_BUS_RUN_LOGS_URL"),

		CommentID:     os.Getenv("KIT_BUS_COMMENT_ID"),
		CommentAuthor: os.Getenv("KIT_BUS_COMMENT_AUTHOR"),
		CommentURL:    os.Getenv("KIT_BUS_COMMENT_URL"),
		CommentBody:   os.Getenv("KIT_BUS_COMMENT_BODY"),

		MergeCommitSHA: os.Getenv("KIT_BUS_MERGE_COMMIT_SHA"),
		MergedAt:       os.Getenv("KIT_BUS_MERGED_AT"),

		ClosedAt: os.Getenv("KIT_BUS_CLOSED_AT"),
		Reason:   os.Getenv("KIT_BUS_REASON"),
	}
}
