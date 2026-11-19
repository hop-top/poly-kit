package eval

import "fmt"

// Thresholds is a pass/fail gate for Report.Pass. A zero field is not
// checked. Every question in the report must meet every set field.
type Thresholds struct {
	// MinN is the least cases per question.
	MinN int
	// AtConfidence picks the confidence cut-off MinAgreement and
	// MinCoverage are read at; it must be one of the report's Thresholds.
	// 0 reads overall agreement and the share of answers that are not
	// abstentions.
	AtConfidence float64
	MinAgreement float64
	MinCoverage  float64
	// MaxECE and MaxBrier need answers with Probs.
	MaxECE   float64
	MaxBrier float64
	// MaxScoreMAE needs KindScore questions with scores on both sides.
	MaxScoreMAE float64
}

// Pass reports whether every question meets th; reasons lists each miss as
// "<question>: <what>", empty when ok. A report with no questions fails.
func (r Report) Pass(th Thresholds) (ok bool, reasons []string) {
	if len(r.Questions) == 0 {
		return false, []string{"no questions evaluated"}
	}
	for _, q := range r.Questions {
		fail := func(format string, a ...any) {
			reasons = append(reasons, q.Name+": "+fmt.Sprintf(format, a...))
		}
		if th.MinN > 0 && q.N < th.MinN {
			fail("n %d < %d", q.N, th.MinN)
		}
		if th.MinAgreement > 0 || th.MinCoverage > 0 {
			agree, cover, covered, found := q.at(th.AtConfidence)
			switch {
			case !found:
				fail("confidence %.2f not computed", th.AtConfidence)
			default:
				if th.MinCoverage > 0 && cover < th.MinCoverage {
					fail("coverage %.3f < %.3f%s", cover, th.MinCoverage, atConf(th.AtConfidence))
				}
				if th.MinAgreement > 0 && covered == 0 {
					fail("no covered cases%s", atConf(th.AtConfidence))
				} else if th.MinAgreement > 0 && agree < th.MinAgreement {
					fail("agreement %.3f < %.3f%s", agree, th.MinAgreement, atConf(th.AtConfidence))
				}
			}
		}
		if th.MaxECE > 0 {
			if q.NProbs == 0 {
				fail("ece needs probabilities; none")
			} else if q.ECE > th.MaxECE {
				fail("ece %.3f > %.3f", q.ECE, th.MaxECE)
			}
		}
		if th.MaxBrier > 0 {
			if q.NProbs == 0 {
				fail("brier needs probabilities; none")
			} else if q.Brier > th.MaxBrier {
				fail("brier %.3f > %.3f", q.Brier, th.MaxBrier)
			}
		}
		if th.MaxScoreMAE > 0 {
			if q.NScore == 0 {
				fail("score mae needs scores on both sides; none")
			} else if q.ScoreMAE > th.MaxScoreMAE {
				fail("score mae %.3f > %.3f", q.ScoreMAE, th.MaxScoreMAE)
			}
		}
	}
	return len(reasons) == 0, reasons
}

func atConf(t float64) string {
	if t == 0 {
		return ""
	}
	return fmt.Sprintf(" at confidence >= %.2f", t)
}

// at returns agreement, coverage and covered count at confidence t; t == 0
// means overall agreement and non-abstained share.
func (q QuestionReport) at(t float64) (agree, cover float64, covered int, found bool) {
	if t == 0 {
		return q.Agreement, ratio(q.N-q.CandAbstain, q.N), q.N, true
	}
	for _, s := range q.Thresholds {
		if s.T == t {
			return s.Agreement, s.Coverage, s.Covered, true
		}
	}
	return 0, 0, 0, false
}
