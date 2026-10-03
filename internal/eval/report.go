package eval

import (
	"fmt"
	"io"
	"sort"
)

// Failure is one case a grader failed.
type Failure struct {
	Grader string
	CaseID string
	Slip   string
	Detail string
}

// Tally is one grader's score over a set of cases.
type Tally struct {
	Grader  string
	Applied int
	Passed  int
}

// Rate is the pass rate over the cases the grader applied to; a grader
// that applied to none has no rate, and reports 1 so it reads as nothing
// wrong rather than as a failure.
func (tally Tally) Rate() float64 {
	if tally.Applied == 0 {
		return 1
	}
	return float64(tally.Passed) / float64(tally.Applied)
}

// Report is every grader run over every case.
type Report struct {
	Cases    int
	Tallies  []Tally
	Failures []Failure
}

// Run grades every case with every grader.
func Run(cases []Case, graders []Grader) Report {
	report := Report{Cases: len(cases)}
	for _, grader := range graders {
		tally := Tally{Grader: grader.Name}
		for _, turnCase := range cases {
			verdict := grader.Grade(turnCase)
			if !verdict.Applies {
				continue
			}
			tally.Applied++
			if verdict.Pass {
				tally.Passed++
				continue
			}
			report.Failures = append(report.Failures, Failure{
				Grader: grader.Name, CaseID: turnCase.ID, Slip: turnCase.Slip, Detail: verdict.Detail,
			})
		}
		report.Tallies = append(report.Tallies, tally)
	}
	sort.SliceStable(report.Failures, func(i, j int) bool {
		return report.Failures[i].Grader < report.Failures[j].Grader
	})
	return report
}

// Write prints the report as markdown: the pass-rate table, then each
// failure. No case text is quoted beyond the grader's one-line detail, so
// the report can be posted where the cases cannot.
func (report Report) Write(out io.Writer) {
	fmt.Fprintf(out, "%d cases\n\n| grader | applied | passed | rate |\n|---|---:|---:|---:|\n", report.Cases)
	for _, tally := range report.Tallies {
		fmt.Fprintf(out, "| %s | %d | %d | %.1f%% |\n", tally.Grader, tally.Applied, tally.Passed, 100*tally.Rate())
	}
	if len(report.Failures) == 0 {
		return
	}
	fmt.Fprintf(out, "\n### Failures\n\n")
	for _, failure := range report.Failures {
		slip := ""
		if failure.Slip != "" {
			slip = " [" + failure.Slip + "]"
		}
		fmt.Fprintf(out, "- %s · %s%s: %s\n", failure.Grader, failure.CaseID, slip, failure.Detail)
	}
}
