package report

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/example/go-code-agent/internal/benchmark"
)

// Report is a compact benchmark report written to report.json.
type Report struct {
	GeneratedAt     time.Time          `json:"generated_at"`
	BenchmarkDir    string             `json:"benchmark_dir"`
	StartedAt       time.Time          `json:"started_at"`
	EndedAt         time.Time          `json:"ended_at"`
	DurationSeconds float64            `json:"duration_seconds"`
	Total           int                `json:"total"`
	Resolved        int                `json:"resolved"`
	Failed          int                `json:"failed"`
	Errored         int                `json:"errored"`
	ResolveRate     float64            `json:"resolve_rate"`
	AverageSteps    float64            `json:"average_steps"`
	AverageDuration float64            `json:"average_duration_seconds"`
	StatusCounts    map[string]int     `json:"status_counts"`
	Slowest         []benchmark.Result `json:"slowest"`
	MostSteps       []benchmark.Result `json:"most_steps"`
	Results         []benchmark.Result `json:"results"`
}

// Build reads summary.json from benchmarkDir and computes aggregate fields.
func Build(benchmarkDir string) (*Report, error) {
	if benchmarkDir == "" {
		benchmarkDir = "."
	}
	path := filepath.Join(benchmarkDir, "summary.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read summary.json: %w", err)
	}
	var sum benchmark.Summary
	if err := json.Unmarshal(data, &sum); err != nil {
		return nil, fmt.Errorf("parse summary.json: %w", err)
	}
	if sum.OutputDir == "" {
		sum.OutputDir = benchmarkDir
	}
	return fromSummary(benchmarkDir, sum), nil
}

func fromSummary(dir string, sum benchmark.Summary) *Report {
	rep := &Report{
		GeneratedAt:     time.Now(),
		BenchmarkDir:    dir,
		StartedAt:       sum.StartedAt,
		EndedAt:         sum.EndedAt,
		DurationSeconds: sum.DurationSeconds,
		Total:           sum.Total,
		Resolved:        sum.Resolved,
		Failed:          sum.Failed,
		Errored:         sum.Errored,
		StatusCounts:    map[string]int{},
		Results:         append([]benchmark.Result(nil), sum.Results...),
	}
	var stepTotal int
	var durationTotal float64
	for _, r := range rep.Results {
		rep.StatusCounts[r.Status]++
		stepTotal += r.Steps
		durationTotal += r.DurationSeconds
	}
	if rep.Total > 0 {
		rep.ResolveRate = float64(rep.Resolved) / float64(rep.Total)
		rep.AverageSteps = float64(stepTotal) / float64(rep.Total)
		rep.AverageDuration = durationTotal / float64(rep.Total)
	}
	rep.Slowest = topN(rep.Results, 5, func(a, b benchmark.Result) bool { return a.DurationSeconds > b.DurationSeconds })
	rep.MostSteps = topN(rep.Results, 5, func(a, b benchmark.Result) bool { return a.Steps > b.Steps })
	return rep
}

func topN(results []benchmark.Result, n int, less func(a, b benchmark.Result) bool) []benchmark.Result {
	copyResults := append([]benchmark.Result(nil), results...)
	sort.SliceStable(copyResults, func(i, j int) bool { return less(copyResults[i], copyResults[j]) })
	if len(copyResults) > n {
		copyResults = copyResults[:n]
	}
	return copyResults
}

// SaveJSON writes report.json.
func SaveJSON(path string, rep *Report) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// SaveHTML writes a self-contained static HTML report.
func SaveHTML(path string, rep *Report) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("<!doctype html><html><head><meta charset=\"utf-8\"><title>CodeAgent Benchmark Report</title>")
	b.WriteString("<style>body{font-family:system-ui,-apple-system,Segoe UI,sans-serif;margin:2rem;line-height:1.45}table{border-collapse:collapse;width:100%;margin-top:1rem}th,td{border:1px solid #ddd;padding:.5rem;text-align:left;vertical-align:top}th{background:#f5f5f5}.ok{color:#087f23;font-weight:700}.bad{color:#b00020;font-weight:700}.muted{color:#666}.cards{display:flex;gap:1rem;flex-wrap:wrap}.card{border:1px solid #ddd;border-radius:8px;padding:1rem;min-width:10rem}code{background:#f6f8fa;padding:.1rem .25rem;border-radius:4px}</style>")
	b.WriteString("</head><body>")
	fmt.Fprintf(&b, "<h1>CodeAgent Benchmark Report</h1><p class=\"muted\">Generated at %s</p>", html.EscapeString(rep.GeneratedAt.Format(time.RFC3339)))
	b.WriteString("<div class=\"cards\">")
	card(&b, "Total", fmt.Sprintf("%d", rep.Total))
	card(&b, "Resolved", fmt.Sprintf("%d", rep.Resolved))
	card(&b, "Failed", fmt.Sprintf("%d", rep.Failed))
	card(&b, "Errored", fmt.Sprintf("%d", rep.Errored))
	card(&b, "Resolve rate", fmt.Sprintf("%.1f%%", rep.ResolveRate*100))
	card(&b, "Avg steps", fmt.Sprintf("%.1f", rep.AverageSteps))
	b.WriteString("</div>")
	b.WriteString("<h2>Results</h2><table><thead><tr><th>Task</th><th>Status</th><th>Resolved</th><th>Tests</th><th>Steps</th><th>Duration</th><th>Error</th><th>Artifacts</th></tr></thead><tbody>")
	for _, r := range rep.Results {
		statusClass := "bad"
		if r.Resolved {
			statusClass = "ok"
		}
		fmt.Fprintf(&b, "<tr><td>%s</td><td class=\"%s\">%s</td><td>%v</td><td>%v</td><td>%d</td><td>%.2fs</td><td>%s</td><td>%s</td></tr>",
			esc(r.TaskID), statusClass, esc(r.Status), r.Resolved, r.TestPassed, r.Steps, r.DurationSeconds, esc(r.Error), artifactLinks(r))
	}
	b.WriteString("</tbody></table>")
	b.WriteString("</body></html>")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func card(b *strings.Builder, title, value string) {
	fmt.Fprintf(b, "<div class=\"card\"><div class=\"muted\">%s</div><strong>%s</strong></div>", esc(title), esc(value))
}

func artifactLinks(r benchmark.Result) string {
	var links []string
	if r.TrajectoryPath != "" {
		links = append(links, link(r.TrajectoryPath, "trajectory"))
	}
	if r.PatchPath != "" {
		links = append(links, link(r.PatchPath, "patch"))
	}
	if r.ResultPath != "" {
		links = append(links, link(r.ResultPath, "result"))
	}
	return strings.Join(links, " · ")
}

func link(path, label string) string {
	return fmt.Sprintf("<a href=\"%s\">%s</a>", esc(path), esc(label))
}

func esc(s string) string { return html.EscapeString(s) }

// RenderText writes a concise terminal report.
func RenderText(w io.Writer, rep *Report) error {
	if rep == nil {
		return fmt.Errorf("report is nil")
	}
	fmt.Fprintf(w, "Benchmark: %s\n", rep.BenchmarkDir)
	fmt.Fprintf(w, "Total:     %d\n", rep.Total)
	fmt.Fprintf(w, "Resolved:  %d\n", rep.Resolved)
	fmt.Fprintf(w, "Failed:    %d\n", rep.Failed)
	fmt.Fprintf(w, "Errored:   %d\n", rep.Errored)
	fmt.Fprintf(w, "Rate:      %.1f%%\n", rep.ResolveRate*100)
	fmt.Fprintf(w, "Avg steps: %.1f\n", rep.AverageSteps)
	fmt.Fprintf(w, "Avg time:  %.2fs\n", rep.AverageDuration)
	if len(rep.StatusCounts) > 0 {
		fmt.Fprintln(w, "\nStatus counts:")
		keys := make([]string, 0, len(rep.StatusCounts))
		for k := range rep.StatusCounts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(w, "  %-18s %d\n", k, rep.StatusCounts[k])
		}
	}
	if len(rep.Results) > 0 {
		fmt.Fprintln(w, "\nResults:")
		for _, r := range rep.Results {
			mark := "✗"
			if r.Resolved {
				mark = "✓"
			}
			fmt.Fprintf(w, "  %s %-24s status=%-16s tests=%v steps=%d time=%.2fs", mark, r.TaskID, r.Status, r.TestPassed, r.Steps, r.DurationSeconds)
			if r.Error != "" {
				fmt.Fprintf(w, " error=%s", oneLine(r.Error, 80))
			}
			fmt.Fprintln(w)
		}
	}
	return nil
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
