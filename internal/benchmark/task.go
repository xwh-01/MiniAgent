package benchmark

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TaskSpec describes one benchmark task.
// It intentionally stays small: prepare a repo/workspace, ask the agent to solve
// a problem statement, then run acceptance commands.
type TaskSpec struct {
	ID                   string   `json:"id"`
	Repo                 string   `json:"repo"`
	BaseCommit           string   `json:"base_commit"`
	Workspace            string   `json:"workspace"`
	ProblemStatement     string   `json:"problem_statement"`
	ProblemStatementFile string   `json:"problem_statement_file"`
	Setup                []string `json:"setup"`
	Test                 []string `json:"test"`
	ConfigFile           string   `json:"config"`
}

// LoadTaskFile loads a JSON or small YAML-subset task spec.
func LoadTaskFile(path string) (TaskSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return TaskSpec{}, err
	}
	var task TaskSpec
	trim := strings.TrimSpace(string(data))
	if trim == "" {
		return TaskSpec{}, fmt.Errorf("empty task file %s", path)
	}
	if strings.HasPrefix(trim, "{") {
		if err := json.Unmarshal(data, &task); err != nil {
			return TaskSpec{}, err
		}
	} else {
		parsed, err := parseTaskYAMLSubset(string(data))
		if err != nil {
			return TaskSpec{}, err
		}
		task = parsed
	}
	if task.ID == "" {
		task.ID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if task.ProblemStatementFile != "" && task.ProblemStatement == "" {
		p := task.ProblemStatementFile
		if !filepath.IsAbs(p) {
			p = filepath.Join(filepath.Dir(path), p)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return TaskSpec{}, err
		}
		task.ProblemStatement = string(b)
	}
	if strings.TrimSpace(task.ProblemStatement) == "" {
		return TaskSpec{}, fmt.Errorf("task %s: missing problem_statement", task.ID)
	}
	return task, nil
}

func parseTaskYAMLSubset(input string) (TaskSpec, error) {
	var task TaskSpec
	lines := strings.Split(input, "\n")
	for i := 0; i < len(lines); i++ {
		raw := strings.TrimRight(lines[i], "\r")
		trim := strings.TrimSpace(raw)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		if leadingSpaces(raw) != 0 {
			return task, fmt.Errorf("line %d: unexpected indentation", i+1)
		}
		key, val, ok := splitKeyValue(trim)
		if !ok {
			return task, fmt.Errorf("line %d: expected key: value", i+1)
		}
		key = normalizeKey(key)
		val = stripInlineComment(strings.TrimSpace(val))
		switch key {
		case "id":
			task.ID = trimQuotes(val)
		case "repo":
			task.Repo = trimQuotes(val)
		case "base_commit", "basecommit":
			task.BaseCommit = trimQuotes(val)
		case "workspace":
			task.Workspace = trimQuotes(val)
		case "problem_statement", "problemstatement":
			if isBlockMarker(val) {
				block, next := collectIndentedBlock(lines, i+1, 0)
				i = next - 1
				task.ProblemStatement = block
			} else {
				task.ProblemStatement = trimQuotes(val)
			}
		case "problem_statement_file", "problemstatementfile":
			task.ProblemStatementFile = trimQuotes(val)
		case "config":
			task.ConfigFile = trimQuotes(val)
		case "setup":
			list, next, err := collectList(lines, i+1, 0)
			if err != nil {
				return task, fmt.Errorf("setup: %w", err)
			}
			i = next - 1
			task.Setup = list
		case "test", "tests":
			list, next, err := collectList(lines, i+1, 0)
			if err != nil {
				return task, fmt.Errorf("test: %w", err)
			}
			i = next - 1
			task.Test = list
		default:
			return task, fmt.Errorf("unknown task key %q", key)
		}
	}
	return task, nil
}

func collectList(lines []string, start int, parentIndent int) ([]string, int, error) {
	var out []string
	end := start
	for end < len(lines) {
		raw := strings.TrimRight(lines[end], "\r")
		trim := strings.TrimSpace(raw)
		if trim == "" || strings.HasPrefix(trim, "#") {
			end++
			continue
		}
		indent := leadingSpaces(raw)
		if indent <= parentIndent {
			break
		}
		if !strings.HasPrefix(trim, "-") {
			return out, end, fmt.Errorf("line %d: expected list item", end+1)
		}
		item := strings.TrimSpace(strings.TrimPrefix(trim, "-"))
		if isBlockMarker(item) {
			block, next := collectIndentedBlock(lines, end+1, indent)
			out = append(out, block)
			end = next
			continue
		}
		out = append(out, trimQuotes(stripInlineComment(item)))
		end++
	}
	return out, end, nil
}

func collectIndentedBlock(lines []string, start int, parentIndent int) (string, int) {
	var blockLines []string
	minIndent := -1
	end := start
	for end < len(lines) {
		raw := strings.TrimRight(lines[end], "\r")
		trim := strings.TrimSpace(raw)
		if trim == "" {
			blockLines = append(blockLines, "")
			end++
			continue
		}
		indent := leadingSpaces(raw)
		if indent <= parentIndent {
			break
		}
		if minIndent < 0 || indent < minIndent {
			minIndent = indent
		}
		blockLines = append(blockLines, raw)
		end++
	}
	if minIndent < 0 {
		return "", end
	}
	for i, line := range blockLines {
		if strings.TrimSpace(line) == "" {
			blockLines[i] = ""
			continue
		}
		if len(line) >= minIndent {
			blockLines[i] = line[minIndent:]
		}
	}
	return strings.TrimRight(strings.Join(blockLines, "\n"), "\n"), end
}

func splitKeyValue(line string) (string, string, bool) {
	inSingle := false
	inDouble := false
	for i, r := range line {
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case ':':
			if !inSingle && !inDouble {
				return line[:i], line[i+1:], true
			}
		}
	}
	return "", "", false
}

func stripInlineComment(s string) string {
	inSingle := false
	inDouble := false
	for i, r := range s {
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble {
				if i == 0 || s[i-1] == ' ' || s[i-1] == '\t' {
					return strings.TrimSpace(s[:i])
				}
			}
		}
	}
	return strings.TrimSpace(s)
}

func leadingSpaces(s string) int {
	count := 0
	for _, r := range s {
		if r != ' ' {
			break
		}
		count++
	}
	return count
}

func normalizeKey(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "-", "_")
	return strings.ToLower(s)
}

func trimQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			var unq string
			if err := json.Unmarshal([]byte(strconvQuote(s)), &unq); err == nil {
				return unq
			}
			return s[1 : len(s)-1]
		}
	}
	return s
}

func strconvQuote(s string) string {
	if strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") {
		return `"` + strings.ReplaceAll(s[1:len(s)-1], `"`, `\"`) + `"`
	}
	return s
}

func isBlockMarker(s string) bool {
	s = strings.TrimSpace(s)
	return s == "|" || s == "|-" || s == ">" || s == ">-"
}
