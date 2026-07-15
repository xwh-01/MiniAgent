package benchmark

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTaskFileYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bug.yaml")
	yaml := `
id: bug-001
workspace: ./repo
problem_statement: |
  Fix the greeting bug.
  Run the tests.
setup:
  - echo setup
test:
  - grep -q hello hello.txt
  - |
    test -f hello.txt
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	task, err := LoadTaskFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "bug-001" || task.Workspace != "./repo" {
		t.Fatalf("unexpected task: %+v", task)
	}
	if task.ProblemStatement != "Fix the greeting bug.\nRun the tests." {
		t.Fatalf("unexpected problem statement: %q", task.ProblemStatement)
	}
	if len(task.Setup) != 1 || task.Setup[0] != "echo setup" {
		t.Fatalf("unexpected setup: %+v", task.Setup)
	}
	if len(task.Test) != 2 || task.Test[1] != "test -f hello.txt" {
		t.Fatalf("unexpected tests: %+v", task.Test)
	}
}
