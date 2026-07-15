package action

import "testing"

func TestParseBashBlock(t *testing.T) {
	text := "I will inspect.\n\n```bash\nls -la\n```"
	a, err := ParseBashBlock(text)
	if err != nil {
		t.Fatal(err)
	}
	if a.Command != "ls -la" {
		t.Fatalf("got %q", a.Command)
	}
}

func TestParseBashBlockNoAction(t *testing.T) {
	_, err := ParseBashBlock("hello")
	if err != ErrNoAction {
		t.Fatalf("expected ErrNoAction, got %v", err)
	}
}

func TestParsePowerShellBlock(t *testing.T) {
	a, err := ParseBashBlock("```powershell\nGet-ChildItem -Force\n```")
	if err != nil {
		t.Fatal(err)
	}
	if a.Command != "Get-ChildItem -Force" {
		t.Fatalf("command = %q", a.Command)
	}
}

func TestIsSubmit(t *testing.T) {
	if !IsSubmit("DONE", "x", "hello DONE") {
		t.Fatal("expected submit")
	}
}
