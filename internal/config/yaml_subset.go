package config

import (
	"fmt"
	"strings"
)

type yamlFrame struct {
	indent int
	key    string
}

// parseYAMLSubset parses the tiny YAML subset used by codeagent configs:
// nested maps by indentation, scalar key/value pairs, comments, quoted strings,
// booleans/numbers as strings, and literal blocks with |. It deliberately does
// not support lists, anchors, aliases, or complex YAML tags.
func parseYAMLSubset(input string) (map[string]string, error) {
	lines := strings.Split(input, "\n")
	flat := map[string]string{}
	stack := []yamlFrame{}

	for i := 0; i < len(lines); i++ {
		raw := strings.TrimRight(lines[i], "\r")
		trim := strings.TrimSpace(raw)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		if strings.HasPrefix(trim, "-") {
			return nil, fmt.Errorf("line %d: lists are not supported by this minimal parser", i+1)
		}
		indent := leadingSpaces(raw)
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}

		key, value, ok := splitYAMLKeyValue(strings.TrimSpace(raw))
		if !ok {
			return nil, fmt.Errorf("line %d: expected key: value", i+1)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("line %d: empty key", i+1)
		}
		value = stripInlineComment(strings.TrimSpace(value))

		if value == "" {
			stack = append(stack, yamlFrame{indent: indent, key: key})
			continue
		}

		if value == "|" || value == "|-" || value == ">" || value == ">-" {
			block, next := collectBlock(lines, i+1, indent)
			i = next - 1
			flat[joinPath(stack, key)] = block
			continue
		}

		flat[joinPath(stack, key)] = trimQuotes(value)
	}
	return flat, nil
}

func splitYAMLKeyValue(line string) (string, string, bool) {
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

func collectBlock(lines []string, start int, parentIndent int) (string, int) {
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
		if minIndent == -1 || indent < minIndent {
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

func joinPath(stack []yamlFrame, key string) string {
	parts := make([]string, 0, len(stack)+1)
	for _, fr := range stack {
		parts = append(parts, fr.key)
	}
	parts = append(parts, key)
	return strings.Join(parts, ".")
}
