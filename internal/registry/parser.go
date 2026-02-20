package registry

import (
	"bufio"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	sectionTop         = ""
	sectionEnv         = "env"
	sectionRequiredEnv = "required_env"
)

// ParseManifest parses a constrained TOML manifest format and rejects unknown fields.
func ParseManifest(data []byte) (Manifest, error) {
	m := Manifest{
		Enabled: true,
		Args:    []string{},
		Clients: []string{},
		Env:     map[string]string{},
	}

	section := sectionTop
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	lineNo := 0

	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		line = stripInlineComment(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return Manifest{}, fmt.Errorf("line %d: invalid section header", lineNo)
			}
			name := strings.TrimSpace(line[1 : len(line)-1])
			switch name {
			case sectionEnv, sectionRequiredEnv:
				section = name
			default:
				return Manifest{}, fmt.Errorf("line %d: unknown section %q", lineNo, name)
			}
			continue
		}

		key, value, err := splitKeyValue(line)
		if err != nil {
			return Manifest{}, fmt.Errorf("line %d: %w", lineNo, err)
		}

		switch section {
		case sectionTop:
			if err := parseTopLevel(&m, key, value); err != nil {
				return Manifest{}, fmt.Errorf("line %d: %w", lineNo, err)
			}
		case sectionEnv:
			sv, err := parseString(value)
			if err != nil {
				return Manifest{}, fmt.Errorf("line %d: invalid env value for %q: %w", lineNo, key, err)
			}
			m.Env[key] = sv
		case sectionRequiredEnv:
			if key != "keys" {
				return Manifest{}, fmt.Errorf("line %d: unknown key %q in [required_env]", lineNo, key)
			}
			arr, err := parseStringArray(value)
			if err != nil {
				return Manifest{}, fmt.Errorf("line %d: invalid required_env keys: %w", lineNo, err)
			}
			m.RequiredEnv.Keys = arr
		default:
			return Manifest{}, fmt.Errorf("line %d: unknown parse section", lineNo)
		}
	}

	if err := scanner.Err(); err != nil {
		return Manifest{}, fmt.Errorf("scan manifest: %w", err)
	}

	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// MarshalManifest renders a deterministic TOML manifest.
func MarshalManifest(m Manifest) ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}

	var b strings.Builder

	fmt.Fprintf(&b, "name = %s\n", strconv.Quote(m.Name))
	fmt.Fprintf(&b, "command = %s\n", strconv.Quote(m.Command))
	fmt.Fprintf(&b, "args = %s\n", formatStringArray(m.Args))
	fmt.Fprintf(&b, "enabled = %t\n", m.Enabled)
	fmt.Fprintf(&b, "clients = %s\n", formatStringArray(m.Clients))
	if strings.TrimSpace(m.Description) != "" {
		fmt.Fprintf(&b, "description = %s\n", strconv.Quote(m.Description))
	}

	if len(m.Env) > 0 {
		keys := make([]string, 0, len(m.Env))
		for key := range m.Env {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		b.WriteString("\n[env]\n")
		for _, key := range keys {
			fmt.Fprintf(&b, "%s = %s\n", key, strconv.Quote(m.Env[key]))
		}
	}

	if len(m.RequiredEnv.Keys) > 0 {
		keys := append([]string(nil), m.RequiredEnv.Keys...)
		sort.Strings(keys)
		b.WriteString("\n[required_env]\n")
		fmt.Fprintf(&b, "keys = %s\n", formatStringArray(keys))
	}

	return []byte(b.String()), nil
}

func parseTopLevel(m *Manifest, key, value string) error {
	switch key {
	case "name":
		sv, err := parseString(value)
		if err != nil {
			return fmt.Errorf("invalid name: %w", err)
		}
		m.Name = sv
	case "command":
		sv, err := parseString(value)
		if err != nil {
			return fmt.Errorf("invalid command: %w", err)
		}
		m.Command = sv
	case "description":
		sv, err := parseString(value)
		if err != nil {
			return fmt.Errorf("invalid description: %w", err)
		}
		m.Description = sv
	case "enabled":
		bv, err := parseBool(value)
		if err != nil {
			return fmt.Errorf("invalid enabled: %w", err)
		}
		m.Enabled = bv
	case "args":
		av, err := parseStringArray(value)
		if err != nil {
			return fmt.Errorf("invalid args: %w", err)
		}
		m.Args = av
	case "clients":
		cv, err := parseStringArray(value)
		if err != nil {
			return fmt.Errorf("invalid clients: %w", err)
		}
		m.Clients = cv
	default:
		return fmt.Errorf("unknown top-level key %q", key)
	}
	return nil
}

func splitKeyValue(line string) (string, string, error) {
	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", fmt.Errorf("expected key = value")
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" {
		return "", "", fmt.Errorf("empty key")
	}
	if value == "" {
		return "", "", fmt.Errorf("empty value")
	}
	return key, value, nil
}

func parseString(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if !strings.HasPrefix(value, "\"") || !strings.HasSuffix(value, "\"") {
		return "", fmt.Errorf("expected quoted string")
	}
	unquoted, err := strconv.Unquote(value)
	if err != nil {
		return "", err
	}
	return unquoted, nil
}

func parseBool(raw string) (bool, error) {
	switch strings.TrimSpace(raw) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("expected true or false")
	}
}

func parseStringArray(raw string) ([]string, error) {
	value := strings.TrimSpace(raw)
	if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
		return nil, fmt.Errorf("expected array")
	}
	inner := strings.TrimSpace(value[1 : len(value)-1])
	if inner == "" {
		return []string{}, nil
	}

	var out []string
	i := 0
	for i < len(inner) {
		for i < len(inner) && (inner[i] == ' ' || inner[i] == '\t' || inner[i] == ',') {
			i++
		}
		if i >= len(inner) {
			break
		}
		if inner[i] != '"' {
			return nil, fmt.Errorf("array values must be quoted strings")
		}

		start := i
		i++
		escaped := false
		for i < len(inner) {
			if escaped {
				escaped = false
				i++
				continue
			}
			if inner[i] == '\\' {
				escaped = true
				i++
				continue
			}
			if inner[i] == '"' {
				i++
				break
			}
			i++
		}
		if i > len(inner) {
			return nil, fmt.Errorf("unterminated string literal")
		}
		if i <= start+1 || inner[i-1] != '"' {
			return nil, fmt.Errorf("unterminated string literal")
		}

		token := inner[start:i]
		parsed, err := strconv.Unquote(token)
		if err != nil {
			return nil, err
		}
		out = append(out, parsed)

		for i < len(inner) && (inner[i] == ' ' || inner[i] == '\t') {
			i++
		}
		if i < len(inner) {
			if inner[i] != ',' {
				return nil, fmt.Errorf("expected comma between array values")
			}
			i++
		}
	}

	return out, nil
}

func formatStringArray(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.Quote(value))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func stripInlineComment(line string) string {
	inQuote := false
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		switch r {
		case '\\':
			if inQuote {
				escaped = true
			}
		case '"':
			inQuote = !inQuote
		case '#':
			if !inQuote {
				return strings.TrimSpace(line[:i])
			}
		}
	}
	return strings.TrimSpace(line)
}
