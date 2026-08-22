package verification

import "testing"

func TestPerformanceReportMeasuresAllSearchMethods(t *testing.T) {
	full := GenerateProgramCorpus()
	corpus := ProgramCorpus{Version: full.Version, MaxScheduleActions: full.MaxScheduleActions}
	families := make(map[string]bool)
	for _, program := range full.Programs {
		if program.ExpectedInvariant == "" || families[program.Family] {
			continue
		}
		families[program.Family] = true
		corpus.Programs = append(corpus.Programs, program)
		if len(corpus.Programs) == 2 {
			break
		}
	}

	report, err := RunPerformanceReport(corpus, 50, 7, 1)
	if err != nil {
		t.Fatal(err)
	}
	if report.ModelBytes == 0 || report.InferenceEvaluations == 0 || report.InferenceNanosecondsPerSchedule <= 0 {
		t.Fatalf("inference measurement is incomplete: %#v", report)
	}
	if report.PeakGoHeapInuseBytes < report.StartGoHeapInuseBytes {
		t.Fatalf("peak heap %d is below start heap %d", report.PeakGoHeapInuseBytes, report.StartGoHeapInuseBytes)
	}
	if len(report.Searches) != 10 || len(report.Summary) != 5 {
		t.Fatalf("got %d searches and %d summaries", len(report.Searches), len(report.Summary))
	}
	for _, search := range report.Searches {
		if search.ElapsedNanoseconds <= 0 {
			t.Fatalf("search has no elapsed time: %#v", search)
		}
	}
}
