package plugintest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alis-exchange/protoc-gen-go-jsonschema/plugin"
)

// TestProto2GroupsSemantics verifies proto2 groups end-to-end: a group field
// renders like a nested message reference (inline, or $ref when it sits on a
// cycle), and the generated schemas resolve and validate documents shaped the
// way encoding/json marshals protoc-gen-go's group types.
func (s *IntegrationTestSuite) TestProto2GroupsSemantics() {
	if testing.Short() {
		s.T().Skip("Skipping proto2 groups semantics test in short mode")
	}
	requireProtoc(s.T())

	legacyProto := "legacy/v1/legacy.proto"
	tmpDir := s.TempDir()
	descPath := filepath.Join(tmpDir, "legacy.pb")
	fds := buildDescriptorSet(s.T(), s.workspaceRoot, []string{legacyProto}, descPath)

	p := createTestPlugin(s.T(), fds, []string{legacyProto})
	s.Require().NoError(plugin.Generate(p, "test"), "Generate failed for legacy proto")

	pkgDir := filepath.Join(tmpDir, "legacyv1")
	s.Require().NoError(os.MkdirAll(pkgDir, 0o755))
	for name, content := range getGeneratedContent(s.T(), p) {
		if !strings.HasSuffix(name, "_jsonschema.pb.go") {
			continue
		}
		s.Require().NoError(os.WriteFile(filepath.Join(pkgDir, filepath.Base(name)), []byte(content), 0o644))
	}

	goMod := `module testlegacy

go 1.26

require github.com/google/jsonschema-go v0.4.3
`
	s.Require().NoError(os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0o644))

	// protoc-gen-go names a group's type Parent_Group, like a nested message.
	stubTypes := `package legacyv1

type Record struct{}
type Record_Attributes struct{}
type Record_Tags struct{}
type Node struct{}
type Node_Children struct{}
`
	s.Require().NoError(os.WriteFile(filepath.Join(pkgDir, "stub_types.go"), []byte(stubTypes), 0o644))

	writeSchemaTestHelpers(s.T(), pkgDir, "legacyv1")

	testContent := `package legacyv1

import "testing"

func TestGroupsRenderAsNestedMessages(t *testing.T) {
	schema := (&Record{}).JsonSchema()
	resolved, err := ValidateSchemaWithName("Record", schema)
	if err != nil {
		t.Fatalf("Record schema validation failed: %v", err)
	}
	if schema.Defs != nil {
		t.Errorf("acyclic groups must inline, got $defs %v", getDefKeys(schema.Defs))
	}
	attrs := schema.Properties["attributes"]
	if attrs == nil || attrs.Ref != "" || attrs.Type != "object" || attrs.Properties["key"] == nil || attrs.Properties["value"] == nil {
		t.Fatalf("a singular group must be an inline object with the group's fields, got %+v", attrs)
	}
	tags := schema.Properties["tags"]
	if tags == nil || tags.Type != "array" || tags.Items == nil || tags.Items.Ref != "" || tags.Items.Properties["name"] == nil {
		t.Fatalf("a repeated group must be an array of inline objects, got %+v", tags)
	}

	// The shape encoding/json produces for the protoc-gen-go group types.
	doc := map[string]any{
		"id":         "r1",
		"attributes": map[string]any{"key": "k", "value": 1},
		"tags":       []any{map[string]any{"name": "t"}},
	}
	if err := resolved.Validate(doc); err != nil {
		t.Fatalf("valid document rejected: %v", err)
	}
	if err := resolved.Validate(map[string]any{"attributes": map[string]any{"value": "not a number"}}); err == nil {
		t.Error("expected validation to reject a wrongly typed group field")
	}
}

func TestGroupOnCycleUsesDefs(t *testing.T) {
	schema := (&Node{}).JsonSchema()
	resolved, err := ValidateSchemaWithName("Node", schema)
	if err != nil {
		t.Fatalf("Node schema validation failed: %v", err)
	}
	const nodeKey, childrenKey = "legacy.v1.Node", "legacy.v1.Node.Children"
	if len(schema.Defs) != 2 || schema.Defs[nodeKey] == nil || schema.Defs[childrenKey] == nil {
		t.Fatalf("expected $defs for Node and Node.Children, got %v", getDefKeys(schema.Defs))
	}
	if got := schema.Properties["children"].Ref; got != "#/$defs/"+childrenKey {
		t.Errorf("children ref = %q", got)
	}
	if got := schema.Defs[childrenKey].Properties["node"].Items.Ref; got != "#/$defs/"+nodeKey {
		t.Errorf("Children.node item ref = %q", got)
	}
	doc := map[string]any{
		"label":    "root",
		"children": map[string]any{"node": []any{map[string]any{"label": "leaf"}}},
	}
	if err := resolved.Validate(doc); err != nil {
		t.Fatalf("valid recursive document rejected: %v", err)
	}
}
`
	s.Require().NoError(os.WriteFile(filepath.Join(pkgDir, "legacy_test.go"), []byte(testContent), 0o644))

	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	s.Require().NoError(err, "go mod tidy failed: %s", string(output))

	cmd = exec.Command("go", "test", "-v", "-timeout", "60s", "./...")
	cmd.Dir = tmpDir
	output, err = cmd.CombinedOutput()
	s.T().Logf("Proto2 groups semantics output:\n%s", string(output))
	s.Require().NoError(err, "proto2 groups semantics tests failed: %s", string(output))
}
