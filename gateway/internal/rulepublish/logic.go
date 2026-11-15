package rulepublish

import (
	"errors"
	"strings"
)

func logical(ruleType string) bool {
	switch kind(ruleType) {
	case "AND", "OR", "NOT":
		return true
	default:
		return false
	}
}

func splitFields(raw string) []string {
	var fields []string
	start, nesting := 0, 0
	for i, ch := range raw {
		switch ch {
		case '(':
			nesting++
		case ')':
			nesting--
		case ',':
			if nesting == 0 {
				fields = append(fields, strings.TrimSpace(raw[start:i]))
				start = i + 1
			}
		}
		if nesting == 0 && (strings.HasPrefix(raw[i:], " && ") || strings.HasPrefix(raw[i:], " || ")) {
			fields = append(fields, strings.TrimSpace(raw[start:i]))
			start = i + 4
		}
	}
	return append(fields, strings.TrimSpace(raw[start:]))
}

// Normalize the YAML operand list and the controller's rendered expression
// for comparison. The startup file retains its original rule strings.
func logicKey(ruleType, payload string, depth int) (string, error) {
	if depth > 32 || !strings.HasPrefix(payload, "(") || !strings.HasSuffix(payload, ")") {
		return "", errors.New("逻辑规则条件格式无效")
	}
	body := strings.TrimSpace(payload[1 : len(payload)-1])
	if kind(ruleType) == "NOT" {
		body = strings.TrimPrefix(body, "!")
	}
	var terms []string
	for _, term := range splitFields(body) {
		if len(term) < 3 || term[0] != '(' || term[len(term)-1] != ')' {
			return "", errors.New("逻辑规则子条件格式无效")
		}
		fields := splitFields(term[1 : len(term)-1])
		if len(fields) < 2 {
			return "", errors.New("逻辑规则缺少子条件")
		}
		tp, payload := fields[0], fields[1]
		if logical(tp) {
			nested, err := logicKey(tp, payload, depth+1)
			if err != nil {
				return "", err
			}
			payload = nested
		}
		key := canonical(Rule{Type: tp, Payload: payload})
		terms = append(terms, "("+key.Type+","+key.Payload+")")
	}
	return "(" + strings.Join(terms, ",") + ")", nil
}
