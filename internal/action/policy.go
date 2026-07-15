package action

import (
	"fmt"
	"strings"
)

// Policy is intentionally simple for M1: block obvious host-destructive commands.
type Policy struct {
	MaxCommandChars int
}

func (p Policy) Check(a Action) error {
	max := p.MaxCommandChars
	if max <= 0 {
		max = 20_000
	}
	cmd := strings.TrimSpace(a.Command)
	if len(cmd) > max {
		return fmt.Errorf("command too long: %d > %d chars", len(cmd), max)
	}

	dangerous := []string{
		"rm -rf /",
		"rm -fr /",
		":(){ :|:& };:",
		"mkfs.",
		"dd if=",
		"shutdown",
		"reboot",
	}
	lower := strings.ToLower(cmd)
	for _, needle := range dangerous {
		if strings.Contains(lower, needle) {
			return fmt.Errorf("blocked dangerous command containing %q", needle)
		}
	}
	return nil
}
