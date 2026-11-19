// Package judge is the backend-neutral core for typed judgments: a Case
// (state + typed Questions + optional media) goes in, a Verdict (typed,
// probability-bearing Answers + usage/cost) comes out.
//
// Question kinds follow Jev's answer types:
//
//   - KindChoice: one of Options; labels are Option.Value.
//   - KindScore: ordered Anchors, low to high; labels are the anchor
//     indices "0".."n-1"; Answer.Score is the expected anchor index
//     scaled to [0,1] (see ScoreEV).
//   - KindYesNo: labels "yes" / "no".
//
// Any kind may allow abstention (Question.Abstain): label "abstain".
//
// Backends implement Judge (and optionally BatchJudge). This package ships
// no backend; see judgetest for a deterministic fake and calibrate for
// post-hoc probability calibration.
package judge
