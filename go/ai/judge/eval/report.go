package eval

import (
	"encoding/json"
	"fmt"
	"strings"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/judge/calibrate"
)

// Report is the result of Compare.
type Report struct {
	// N is the number of cases replayed.
	N int `json:"n"`
	// Questions in order of first appearance.
	Questions []QuestionReport `json:"questions"`
	// Samples are the candidate's probabilities against reference labels,
	// one per answer that carries Probs, in case order: feed calibrate.Fit
	// to calibrate the candidate from the same run. Fit rejects a sample
	// whose true label has probability 0; drop those first if you see it.
	Samples []calibrate.Sample `json:"samples,omitempty"`
}

// QuestionReport is one question's statistics; "cand" is the judge under
// test, "ref" the reference.
type QuestionReport struct {
	Name string     `json:"name"`
	Kind judge.Kind `json:"kind"`
	// N cases scored; Matches have identical Value (abstain matches only
	// abstain). Agreement = Matches / N.
	N         int     `json:"n"`
	Matches   int     `json:"matches"`
	Agreement float64 `json:"agreement"`
	// RefAbstain / CandAbstain count abstentions per side. NAnswered is
	// the cases neither side abstained on; AgreementAnswered the match
	// rate among them.
	RefAbstain        int     `json:"ref_abstain"`
	CandAbstain       int     `json:"cand_abstain"`
	NAnswered         int     `json:"n_answered"`
	AgreementAnswered float64 `json:"agreement_answered"`
	// Thresholds: coverage and agreement keeping only answers whose cand
	// Confidence >= T (abstentions are never covered), ascending T.
	Thresholds []ThresholdStat `json:"thresholds"`
	// NProbs answers carried Probs; Brier (multi-class, summed over the
	// question's labels, mean over NProbs) and ECE (top-label confidence
	// vs accuracy over Bins) use only those. Both 0 when NProbs is 0.
	NProbs int     `json:"n_probs"`
	Brier  float64 `json:"brier"`
	ECE    float64 `json:"ece"`
	Bins   []Bin   `json:"bins"`
	// NScore cases had a score on both sides (KindScore only); ScoreMAE is
	// the mean absolute error of cand Score vs the reference's.
	NScore   int     `json:"n_score"`
	ScoreMAE float64 `json:"score_mae"`
	// Confusion[ref][cand] counts Value pairs.
	Confusion map[string]map[string]int `json:"confusion"`
}

// ThresholdStat is one confidence cut-off. Covered answers have cand
// Confidence >= T; Coverage = Covered / N; Agreement is the match rate
// among them (0 when none).
type ThresholdStat struct {
	T         float64 `json:"t"`
	Covered   int     `json:"covered"`
	Coverage  float64 `json:"coverage"`
	Agreement float64 `json:"agreement"`
}

// Bin is one reliability-diagram bin [Lo, Hi); the last includes 1.
type Bin struct {
	Lo         float64 `json:"lo"`
	Hi         float64 `json:"hi"`
	N          int     `json:"n"`
	Confidence float64 `json:"confidence"`
	Accuracy   float64 `json:"accuracy"`
}

// JSON returns the report as indented JSON.
func (r Report) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// String is Markdown.
func (r Report) String() string { return r.Markdown() }

// Markdown renders a summary table and a coverage / agreement table.
func (r Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d cases\n\n", r.N)
	b.WriteString("| question | kind | n | agree | abstain ref/cand | brier | ece | mae |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	for _, q := range r.Questions {
		brier, ece, mae := "-", "-", "-"
		if q.NProbs > 0 {
			brier, ece = fmt.Sprintf("%.3f", q.Brier), fmt.Sprintf("%.3f", q.ECE)
		}
		if q.NScore > 0 {
			mae = fmt.Sprintf("%.3f", q.ScoreMAE)
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %.3f | %d/%d | %s | %s | %s |\n",
			q.Name, q.Kind, q.N, q.Agreement, q.RefAbstain, q.CandAbstain, brier, ece, mae)
	}
	b.WriteString("\n| question | conf >= | coverage | agree |\n|---|---|---|---|\n")
	for _, q := range r.Questions {
		for _, t := range q.Thresholds {
			fmt.Fprintf(&b, "| %s | %.2f | %.3f | %.3f |\n", q.Name, t.T, t.Coverage, t.Agreement)
		}
	}
	return b.String()
}
