package config

import (
	"fmt"
	"math/big"
	"regexp"
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
	path := ve.InstanceLocation
	switch k := ve.ErrorKind.(type) {
	case *kind.OneOf:
		names := branchNames(ve.SchemaURL, "oneOf")
		if present := presentKeys(ps.root, path, names); len(present) > 1 {
			ps.add(path, fmt.Sprintf("has %s; keep one", andList(present)))
		} else {
			ps.add(path, "needs one of "+orList(names))
		}
	case *kind.AnyOf:
		ps.add(path, "needs at least one of "+orList(branchNames(ve.SchemaURL, "anyOf")))
	case *kind.Not:
		not, _ := schemaAt(ve.SchemaURL)["not"].(map[string]any)
		ps.add(path, "can't have both "+andList(anyStrings(not["required"])))
	case *kind.PropertyNames:
		propertyNameProblem(ps, ve, k)
	case *kind.AdditionalProperties:
		allowed := schemaProperties(ve.SchemaURL)
		for _, p := range k.Properties {
			ps.addKey(append(slices.Clone(path), p), fmt.Sprintf("unknown key %q", p)+suggest(p, allowed))
		}
	default:
		if len(ve.Causes) > 0 {
			for _, c := range ve.Causes {
				schemaProblems(ps, c)
			}
			return
		}
		ps.add(path, leafMessage(ps.root, ve))
	}
}

// leafMessage words one rule failure with no causes of its own.
func leafMessage(root *yaml.Node, ve *jsonschema.ValidationError) string {
	path := ve.InstanceLocation
	switch k := ve.ErrorKind.(type) {
	case *kind.Required:
		return fmt.Sprintf("missing key %s", quoteList(k.Missing))
	case *kind.DependentRequired:
		return fmt.Sprintf("%q needs %s", k.Prop, quoteList(k.Missing))
	case *kind.FalseSchema:
		return falseSchemaMessage(root, path, ve.SchemaURL)
	case *kind.Type:
		want := yamlTypes(k.Want)
		if p, ok := schemaAt(ve.SchemaURL)["pattern"].(string); ok {
			want = describePattern(p)
		}
		return fmt.Sprintf("expected %s, got %s", want, yamlType(k.Got))
	case *kind.Enum:
		want := make([]string, len(k.Want))
		for i, w := range k.Want {
			want[i] = fmt.Sprint(w)
		}
		got := fmt.Sprint(k.Got)
		return fmt.Sprintf("%q is not one of %s", got, quoteList(want)) + suggest(got, want)
	case *kind.Const:
		return fmt.Sprintf("%q is not allowed here; the only text value is %q", fmt.Sprint(k.Got), fmt.Sprint(k.Want))
	case *kind.Pattern:
		return fmt.Sprintf("%q is not %s", k.Got, describePattern(k.Want))
	case *kind.Minimum:
		return "must be at least " + ratString(k.Want)
	case *kind.Maximum:
		return "must be at most " + ratString(k.Want)
	case *kind.UniqueItems:
		return "lists the same value twice"
	case *kind.MinItems, *kind.MinLength, *kind.MinProperties:
		return "must not be empty"
	default:
		return ve.ErrorKind.LocalizedString(printer)
	}
}

// falseSchemaMessage explains a key the schema forbids in this particular
// object: a field of another fault kind, or a bound that excludes another.
func falseSchemaMessage(root *yaml.Node, path []string, schemaURL string) string {
	key := path[len(path)-1]
	if m := dependentSchema.FindStringSubmatch(schemaURL); m != nil {
		return fmt.Sprintf("%q can't go with %q", key, m[1])
	}
	if parent := lookup(root, path[:len(path)-1], false); parent != nil {
		if f := child(parent, "fault"); f != nil {
			return fmt.Sprintf("%q is not a field of a %s fault", key, f.Value)
		}
	}
	return fmt.Sprintf("%q is not allowed here", key)
}

var dependentSchema = regexp.MustCompile(`/dependentSchemas/([^/]+)/`)

// addKey points propertyNames problems at the right key. jsonschema v6.0.3
// stores these errors' InstanceLocation without copying the slice it was
// building (validator.go, kind.PropertyNames), so by the time we read it a
// later-validated sibling map has overwritten its tail; only its length and
// the SchemaURL are reliable. We find every map at that depth whose schema
// is that URL and which holds the key, and report each.
func propertyNameProblem(ps *problems, ve *jsonschema.ValidationError, k *kind.PropertyNames) {
	msg := fmt.Sprintf("%q is not a valid key here", k.Property)
	if p := findPattern(ve); p != nil {
		msg = fmt.Sprintf("%q is not %s", k.Property, describePattern(p.Want))
	}
	want := strings.TrimSuffix(fragment(ve.SchemaURL), "/propertyNames")
	var found bool
	for _, path := range mapsWithKey(ps.root, k.Property, len(ve.InstanceLocation)) {
		if schemaLocation(path) == want {
			ps.addKey(append(path, k.Property), msg)
			found = true
		}
	}
	if !found {
		ps.add(nil, msg)
	}
}

// mapsWithKey returns the paths of every map at the given depth that has
// the key, in file order.
func mapsWithKey(n *yaml.Node, key string, depth int) [][]string {
	if depth == 0 {
		if n.Kind == yaml.MappingNode && child(n, key) != nil {
			return [][]string{{}}
		}
		return nil
	}
	var out [][]string
	switch n.Kind {
	case yaml.MappingNode:
		for j := 0; j+1 < len(n.Content); j += 2 {
			for _, sub := range mapsWithKey(n.Content[j+1], key, depth-1) {
				out = append(out, append([]string{n.Content[j].Value}, sub...))
			}
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			for _, sub := range mapsWithKey(c, key, depth-1) {
				out = append(out, append([]string{strconv.Itoa(i)}, sub...))
			}
		}
	default:
	}
	return out
}

// schemaLocation walks kazu.schema.json along an instance path and returns
// the fragment of the schema that applies there, after following $ref the
// way the validator reports it.
func schemaLocation(path []string) string {
	loc := "#"
	node, _ := schemaDoc.(map[string]any)
	node, loc = deref(node, loc)
	for _, tok := range path {
		if props, ok := node["properties"].(map[string]any); ok {
			if sub, ok := props[tok].(map[string]any); ok {
				node, loc = deref(sub, loc+"/properties/"+tok)
				continue
			}
		}
		if sub, ok := node["additionalProperties"].(map[string]any); ok {
			node, loc = deref(sub, loc+"/additionalProperties")
			continue
		}
		if sub, ok := node["items"].(map[string]any); ok {
			node, loc = deref(sub, loc+"/items")
			continue
		}
		return ""
	}
	return loc
}

func deref(node map[string]any, loc string) (map[string]any, string) {
	for {
		ref, ok := node["$ref"].(string)
		if !ok {
			return node, loc
		}
		node, loc = schemaAt(ref), fragment(ref)
	}
}

func fragment(url string) string {
	if i := strings.Index(url, "#"); i >= 0 {
		return url[i:]
	}
	return "#"
}

var printer = message.NewPrinter(language.English)

// patternNames describes the schema's patterns in words.
var patternNames = map[string]string{
	"^([0-9]+(\\.[0-9]+)?(ms|s|m|h))+$":                                "a duration such as 300ms, 20s or 1m30s",
	"^[0-9]+(\\.[0-9]+)?%$":                                            "a percentage such as 20%",
	"^(100(\\.0+)?|[0-9]{1,2}(\\.[0-9]+)?)%$":                          "a percentage from 0% to 100%",
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

func ratString(r *big.Rat) string {
	if r.IsInt() {
		return r.Num().String()
	}
	f, _ := r.Float64()
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// branchNames returns the key each branch of a oneOf or anyOf requires.
func branchNames(url, keyword string) []string {
	branches, _ := schemaAt(url)[keyword].([]any)
	var names []string
	for _, b := range branches {
		m, _ := b.(map[string]any)
		names = append(names, anyStrings(m["required"])...)
	}
	return names
}

// presentKeys returns which of names the map at path has, in file order.
func presentKeys(root *yaml.Node, path []string, names []string) []string {
	n := lookup(root, path, false)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	var present []string
	for j := 0; j+1 < len(n.Content); j += 2 {
		if slices.Contains(names, n.Content[j].Value) {
			present = append(present, n.Content[j].Value)
		}
	}
	return present
}

func anyStrings(v any) []string {
	items, _ := v.([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, fmt.Sprint(it))
	}
	return out
}

func andList(items []string) string { return joinList(items, "and") }

func orList(items []string) string { return joinList(items, "or") }

func joinList(items []string, conj string) string {
	if len(items) <= 1 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " " + conj + " " + items[len(items)-1]
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
	node := schemaDoc
	for _, tok := range strings.Split(strings.TrimPrefix(fragment(url), "#/"), "/") {
		if tok == "" || tok == "#" {
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
