package inspect

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/example/go-code-agent/internal/trajectory"
)

// Options controls trajectory rendering.
type Options struct {
	Step          int
	ShowMessages  bool
	ShowFull      bool
	MaxFieldChars int
}

// Render writes a human-readable trajectory inspection view.
func Render(w io.Writer, run *trajectory.Run, opts Options) error {
	if run == nil {
		return fmt.Errorf("trajectory is nil")
	}
	if opts.MaxFieldChars <= 0 {
		opts.MaxFieldChars = 2000
	}

	fmt.Fprintf(w, "Trajectory: %s\n", nonEmpty(run.Format, "unknown"))
	fmt.Fprintf(w, "Status:     %s\n", nonEmpty(run.Status, "unknown"))
	fmt.Fprintf(w, "Model:      %s\n", nonEmpty(run.Model, "unknown"))
	fmt.Fprintf(w, "Workspace:  %s\n", nonEmpty(run.Workspace, "unknown"))
	fmt.Fprintf(w, "Started:    %s\n", formatTime(run.StartedAt))
	fmt.Fprintf(w, "Ended:      %s\n", formatTime(run.EndedAt))
	fmt.Fprintf(w, "Duration:   %.2fs\n", run.Stats.DurationSeconds)
	fmt.Fprintf(w, "Steps:      %d\n", len(run.Steps))
	fmt.Fprintf(w, "API calls:  %d\n", run.Stats.APICalls)
	if run.Stats.TotalTokens > 0 {
		fmt.Fprintf(w, "Tokens:     prompt=%d completion=%d total=%d\n", run.Stats.PromptTokens, run.Stats.CompletionTokens, run.Stats.TotalTokens)
	}
	if strings.TrimSpace(run.Task) != "" {
		fmt.Fprintf(w, "\nTask:\n%s\n", indent(truncate(run.Task, opts.MaxFieldChars, opts.ShowFull), "  "))
	}

	if opts.ShowMessages {
		fmt.Fprintln(w, "\nMessages:")
		for i, msg := range run.Messages {
			fmt.Fprintf(w, "\n[%d] %s\n", i+1, strings.ToUpper(msg.Role))
			fmt.Fprintln(w, indent(truncate(msg.Content, opts.MaxFieldChars, opts.ShowFull), "  "))
		}
		return nil
	}

	steps := run.Steps
	if opts.Step > 0 {
		if opts.Step > len(run.Steps) {
			return fmt.Errorf("step %d out of range; trajectory has %d steps", opts.Step, len(run.Steps))
		}
		steps = []trajectory.Step{run.Steps[opts.Step-1]}
	}
	fmt.Fprintln(w, "\nSteps:")
	if len(steps) == 0 {
		fmt.Fprintln(w, "  (no steps recorded)")
		return nil
	}
	for _, step := range steps {
		fmt.Fprintf(w, "\n[%d]", step.Index)
		if step.Command != "" {
			fmt.Fprintf(w, " command=%q", oneLine(step.Command, 120))
		}
		if step.ParseError != "" {
			fmt.Fprintf(w, " parse_error=%q", step.ParseError)
		}
		if step.PolicyError != "" {
			fmt.Fprintf(w, " policy_error=%q", step.PolicyError)
		}
		if step.Observation != nil {
			fmt.Fprintf(w, " exit=%d timeout=%v duration=%s", step.Observation.ExitCode, step.Observation.TimedOut, step.Observation.Duration)
		}
		fmt.Fprintln(w)

		fmt.Fprintln(w, "  Assistant:")
		fmt.Fprintln(w, indent(truncate(step.AssistantMessage, opts.MaxFieldChars, opts.ShowFull), "    "))
		if step.Command != "" {
			fmt.Fprintln(w, "  Command:")
			fmt.Fprintln(w, indent(step.Command, "    "))
		}
		if step.Observation != nil {
			if step.Observation.Stdout != "" {
				fmt.Fprintln(w, "  STDOUT:")
				fmt.Fprintln(w, indent(truncate(step.Observation.Stdout, opts.MaxFieldChars, opts.ShowFull), "    "))
			}
			if step.Observation.Stderr != "" {
				fmt.Fprintln(w, "  STDERR:")
				fmt.Fprintln(w, indent(truncate(step.Observation.Stderr, opts.MaxFieldChars, opts.ShowFull), "    "))
			}
			if step.Observation.Error != "" {
				fmt.Fprintln(w, "  Error:")
				fmt.Fprintln(w, indent(step.Observation.Error, "    "))
			}
		}
	}
	return nil
}

func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.Format(time.RFC3339)
}

func indent(s, prefix string) string {
	if s == "" {
		return prefix + "(empty)"
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

func truncate(s string, max int, showFull bool) string {
	if showFull || max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("\n... [truncated %d bytes]", len(s)-max)
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
