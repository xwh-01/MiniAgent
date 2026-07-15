package environment

import "testing"

func TestDockerContainerCWD(t *testing.T) {
	e := &DockerEnvironment{
		hostWorkspace:      "/tmp/project",
		containerWorkspace: "/workspace",
	}

	tests := []struct {
		name string
		cwd  string
		want string
	}{
		{name: "empty", cwd: "", want: "/workspace"},
		{name: "relative", cwd: "pkg/foo", want: "/workspace/pkg/foo"},
		{name: "dot", cwd: ".", want: "/workspace"},
		{name: "host root", cwd: "/tmp/project", want: "/workspace"},
		{name: "host child", cwd: "/tmp/project/pkg/foo", want: "/workspace/pkg/foo"},
		{name: "container abs", cwd: "/usr/src", want: "/usr/src"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := e.containerCWD(tt.cwd)
			if err != nil {
				t.Fatalf("containerCWD() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("containerCWD() = %q, want %q", got, tt.want)
			}
		})
	}
}
