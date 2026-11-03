package render

import (
	"bytes"
	"encoding/json"
	"net/netip"
	"os"
	"slices"
	"testing"

	"github.com/sv222/portpin/internal/advise"
	"github.com/sv222/portpin/internal/model"
)

type schemaNode struct {
	Ref        string                 `json:"$ref"`
	Required   []string               `json:"required"`
	Properties map[string]*schemaNode `json:"properties"`
	Items      *schemaNode            `json:"items"`
	Enum       []any                  `json:"enum"`
	Defs       map[string]*schemaNode `json:"$defs"`
}

func checkSchema(t *testing.T, defs map[string]*schemaNode, s *schemaNode, v any, path string) {
	t.Helper()
	if s.Ref != "" {
		s = defs[s.Ref[len("#/$defs/"):]]
	}
	if s.Enum != nil && !slices.Contains(s.Enum, v) {
		t.Errorf("%s: %v is not in the documented enum %v", path, v, s.Enum)
	}
	switch v := v.(type) {
	case map[string]any:
		for _, r := range s.Required {
			if _, ok := v[r]; !ok {
				t.Errorf("%s: required field %q missing", path, r)
			}
		}
		for k, child := range v {
			p, ok := s.Properties[k]
			if !ok {
				t.Errorf("%s: field %q is not documented in docs/schema.json", path, k)
				continue
			}
			checkSchema(t, defs, p, child, path+"."+k)
		}
	case []any:
		for _, child := range v {
			checkSchema(t, defs, s.Items, child, path+"[]")
		}
	}
}

func TestJSONMatchesDocumentedSchema(t *testing.T) {
	raw, err := os.ReadFile("../../docs/schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema schemaNode
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}

	r := Report{
		Target: "8080",
		Bindings: []model.Binding{
			{
				Endpoint: netip.MustParseAddrPort("127.0.0.1:8080"),
				Protocol: model.TCP,
				State:    model.StateListen,
				Inode:    7,
				Proc:     &model.ProcMeta{PID: 1, PPID: 2, UID: 3, User: "u", Name: "n", Cmdline: []string{"n"}},
			},
			{Endpoint: netip.MustParseAddrPort("[::1]:8080"), Protocol: model.UDP, State: model.StateTimeWait},
		},
		Actions: []Action{{
			PID: 1, Outcome: "released", GracefulSent: true, HardKill: true, Group: []uint32{2, 1},
			Error: "e", Advisory: &advise.Advisory{Kind: "container-proxy", Lines: []string{"l"}},
		}, {PID: 2, Outcome: "dry-run"}},
		ExitCode: 4,
		Error:    "e",
	}
	var buf bytes.Buffer
	if err := JSON(&buf, r); err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	checkSchema(t, schema.Defs, &schema, doc, "$")
}
