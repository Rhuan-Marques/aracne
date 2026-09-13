package helper

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// UnknownConfigKeys reports keys present in a raw .aracne/config.json that no field of Config
// claims, as dotted paths ("read.max_file_szie").
//
// WHY THIS EXISTS. A bad config VALUE has always been caught loudly -- `"mode": "not_a_mode"`
// stops `arac setup` and names the four legal spellings. A bad config KEY was not caught at
// all: a project could write `read.max_file_szie`, get the default 512 KB, and never be told
// which of the two was in force. Every decision aracne makes is in this file, so a setting
// that silently does nothing is the most expensive kind of typo -- it looks applied.
//
// Deliberately a WARNING and not an error. Forward compatibility runs the other way too: an
// older binary reading a config written by a newer one should keep working, and refusing to
// start over a key it has not learned about yet would be worse than saying so.
func UnknownConfigKeys(raw []byte) []string {
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		// Not our error to report: the loader already fails, loudly, on unparseable JSON.
		return nil
	}
	var found []string
	collectUnknown(decoded, reflect.TypeOf(Config{}), "", &found)
	sort.Strings(found)
	return found
}

// collectUnknown walks the decoded JSON beside the struct that is supposed to describe it.
//
// It descends ONLY through struct fields. A map field (chat.agents, keyed by agent name) or a
// slice holds caller-chosen keys, so there is nothing there to call unknown -- descending
// would report every agent someone named as a typo.
func collectUnknown(node map[string]any, t reflect.Type, prefix string, found *[]string) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || hasCustomJSON(t) {
		return
	}
	fields := jsonFields(t)
	for key, val := range node {
		ft, ok := fields[key]
		if !ok {
			*found = append(*found, prefix+key)
			continue
		}
		child, isObject := val.(map[string]any)
		if !isObject {
			continue
		}
		collectUnknown(child, ft, prefix+key+".", found)
	}
}

// hasCustomJSON reports whether a type does its own encoding, in which case its struct tags do
// NOT describe the JSON it produces and nothing inside it can be judged from them.
//
// VizChatAgents is the case that matters: it marshals a bare "model" string flat alongside a
// map of caller-named agents, so reflection over its two Go fields sees every agent name in a
// real config -- bug-hunter, explorer, the default set -- as an unknown key. The first version
// of this check warned about aracne's own default config.
func hasCustomJSON(t reflect.Type) bool {
	marshaler := reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	unmarshaler := reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
	ptr := reflect.PointerTo(t)
	return t.Implements(marshaler) || t.Implements(unmarshaler) ||
		ptr.Implements(marshaler) || ptr.Implements(unmarshaler)
}

// jsonFields maps a struct's JSON names to their types, following embedded structs so an
// inlined section's keys are not reported as unknown.
func jsonFields(t reflect.Type) map[string]reflect.Type {
	out := make(map[string]reflect.Type)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "-" {
			continue
		}
		if f.Anonymous && name == "" {
			for k, v := range jsonFields(f.Type) {
				out[k] = v
			}
			continue
		}
		if name == "" {
			name = f.Name
		}
		out[name] = f.Type
	}
	return out
}

// ConfigKeyWarning is the line to print for a config carrying keys nothing reads, or "" when
// there are none. Shared so `arac init` and `arac setup` word it identically.
func ConfigKeyWarning(raw []byte) string {
	unknown := UnknownConfigKeys(raw)
	if len(unknown) == 0 {
		return ""
	}
	noun := "key"
	if len(unknown) > 1 {
		noun = "keys"
	}
	return fmt.Sprintf(
		"warning: .aracne/config.json has %d %s nothing reads: %s. Check the spelling against "+
			"docs/configuration.md — a setting aracne does not recognise has no effect.",
		len(unknown), noun, strings.Join(unknown, ", "))
}
