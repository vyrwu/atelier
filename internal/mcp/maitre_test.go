package mcp

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestMaitreToolList(t *testing.T) {
	for _, maitre := range []bool{false, true} {
		r, _ := dispatch(request{ID: json.RawMessage(`1`), Method: "tools/list"}, maitre)
		tools := r.Result.(map[string]interface{})["tools"].([]interface{})
		var names []string
		for _, tool := range tools {
			spec := tool.(map[string]interface{})
			names = append(names, spec["name"].(string))
			if spec["inputSchema"] == nil {
				t.Fatal("missing schema")
			}
		}
		want := []string{"register_pr", "create_worktree", "create_pr"}
		if maitre {
			want = []string{"list_spaces", "create_space", "rename_space", "retire_space", "restore_space", "delete_space", "purge_spaces", "swap_agent", "read_screen", "send_input", "wait_for_change", "focus_space"}
		}
		sort.Strings(names)
		sort.Strings(want)
		if !reflect.DeepEqual(names, want) {
			t.Fatal(names)
		}
	}
}

func TestMaitreToolRejectsBadArgs(t *testing.T) {
	for _, tc := range []struct{ name, args, want string }{
		{"send_input", `{"space":"x","keys":["F1"]}`, `unknown key "F1"`},
		{"rename_space", `{"space":"x"}`, "title is required"},
		{"create_space", `{}`, "intent is required"},
		{"purge_spaces", `{}`, "older_than is required"},
		{"wait_for_change", `{"timeout_seconds":-1}`, "timeout"},
		{"nope", `{}`, "unknown tool"},
	} {
		r := callMaitreTool(callEnvelope{Name: tc.name, Arguments: json.RawMessage(tc.args)})
		text := r["content"].([]map[string]interface{})[0]["text"].(string)
		if r["isError"] != true || !strings.Contains(text, tc.want) {
			t.Fatalf("%s: %v", tc.name, r)
		}
	}
}
