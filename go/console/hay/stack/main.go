package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"hop.top/kit/go/console/hay"
)

func main() {
	explain := flag.Bool("explain", false, "show score breakdown per scorer")
	flag.BoolVar(explain, "e", false, "show score breakdown per scorer")

	scorer := flag.String("scorer", "combined", "scorer: combined, subsequence, substring, levenshtein")
	flag.StringVar(scorer, "s", "combined", "scorer")

	margin := flag.Int("margin", 0, "tie margin")
	flag.IntVar(margin, "m", 0, "tie margin")

	maxN := flag.Int("max", 20, "max results")
	flag.IntVar(maxN, "n", 20, "max results")

	policy := flag.String("policy", "list-fail", "ambiguity policy: list-fail, list-ok, pick-fail, pick-ok")
	flag.StringVar(policy, "p", "list-fail", "ambiguity policy")

	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: hay/stack <query> [flags]")
		os.Exit(2)
	}
	query := flag.Arg(0)

	var corpus []string
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			corpus = append(corpus, line)
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "reading stdin: %v\n", err)
		os.Exit(1)
	}
	if len(corpus) == 0 {
		fmt.Fprintln(os.Stderr, "no input")
		os.Exit(1)
	}

	scoreFn := pickScorer(*scorer)
	if scoreFn == nil {
		fmt.Fprintf(os.Stderr, "unknown scorer: %s\n", *scorer)
		os.Exit(2)
	}

	pol := parsePolicy(*policy)

	opts := hay.Options[string]{
		Score:         hay.StringScore(func(s string) string { return s }, scoreFn),
		Policy:        pol,
		TieMargin:     *margin,
		MaxCandidates: *maxN,
	}

	res, err := hay.Resolve(query, corpus, opts)
	if err != nil {
		if ambig, ok := err.(*hay.ErrAmbiguous[string]); ok {
			printResults(ambig.Candidates, query, *explain)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	printResults(res.Candidates, query, *explain)
}

func pickScorer(name string) func(string, string) int {
	switch name {
	case "combined":
		return hay.Combined
	case "subsequence":
		return hay.Subsequence
	case "substring":
		return hay.Substring
	case "levenshtein":
		return hay.Levenshtein
	default:
		return nil
	}
}

func parsePolicy(s string) hay.Policy {
	switch s {
	case "list-ok":
		return hay.Policy{Action: hay.ActionList, Fail: false}
	case "pick-fail":
		return hay.Policy{Action: hay.ActionPick, Fail: true}
	case "pick-ok":
		return hay.Policy{Action: hay.ActionPick, Fail: false}
	default: // list-fail
		return hay.Policy{Action: hay.ActionList, Fail: true}
	}
}

func printResults(candidates []hay.Scored[string], query string, explain bool) {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)

	if explain {
		fmt.Fprintln(w, "SCORE\tSUB-SEQ\tSUB-STR\tLEV\tPATH")
		for _, c := range candidates {
			subseq := hay.Subsequence(query, c.Item)
			substr := hay.Substring(query, c.Item)
			lev := hay.Levenshtein(query, c.Item)
			fmt.Fprintf(w, "%d\t%d\t%d\t%d\t%s\n", c.Score, subseq, substr, lev, c.Item)
		}
	} else {
		fmt.Fprintln(w, "SCORE\tPATH")
		for _, c := range candidates {
			fmt.Fprintf(w, "%d\t%s\n", c.Score, c.Item)
		}
	}

	w.Flush()
}
