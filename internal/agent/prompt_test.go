package agent

import (
	"strings"
	"testing"
)

func TestDefaultSystemPromptForPowerShell(t *testing.T) {
	prompt := DefaultSystemPromptForShell("powershell")
	if !strings.Contains(prompt, "```powershell") || !strings.Contains(prompt, "Get-ChildItem -Force") {
		t.Fatalf("prompt is not PowerShell-aware:\n%s", prompt)
	}
}

func TestDefaultSystemPromptForShell(t *testing.T) {
	if got := DefaultSystemPromptForShell("sh"); got != DefaultSystemPrompt {
		t.Fatal("sh prompt should preserve the default prompt")
	}
}
