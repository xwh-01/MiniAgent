package action

import (
	"errors"
	"strings"
)

// Action is the command selected by the model for execution.
type Action struct {
	Command string `json:"command"`
	Kind    string `json:"kind"`
}

var ErrNoAction = errors.New("no executable bash code block found")

// ParseBashBlock extracts the first supported fenced shell block.
func ParseBashBlock(text string) (Action, error) {
	lines := strings.Split(text, "\n")
	inFence := false
	var b strings.Builder

	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if !inFence {
			if strings.HasPrefix(trim, "```") {
				lang := strings.TrimSpace(strings.TrimPrefix(trim, "```"))
				lang = strings.ToLower(strings.Fields(lang + " ")[0])
				if isShellLanguage(lang) {
					inFence = true
				}
			}
			continue
		}

		if strings.HasPrefix(trim, "```") {
			cmd := strings.TrimSpace(b.String())
			if cmd == "" {
				return Action{}, ErrNoAction
			}
			return Action{Command: cmd, Kind: "bash"}, nil
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}

	return Action{}, ErrNoAction
}

func isShellLanguage(lang string) bool {
	switch lang {
	case "bash", "sh", "shell", "codeagent_bash", "powershell", "pwsh", "ps1":
		return true
	default:
		return false
	}
}

// IsSubmit returns true when a command or output contains the submit sentinel.
func IsSubmit(sentinel string, texts ...string) bool {
	if sentinel == "" {
		return false
	}
	for _, t := range texts {
		if strings.Contains(t, sentinel) {
			return true
		}
	}
	return false
}
