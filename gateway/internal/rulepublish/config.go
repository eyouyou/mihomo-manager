package rulepublish

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

type Rule struct {
	Index   int    `json:"index"`
	Type    string `json:"type"`
	Payload string `json:"payload"`
	Proxy   string `json:"proxy"`
	Size    int    `json:"size"`
	Extra   struct {
		Disabled  bool   `json:"disabled"`
		HitCount  uint64 `json:"hitCount,omitempty"`
		MissCount uint64 `json:"missCount,omitempty"`
		HitAt     string `json:"hitAt,omitempty"`
		MissAt    string `json:"missAt,omitempty"`
	} `json:"extra"`
}

type disabledEntry struct {
	Position int    `yaml:"position"`
	Rule     string `yaml:"rule"`
}

const inactiveKey = "x-mihomo-manager-disabled-rules"

var typeNames = []string{"DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD", "DOMAIN-REGEX", "DOMAIN-WILDCARD",
	"GEOSITE", "GEOIP", "SRC-GEOIP", "IP-ASN", "SRC-IP-ASN", "IP-CIDR", "IP-CIDR6", "SRC-IP-CIDR",
	"IP-SUFFIX", "SRC-IP-SUFFIX", "SRC-PORT", "DST-PORT", "IN-PORT", "DSCP", "IN-USER", "IN-NAME",
	"IN-TYPE", "PROCESS-NAME", "PROCESS-PATH", "PROCESS-NAME-REGEX", "PROCESS-PATH-REGEX",
	"PROCESS-NAME-WILDCARD", "PROCESS-PATH-WILDCARD", "REMATCH-NAME", "RULE-SET", "NETWORK", "UID",
	"SUB-RULE", "MATCH", "AND", "OR", "NOT"}

func kind(value string) string {
	value = strings.ToUpper(strings.ReplaceAll(value, "-", ""))
	switch value {
	case "IPCIDR6":
		return "IPCIDR"
	case "SUBRULES":
		return "SUBRULE"
	}
	return value
}

func canonical(rule Rule) Rule {
	rule.Type = kind(rule.Type)
	rule.Size = 0
	if strings.HasPrefix(rule.Type, "DOMAIN") && rule.Type != "DOMAINREGEX" {
		rule.Payload = strings.ToLower(rule.Payload)
	}
	if rule.Type == "IPCIDR" || rule.Type == "SRCIPCIDR" {
		if p, err := netip.ParsePrefix(rule.Payload); err == nil {
			rule.Payload = p.Masked().String()
		}
	}
	if logical(rule.Type) {
		if normalized, err := logicKey(rule.Type, rule.Payload, 0); err == nil {
			rule.Payload = normalized
		}
	}
	if rule.Type == "NETWORK" {
		rule.Payload = strings.ToLower(rule.Payload)
	}
	rule.Extra.HitCount, rule.Extra.MissCount, rule.Extra.HitAt, rule.Extra.MissAt = 0, 0, "", ""
	return rule
}

func sameDefinitions(a, b []Rule) bool {
	return slices.EqualFunc(a, b, func(left, right Rule) bool { return canonical(left) == canonical(right) })
}

func splitRule(raw string) ([]string, error) {
	fields := splitFields(raw)
	if len(fields) < 2 || (kind(fields[0]) != "MATCH" && len(fields) < 3) {
		return nil, errors.New("规则字段不足")
	}
	return fields, nil
}

func parseRule(raw string, index int) (Rule, error) {
	fields, err := splitRule(raw)
	if err != nil {
		return Rule{}, err
	}
	r := Rule{Index: index, Type: fields[0], Proxy: fields[1], Size: -1}
	if kind(r.Type) != "MATCH" {
		r.Payload, r.Proxy = fields[1], fields[2]
	}
	if slices.Contains(fields[fieldCount(r.Type):], "src") {
		switch kind(r.Type) {
		case "IPCIDR", "IPASN", "GEOIP", "IPSUFFIX":
			r.Type = "SRC-" + r.Type
		}
	}
	return r, nil
}

func fieldCount(t string) int {
	if kind(t) == "MATCH" {
		return 2
	}
	return 3
}

func formatRule(desired Rule, previous *yaml.Node) (string, error) {
	inputType := strings.ToUpper(desired.Type)
	if !slices.Contains(typeNames, inputType) {
		i := slices.IndexFunc(typeNames, func(t string) bool { return kind(t) == kind(inputType) })
		if i < 0 {
			return "", errors.New("未知的规则类型")
		}
		inputType = typeNames[i]
	}
	if strings.ContainsAny(desired.Proxy, "\r\n,") || strings.TrimSpace(desired.Proxy) == "" ||
		strings.ContainsAny(desired.Payload, "\r\n") {
		return "", errors.New("规则字段格式无效")
	}
	fields := []string{inputType, desired.Proxy}
	if inputType != "MATCH" {
		fields = []string{inputType, desired.Payload, desired.Proxy}
	}
	if previous != nil {
		old, err := splitRule(previous.Value)
		if err != nil {
			return "", err
		}
		if kind(old[0]) == kind(inputType) {
			fields[0] = old[0]
			if logical(inputType) && kind(inputType) != "MATCH" {
				oldRule, _ := parseRule(previous.Value, desired.Index)
				if canonical(oldRule).Payload == canonical(desired).Payload {
					fields[1] = old[1]
				}
			}
			fields = append(fields, old[fieldCount(old[0]):]...)
		}
	}
	raw := strings.Join(fields, ",")
	parsed, err := parseRule(raw, desired.Index)
	desired.Extra.Disabled = false
	if err != nil || !sameDefinitions([]Rule{parsed}, []Rule{desired}) {
		return "", errors.New("规则字段包含歧义，请检查匹配条件及策略")
	}
	return raw, nil
}

type config struct {
	doc      yaml.Node
	active   *yaml.Node
	rows     []*yaml.Node
	disabled map[int]bool
}

func readConfig(raw []byte) (*config, error) {
	var c config
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	if decoder.Decode(&c.doc) != nil || len(c.doc.Content) != 1 || c.doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("无法读取启动 YAML")
	}
	var rest yaml.Node
	if decoder.Decode(&rest) != io.EOF {
		return nil, errors.New("启动配置必须只有一个文档")
	}
	var disabled []disabledEntry
	root := c.doc.Content[0]
	for i := 0; i < len(root.Content); i += 2 {
		key, value := root.Content[i], root.Content[i+1]
		switch key.Value {
		case "rules":
			if c.active != nil || value.Kind != yaml.SequenceNode {
				return nil, errors.New("rules 必须是一个列表")
			}
			c.active = value
		case inactiveKey:
			if value.Decode(&disabled) != nil {
				return nil, errors.New("无法读取停用规则")
			}
		}
	}
	if c.active == nil {
		return nil, errors.New("启动配置没有 rules 列表")
	}
	c.rows = make([]*yaml.Node, len(c.active.Content)+len(disabled))
	c.disabled = make(map[int]bool, len(disabled))
	for _, entry := range disabled {
		if entry.Position < 0 || entry.Position >= len(c.rows) || c.disabled[entry.Position] {
			return nil, errors.New("停用规则位置无效")
		}
		c.disabled[entry.Position] = true
		c.rows[entry.Position] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: entry.Rule}
	}
	active := 0
	for i := range c.rows {
		if !c.disabled[i] {
			node := c.active.Content[active]
			if node.Kind != yaml.ScalarNode || node.Tag != "!!str" || node.Anchor != "" {
				return nil, errors.New("rules 条目必须为无锚点的字符串")
			}
			c.rows[i] = node
			active++
		}
	}
	return &c, nil
}

func (c *config) mergeRuntime(live []Rule) ([]Rule, error) {
	combined := make([]Rule, 0, len(c.rows))
	index := 0
	for i, node := range c.rows {
		rule, err := parseRule(node.Value, i)
		if err != nil {
			return nil, fmt.Errorf("启动规则 #%d 无效", i+1)
		}
		if c.disabled[i] {
			rule.Extra.Disabled = true
		} else {
			if index >= len(live) {
				return nil, errors.New("启动规则与运行态数量不一致")
			}
			current := live[index]
			if current.Index != index || current.Extra.Disabled {
				return nil, errors.New("运行态存在独立变更")
			}
			current.Index = i
			if !sameDefinitions([]Rule{rule}, []Rule{current}) {
				return nil, fmt.Errorf("启动规则与运行态在第 %d 条不一致", i+1)
			}
			rule = current
			index++
		}
		combined = append(combined, rule)
	}
	if index != len(live) {
		return nil, errors.New("启动规则与运行态数量不一致")
	}
	return combined, nil
}

func (c *config) candidate(baseline, draft []Rule) ([]byte, []Rule, error) {
	var active []*yaml.Node
	var disabled []disabledEntry
	expected := make([]Rule, 0, len(draft))
	seen := map[int]bool{}
	for i, rule := range draft {
		if rule.Index < 0 || seen[rule.Index] {
			return nil, nil, errors.New("草稿规则标识无效")
		}
		seen[rule.Index] = true
		var node *yaml.Node
		var previous *yaml.Node
		if rule.Index < len(c.rows) {
			previous = c.rows[rule.Index]
			current := baseline[rule.Index]
			current.Extra.Disabled = rule.Extra.Disabled
			if canonical(rule) == canonical(current) {
				node = previous
			}
		}
		if node == nil {
			raw, err := formatRule(rule, previous)
			if err != nil {
				return nil, nil, fmt.Errorf("草稿规则 #%d: %w", i+1, err)
			}
			node = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: raw}
			if previous != nil {
				node.HeadComment, node.LineComment, node.FootComment = previous.HeadComment, previous.LineComment, previous.FootComment
			}
		}
		if rule.Extra.Disabled {
			disabled = append(disabled, disabledEntry{Position: i, Rule: node.Value})
			continue
		}
		active = append(active, node)
		rule.Index = len(expected)
		expected = append(expected, rule)
	}
	c.active.Content = active
	root := c.doc.Content[0]
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == inactiveKey {
			root.Content = slices.Delete(root.Content, i, i+2)
			break
		}
	}
	if len(disabled) > 0 {
		var value yaml.Node
		if err := value.Encode(disabled); err != nil {
			return nil, nil, err
		}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: inactiveKey}, &value)
	}
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(&c.doc); err != nil {
		return nil, nil, err
	}
	return buffer.Bytes(), expected, nil
}
