package config

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"gopkg.in/yaml.v3"
)

// schemaProblems turns the validator's error tree into one problem per
// failed rule, worded for someone editing YAML rather than JSON.
func schemaProblems(ps *problems, ve *jsonschema.ValidationError) {
	if _, ok := ve.ErrorKind.(*kind.PropertyNames); !ok && len(ve.Causes) > 0 {
		for _, c := range ve.Causes {
			schemaProblems(ps, c)
		}
		return
	}
	path := ve.InstanceLocation
	switch k := ve.ErrorKind.(type) {
	case *kind.AdditionalProperties:
		allowed := schemaProperties(ve.SchemaURL)
		for _, p := range k.Properties {
			msg := fmt.Sprintf("unknown key %q", p) + suggest(p, allowed)
			ps.addKey(append(slices.Clone(path), p), msg)
		}
	case *kind.Required:
		for _, m := range k.Missing {
			ps.add(path, fmt.Sprintf("missing key %q", m))
		}
	case *kind.Type:
		want := yamlTypes(k.Want)
		if p, ok := schemaAt(ve.SchemaURL)["pattern"].(string); ok {
			want = describePattern(p)
		}
		ps.add(path, fmt.Sprintf("expected %s, got %s", want, yamlType(k.Got)))
	case *kind.Enum:
		want := make([]string, len(k.Want))
		for i, w := range k.Want {
			want[i] = fmt.Sprint(w)
		}
		got := fmt.Sprint(k.Got)
		ps.add(path, fmt.Sprintf("%q is not one of %s", got, quoteList(want))+suggest(got, want))
	case *kind.Const:
		ps.add(path, fmt.Sprintf("%q is not allowed here; the only text value is %q", fmt.Sprint(k.Got), fmt.Sprint(k.Want)))
	case *kind.Pattern:
		ps.add(path, fmt.Sprintf("%q is not %s", k.Got, describePattern(k.Want)))
	case *kind.PropertyNames:
		// The validator (v6.0.3) can report these at a sibling key's
		// location, so find the key in the file instead.
		msg := fmt.Sprintf("%q is not a valid key here", k.Property)
		if p := findPattern(ve); p != nil {
			msg = fmt.Sprintf("%q is not %s", k.Property, describePattern(p.Want))
		}
		ps.addKey(findKey(ps.root, k.Property), msg)
		return
	case *kind.UniqueItems:
		ps.add(path, "lists the same value twice")
	case *kind.MinItems:
		ps.add(path, "must not be empty")
	case *kind.MinLength:
		ps.add(path, "must not be empty")
	case *kind.MinProperties:
		ps.add(path, "must not be empty")
	default:
		ps.add(path, ve.ErrorKind.LocalizedString(printer))
	}
}

var printer = message.NewPrinter(language.English)

// patternNames describes the schema's patterns in words.
var patternNames = map[string]string{
	"^([0-9]+(\\.[0-9]+)?(ms|s|m|h))+$":                                "a duration such as 300ms, 20s or 1m30s",
	"^[0-9]+(\\.[0-9]+)?%$":                                            "a percentage such as 20%",
	"^[0-9]+(\\.[0-9]+)?pp$":                                           "percentage points such as 0.5pp",
	"^[0-9]+(\\.[0-9]+)?(bps|kbps|mbps|gbps)$":                         "a bandwidth such as 64kbps",
	"^[A-Za-z0-9][A-Za-z0-9_.-]*$":                                     "a name (letters, digits, '.', '_' and '-')",
	"^ *[A-Za-z0-9][A-Za-z0-9_.-]* *-> *[A-Za-z0-9][A-Za-z0-9_.-]* *$": `an edge written as "caller -> callee"`,
	"^[A-Za-z_][A-Za-z0-9_]*$":                                         "an env var name",
	"^[A-Za-z_][A-Za-z0-9_]*=":                                         "an env var written as NAME=value",
	"^/":                                                               "a path starting with /",
}

func describePattern(p string) string {
	if d, ok := patternNames[p]; ok {
		return d
	}
	return "in the form " + strconv.Quote(p)
}

func yamlType(t string) string {
	switch t {
	case "object":
		return "a map"
	case "array":
		return "a list"
	case "string":
		return "text"
	case "integer":
		return "a whole number"
	case "number":
		return "a number"
	case "boolean":
		return "true or false"
	case "null":
		return "nothing"
	}
	return t
}

func yamlTypes(ts []string) string {
	words := make([]string, len(ts))
	for i, t := range ts {
		words[i] = yamlType(t)
	}
	return strings.Join(words, " or ")
}

// schemaProperties returns the keys the schema at url allows, for
// suggestions on unknown keys.
func schemaProperties(url string) []string {
	props, _ := schemaAt(url)["properties"].(map[string]any)
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// schemaAt returns the raw schema object at url, a location in kazu.schema.json.
func schemaAt(url string) map[string]any {
	_, frag, _ := strings.Cut(url, "#")
	node := schemaDoc
	for _, tok := range strings.Split(strings.TrimPrefix(frag, "/"), "/") {
		if tok == "" {
			continue
		}
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		m, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		node = m[tok]
	}
	m, _ := node.(map[string]any)
	return m
}

// findKey returns the path to the first map key named name, in file order.
func findKey(n *yaml.Node, name string) []string {
	switch n.Kind {
	case yaml.MappingNode:
		for j := 0; j+1 < len(n.Content); j += 2 {
			k := n.Content[j].Value
			if k == name {
				return []string{k}
			}
			if sub := findKey(n.Content[j+1], name); sub != nil {
				return append([]string{k}, sub...)
			}
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			if sub := findKey(c, name); sub != nil {
				return append([]string{strconv.Itoa(i)}, sub...)
			}
		}
	default:
	}
	return nil
}

// findPattern returns the first pattern error under ve.
func findPattern(ve *jsonschema.ValidationError) *kind.Pattern {
	if p, ok := ve.ErrorKind.(*kind.Pattern); ok {
		return p
	}
	for _, c := range ve.Causes {
		if p := findPattern(c); p != nil {
			return p
		}
	}
	return nil
}
