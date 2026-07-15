package environment

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLocalEnvironmentExecute(t *testing.T) {
	env, err := NewLocalEnvironment(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	res, err := env.Execute(context.Background(), ExecRequest{Command: "echo hello", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "hello") {
		t.Fatalf("stdout = %q", res.Stdout)
	}
}

func TestLocalEnvironmentTimeout(t *testing.T) {
	env, err := NewLocalEnvironment(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	command := "sleep 10"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Seconds 10"
	}
	started := time.Now()
	res, err := env.Execute(context.Background(), ExecRequest{Command: command, Timeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut {
		t.Fatalf("TimedOut = false; result = %+v", res)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatalf("timed-out command took too long: %s", time.Since(started))
	}
}
