package model

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// HumanClient asks a person to type the assistant response for each step.
// End a response with a line containing only EOF.
type HumanClient struct {
	In   io.Reader
	Out  io.Writer
	turn int
}

func NewHumanClient() *HumanClient {
	return &HumanClient{In: os.Stdin, Out: os.Stdout}
}

func (c *HumanClient) Name() string { return "human" }

func (c *HumanClient) Generate(ctx context.Context, messages []Message, opts Options) (*Response, error) {
	_ = opts
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if c.In == nil {
		c.In = os.Stdin
	}
	if c.Out == nil {
		c.Out = os.Stdout
	}
	c.turn++
	fmt.Fprintf(c.Out, "\n--- HumanModel turn %d ---\n", c.turn)
	if len(messages) > 0 {
		last := messages[len(messages)-1]
		fmt.Fprintf(c.Out, "Last %s message:\n%s\n", last.Role, last.Content)
	}
	fmt.Fprintln(c.Out, "\nType the assistant response. Include a fenced bash block. End with a line containing only EOF.")

	scanner := bufio.NewScanner(c.In)
	var b strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "EOF" {
			break
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	content := strings.TrimRight(b.String(), "\n")
	return &Response{Content: content}, nil
}
