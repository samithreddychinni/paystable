package verification

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"runtime"
	"slices"
	"time"
)

type PerformanceReport struct {
	Version                         int                  `json:"version"`
	Budget                          int                  `json:"budget"`
	Seed                            int64                `json:"seed"`
	ModelBytes                      int                  `json:"model_bytes"`
	InferenceRepetitions            int                  `json:"inference_repetitions"`
	InferenceEvaluations            int                  `json:"inference_evaluations"`
	InferenceTotalNanoseconds       int64                `json:"inference_total_nanoseconds"`
	InferenceNanosecondsPerSchedule float64              `json:"inference_nanoseconds_per_schedule"`
	HeapSampleIntervalNanoseconds   int64                `json:"heap_sample_interval_nanoseconds"`
	StartGoHeapInuseBytes           uint64               `json:"start_go_heap_inuse_bytes"`
	PeakGoHeapInuseBytes            uint64               `json:"peak_go_heap_inuse_bytes"`
	PeakGoHeapIncreaseBytes         uint64               `json:"peak_go_heap_increase_bytes"`
	Searches                        []PerformanceSearch  `json:"searches"`
	Summary                         []PerformanceSummary `json:"summary"`
}

type PerformanceSearch struct {
	Method                   string `json:"method"`
	Program                  string `json:"program"`
	Found                    bool   `json:"found"`
	Executions               int    `json:"executions"`
	ElapsedNanoseconds       int64  `json:"elapsed_nanoseconds"`
	TimeToFindingNanoseconds int64  `json:"time_to_finding_nanoseconds,omitempty"`
}

type PerformanceSummary struct {
	Method                         string `json:"method"`
	Findings                       int    `json:"findings"`
	MedianTimeToFindingNanoseconds int64  `json:"median_time_to_finding_nanoseconds"`
}

// RunPerformanceReport measures one local process. Its timing and memory values are not deterministic.
func RunPerformanceReport(corpus ProgramCorpus, budget int, seed int64, repetitions int) (report PerformanceReport, err error) {
	if budget < 50 || budget > 1000 {
		return PerformanceReport{}, fmt.Errorf("budget must be between 50 and 1000")
	}
	if repetitions < 1 || repetitions > 10000 {
		return PerformanceReport{}, fmt.Errorf("repetitions must be between 1 and 10000")
	}

	runtime.GC()
	const sampleInterval = time.Millisecond
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	startHeap := memory.HeapInuse
	stopSampling := make(chan struct{})
	peakHeap := make(chan uint64)
	go samplePeakHeap(startHeap, sampleInterval, stopSampling, peakHeap)
	defer func() {
		close(stopSampling)
		report.PeakGoHeapInuseBytes = <-peakHeap
		if report.PeakGoHeapInuseBytes > report.StartGoHeapInuseBytes {
			report.PeakGoHeapIncreaseBytes = report.PeakGoHeapInuseBytes - report.StartGoHeapInuseBytes
		}
	}()

	report = PerformanceReport{
		Version: 1, Budget: budget, Seed: seed, InferenceRepetitions: repetitions,
		HeapSampleIntervalNanoseconds: sampleInterval.Nanoseconds(), StartGoHeapInuseBytes: startHeap,
	}
	model, err := TrainScout(corpus)
	if err != nil {
		return PerformanceReport{}, err
	}
	modelJSON, err := json.Marshal(model)
	if err != nil {
		return PerformanceReport{}, err
	}
	report.ModelBytes = len(modelJSON)

	candidates := make(map[string][]searchCandidate, len(corpus.Programs))
	foldModels := make(map[string]ScoutModel)
	for _, program := range corpus.Programs {
		graph, compileErr := CompileBehaviorGraph(program.Program, corpus.MaxScheduleActions)
		if compileErr != nil {
			return PerformanceReport{}, compileErr
		}
		candidates[program.Program] = graphCandidates(graph)
		if program.ExpectedInvariant != "" {
			if _, exists := foldModels[program.Family]; !exists {
				foldModels[program.Family], err = trainScout(corpus, program.Family)
				if err != nil {
					return PerformanceReport{}, err
				}
			}
		}
	}

	inferenceStart := time.Now()
	var inferenceScore float64
	for range repetitions {
		for _, program := range corpus.Programs {
			for _, candidate := range candidates[program.Program] {
				inferenceScore += model.score(candidate.actions)
				report.InferenceEvaluations++
			}
		}
	}
	report.InferenceTotalNanoseconds = time.Since(inferenceStart).Nanoseconds()
	runtime.KeepAlive(inferenceScore)
	report.InferenceNanosecondsPerSchedule = float64(report.InferenceTotalNanoseconds) / float64(report.InferenceEvaluations)

	methods := []string{BaselineBounded, BaselineRandom, BaselineCoverage, ScoutMethod, ScoutClosedLoopMethod}
	for _, method := range methods {
		for i, program := range corpus.Programs {
			if program.ExpectedInvariant == "" {
				continue
			}
			search, searchErr := measureSearch(method, program, candidates[program.Program], budget, seed+int64(i), foldModels[program.Family])
			if searchErr != nil {
				return PerformanceReport{}, searchErr
			}
			report.Searches = append(report.Searches, search)
		}
		report.Summary = append(report.Summary, summarizePerformance(method, report.Searches))
	}
	return report, nil
}

func samplePeakHeap(peak uint64, interval time.Duration, stop <-chan struct{}, done chan<- uint64) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			var memory runtime.MemStats
			runtime.ReadMemStats(&memory)
			peak = max(peak, memory.HeapInuse)
		case <-stop:
			var memory runtime.MemStats
			runtime.ReadMemStats(&memory)
			done <- max(peak, memory.HeapInuse)
			return
		}
	}
}

func measureSearch(method string, program ProgramCase, source []searchCandidate, budget int, seed int64, model ScoutModel) (PerformanceSearch, error) {
	candidates := slices.Clone(source)
	start := time.Now()
	var run BaselineRun
	var err error
	switch method {
	case BaselineBounded:
		run, err = evaluateCandidates(method, program, candidates, budget)
	case BaselineRandom:
		rand.New(rand.NewSource(seed)).Shuffle(len(candidates), func(i, j int) {
			candidates[i], candidates[j] = candidates[j], candidates[i]
		})
		run, err = evaluateCandidates(method, program, candidates, budget)
	case BaselineCoverage:
		run, err = evaluateCandidates(method, program, coverageOrder(candidates), budget)
	case ScoutMethod:
		rankScoutCandidates(candidates, model)
		run, err = evaluateCandidates(method, program, candidates, budget)
	case ScoutClosedLoopMethod:
		run, err = evaluateClosedLoop(program, candidates, budget, model)
	default:
		return PerformanceSearch{}, fmt.Errorf("unsupported search method %q", method)
	}
	elapsed := time.Since(start).Nanoseconds()
	if err != nil {
		return PerformanceSearch{}, err
	}
	search := PerformanceSearch{Method: method, Program: program.Program, Found: run.Found, Executions: run.Executions, ElapsedNanoseconds: elapsed}
	if run.Found {
		search.TimeToFindingNanoseconds = elapsed
	}
	return search, nil
}

func summarizePerformance(method string, searches []PerformanceSearch) PerformanceSummary {
	summary := PerformanceSummary{Method: method}
	var times []int64
	for _, search := range searches {
		if search.Method == method && search.Found {
			times = append(times, search.TimeToFindingNanoseconds)
		}
	}
	if len(times) == 0 {
		return summary
	}
	slices.Sort(times)
	summary.Findings = len(times)
	summary.MedianTimeToFindingNanoseconds = times[len(times)/2]
	return summary
}
