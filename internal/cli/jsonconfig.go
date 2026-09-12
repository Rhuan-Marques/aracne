package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/tailscale/hujson"
)

// THE HARNESS CONFIG FILES ARE NOT ARACNE'S, AND THEIR BYTES CARRY MEANING.
//
// `arac setup` and `arac disable` merge a handful of keys into files the operator writes by
// hand -- `.opencode/opencode.json`, `.mcp.json`, `.claude/settings.json`. Decoding one into a
// map[string]interface{} and re-encoding it destroyed two things the operator put there:
//
//   - KEY ORDER. OpenCode converts a permission object to rules in Object.entries order and
//     resolves them with findLast, so `{"git*":"allow","git push":"deny"}` denies `git push`
//     and the same two keys sorted the other way round allow it. encoding/json sorts map keys,
//     so `arac setup` turned that deny into an allow just by touching the file.
//   - COMMENTS. OpenCode reads opencode.json as JSONC, so a commented file is a valid file
//     there. encoding/json refused it and readJSONConfig exited 1 -- and since initOpenCode
//     runs before initClaudeCode, a bare `arac setup` then wrote no integration at all.
//
// So the file is read as JSONC and written back as a PATCH: every byte aracne did not have to
// change stays exactly where the operator put it, comments and order included.

// parseJSONConfig decodes a harness config file, tolerating the comments and trailing commas
// both harnesses accept. A file that is broken for other reasons is still an error.
func parseJSONConfig(data []byte) (map[string]interface{}, error) {
	config := make(map[string]interface{})
	if len(bytes.TrimSpace(data)) == 0 {
		return config, nil
	}
	// On a COPY: Standardize blanks the comments in place, and hujson.Parse's syntax tree
	// aliases the same bytes -- so standardizing the buffer a caller is still patching erased
	// the operator's comments from the file on the way back out.
	standard, err := hujson.Standardize(bytes.Clone(data))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(standard, &config); err != nil {
		return nil, err
	}
	return config, nil
}

// encodeJSONConfig renders config as the bytes to write, given the file's current contents.
//
// An existing file is PATCHED rather than re-encoded, so only the values aracne actually
// changed move. Anything the patcher cannot model -- a file that is not a JSON object, one that
// no longer parses -- falls back to a plain encoding, which is also what a file that does not
// exist yet gets: there is no order or comment there to preserve.
func encodeJSONConfig(original []byte, config map[string]interface{}) ([]byte, error) {
	if patched, ok := patchJSONConfig(original, config); ok {
		return patched, nil
	}
	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// patchJSONConfig re-renders original so that it encodes config, reporting whether it could.
func patchJSONConfig(original []byte, config map[string]interface{}) ([]byte, bool) {
	if len(bytes.TrimSpace(original)) == 0 {
		return nil, false
	}
	root, err := hujson.Parse(original)
	if err != nil {
		return nil, false
	}
	object, ok := root.Value.(*hujson.Object)
	if !ok {
		return nil, false
	}
	current, err := parseJSONConfig(original)
	if err != nil {
		return nil, false
	}
	if err := syncJSONObject(object, current, config, jsonConfigIndent(original), 1); err != nil {
		return nil, false
	}
	out := root.Pack()
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return out, true
}

// syncJSONObject edits object in place until it encodes want, given that it currently encodes
// current. Members are kept in their existing order; a member whose key is gone takes the
// comment attached to it with it, and a new one is appended at the end, which is the only place
// an insertion cannot change the meaning of a rule that was already there.
func syncJSONObject(object *hujson.Object, current, want map[string]interface{}, indent string, depth int) error {
	kept := object.Members[:0]
	seen := make(map[string]bool, len(object.Members))
	for _, member := range object.Members {
		name, ok := jsonMemberName(member)
		if !ok {
			return fmt.Errorf("object member with a non-string name")
		}
		newValue, present := want[name]
		// A duplicate key is legal JSON and the LAST one wins, so an earlier copy is dropped
		// rather than synced -- which is what re-encoding did to it too.
		if !present || seen[name] {
			continue
		}
		seen[name] = true
		if err := syncJSONMember(&member, current[name], newValue, indent, depth); err != nil {
			return err
		}
		kept = append(kept, member)
	}
	object.Members = kept

	added := make([]string, 0, len(want))
	for name := range want {
		if !seen[name] {
			added = append(added, name)
		}
	}
	// Sorted, because map iteration is not: two runs of `arac setup` on the same project must
	// produce the same file, or every re-render shows up as a diff.
	sort.Strings(added)
	if len(added) == 0 {
		return nil
	}
	// Whatever trails the last member -- a comment on its line -- belongs to that member, not
	// to the closing brace, so it is carried onto the front of the first appended member
	// rather than being packed after it. Only the indentation of the closing brace is reused.
	lead, closing := splitAtLastNewline(string(object.AfterExtra))
	if lead == "" {
		lead = "\n"
	}
	object.AfterExtra = hujson.Extra("\n" + closing)
	for _, name := range added {
		value, err := renderJSONValue(want[name], indent, depth)
		if err != nil {
			return err
		}
		object.Members = append(object.Members, hujson.ObjectMember{
			Name:  hujson.Value{BeforeExtra: hujson.Extra(lead + strings.Repeat(indent, depth)), Value: hujson.String(name)},
			Value: hujson.Value{BeforeExtra: hujson.Extra(" "), Value: value.Value},
		})
		lead = "\n"
	}
	return nil
}

// splitAtLastNewline cuts s after its last newline, returning "" and s when it has none.
func splitAtLastNewline(s string) (lead, rest string) {
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[:i+1], s[i+1:]
	}
	return "", s
}

// syncJSONMember updates one member's value. Two objects are walked into so that changing one
// key of a nested block leaves its siblings -- and their comments -- untouched; everything else,
// arrays included, is replaced whole.
func syncJSONMember(member *hujson.ObjectMember, current, want interface{}, indent string, depth int) error {
	if jsonEqual(current, want) {
		return nil
	}
	currentObject, currentIsObject := current.(map[string]interface{})
	wantObject, wantIsObject := want.(map[string]interface{})
	if nested, ok := member.Value.Value.(*hujson.Object); ok && currentIsObject && wantIsObject {
		return syncJSONObject(nested, currentObject, wantObject, indent, depth+1)
	}
	value, err := renderJSONValue(want, indent, depth)
	if err != nil {
		return err
	}
	member.Value.Value = value.Value
	return nil
}

// renderJSONValue encodes a value as it should read at depth levels of indentation, so an
// inserted block lines up with the ones around it instead of arriving as one long line.
func renderJSONValue(value interface{}, indent string, depth int) (hujson.Value, error) {
	encoded, err := json.MarshalIndent(value, strings.Repeat(indent, depth), indent)
	if err != nil {
		return hujson.Value{}, err
	}
	return hujson.Parse(encoded)
}

// jsonMemberName is the decoded key of an object member.
func jsonMemberName(member hujson.ObjectMember) (string, bool) {
	literal, ok := member.Name.Value.(hujson.Literal)
	if !ok || literal.Kind() != '"' {
		return "", false
	}
	return literal.String(), true
}

// jsonConfigIndent is the indentation the file already uses, so a key aracne adds is written the
// way its neighbours are. Two spaces when the file says nothing -- a one-line config, an empty
// object -- which is what the plain encoder uses.
func jsonConfigIndent(data []byte) string {
	for i := 0; i < len(data); i++ {
		if data[i] != '\n' {
			continue
		}
		end := i + 1
		for end < len(data) && (data[end] == ' ' || data[end] == '\t') {
			end++
		}
		if end > i+1 && end < len(data) && data[end] != '\n' && data[end] != '\r' {
			return string(data[i+1 : end])
		}
	}
	return "  "
}
