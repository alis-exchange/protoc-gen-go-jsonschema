package plugintest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alis-exchange/protoc-gen-go-jsonschema/plugin"
)

// TestRecursiveSchemaWithMCP verifies the plugin's primary consumer path with
// real generated types: protoc-gen-go compiles the recursive proto (a
// self-referential nested message), the plugin generates its schemas, and a
// temp module then checks the hybrid inline/$defs shape, validates json.Marshal
// output against the schema, and registers the schemas with an MCP server via
// mcp.AddTool — which panics if Resolve fails.
func (s *IntegrationTestSuite) TestRecursiveSchemaWithMCP() {
	if testing.Short() {
		s.T().Skip("Skipping recursive MCP test in short mode")
	}
	requireProtoc(s.T())
	if _, err := exec.LookPath("protoc-gen-go"); err != nil {
		s.T().Skip("protoc-gen-go not found in PATH")
	}

	tmpDir := s.TempDir()
	protoFile := "recursive/v1/recursive.proto"

	// Real message types via protoc-gen-go, at recursive/v1 (source_relative).
	args := []string{
		"--go_out=" + tmpDir,
		"--go_opt=paths=source_relative",
	}
	for _, path := range protoPaths(s.workspaceRoot) {
		args = append(args, "--proto_path="+path)
	}
	args = append(args, protoFile)
	cmd := exec.Command("protoc", args...)
	output, err := cmd.CombinedOutput()
	s.Require().NoError(err, "protoc --go_out failed: %s\nArgs: %v", string(output), args)

	// Schema code from the plugin, into the same package directory.
	descPath := filepath.Join(tmpDir, "recursive.descriptor.pb")
	fds := buildDescriptorSet(s.T(), s.workspaceRoot, []string{protoFile}, descPath)
	p := createTestPlugin(s.T(), fds, []string{protoFile})
	s.Require().NoError(plugin.Generate(p, "test"), "Generate failed for recursive proto")

	pkgDir := filepath.Join(tmpDir, "recursive", "v1")
	for name, content := range getGeneratedContent(s.T(), p) {
		if !strings.HasSuffix(name, "_jsonschema.pb.go") {
			continue
		}
		outPath := filepath.Join(pkgDir, filepath.Base(name))
		s.Require().NoError(os.WriteFile(outPath, []byte(content), 0o644))
	}

	// The alis options module is declared explicitly to pin its version; the
	// generated .pb.go imports it (options.proto's go_package).
	goMod := `module testmcp

go 1.26

require (
	github.com/google/jsonschema-go v0.4.3
	github.com/modelcontextprotocol/go-sdk v1.6.1
	go.alis.build/common/alis/open/options v1.8.0
)
`
	s.Require().NoError(os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0o644))

	writeSchemaTestHelpers(s.T(), tmpDir, "rectest")

	testContent := `package rectest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	recursivePb "testmcp/recursive/v1"
)

// newRecursiveConfigRequest returns a GetRecursiveConfigRequest with nested
// RecursiveItem children, using names that satisfy the generated name pattern.
func newRecursiveConfigRequest() *recursivePb.GetRecursiveConfigRequest {
	description := "A recursive item for testing"
	return &recursivePb.GetRecursiveConfigRequest{
		Name: "my-config",
		RecursiveConfig: &recursivePb.RecursiveConfig{
			RecursiveItems: []*recursivePb.RecursiveConfig_RecursiveItem{
				{
					Name:        "Book",
					Description: &description,
					Children: []*recursivePb.RecursiveConfig_RecursiveItem{
						{
							Name:        "Chapter",
							Description: &description,
							Children:    []*recursivePb.RecursiveConfig_RecursiveItem{},
						},
					},
				},
			},
		},
	}
}

func Test_RecursiveConfig(t *testing.T) {
	req := newRecursiveConfigRequest()

	schema := req.JsonSchema()
	if schema == nil {
		t.Fatal("JsonSchema() returned nil")
	}

	// Only the cyclic message lives under $defs; the acyclic request and
	// config are expanded in place.
	const itemKey = "recursive.v1.RecursiveConfig.RecursiveItem"
	if len(schema.Defs) != 1 {
		t.Fatalf("expected exactly one $defs entry (%q); have: %v", itemKey, getDefKeys(schema.Defs))
	}
	itemDef := schema.Defs[itemKey]
	if itemDef == nil {
		t.Fatalf("expected $defs[%q]; have: %v", itemKey, getDefKeys(schema.Defs))
	}
	if schema.Ref != "" || schema.Type != "object" {
		t.Fatalf("root must be a real object schema, got ref=%q type=%q", schema.Ref, schema.Type)
	}
	config := schema.Properties["recursive_config"]
	if config == nil || config.Ref != "" || config.Type != "object" {
		t.Fatalf("recursive_config must be inlined, got %+v", config)
	}
	items := config.Properties["recursive_items"]
	if items == nil || items.Items == nil || items.Items.Ref != "#/$defs/"+itemKey {
		t.Fatalf("recursive_items must reference the cyclic RecursiveItem, got %+v", items)
	}
	if itemDef.Properties["name"].Pattern == "" {
		t.Error("RecursiveItem.name should have a pattern constraint")
	}
	childrenSchema := itemDef.Properties["children"]
	if childrenSchema == nil || childrenSchema.Items == nil || childrenSchema.Items.Ref == "" {
		t.Fatal("children array items should $ref RecursiveItem (self-reference)")
	}

	// Validate the full root schema — not an isolated sub-def (those lack $defs context).
	resolved, err := ValidateSchemaWithName("GetRecursiveConfigRequest", schema)
	if err != nil {
		t.Fatalf("Schema validation failed: %v", err)
	}

	// Standalone cyclic schema: a real object root (a copy of the definition)
	// with the definition itself under $defs.
	itemSchema := (&recursivePb.RecursiveConfig_RecursiveItem{}).JsonSchema()
	if _, err := ValidateSchemaWithName("RecursiveItem", itemSchema); err != nil {
		t.Fatalf("standalone RecursiveItem.JsonSchema() should resolve: %v", err)
	}
	if itemSchema.Ref != "" || itemSchema.Properties["children"] == nil || itemSchema.Defs[itemKey] == itemSchema {
		t.Fatal("standalone cyclic root must be a copied object schema with its definition under $defs")
	}

	jsonBytes, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	var marshaledMap map[string]any
	if err := json.Unmarshal(jsonBytes, &marshaledMap); err != nil {
		t.Fatalf("Failed to unmarshal marshaled JSON: %v", err)
	}
	if err := resolved.Validate(marshaledMap); err != nil {
		t.Fatalf("json.Marshal output failed schema validation: %v\njson: %s", err, string(jsonBytes))
	}

	// Invalid name (pattern violation) should fail validation.
	invalidMap := make(map[string]any)
	for k, v := range marshaledMap {
		invalidMap[k] = v
	}
	invalidMap["recursive_config"] = map[string]any{
		"recursive_items": []any{
			map[string]any{"name": "book"}, // lowercase — violates ^[A-Z]...
		},
	}
	if err := resolved.Validate(invalidMap); err == nil {
		t.Error("expected validation to reject name that violates pattern")
	}
}

// Test_MutualRecursion covers a cycle spanning two messages (TreeNode <->
// Branch) and a cycle through a map value: both messages live under $defs,
// every reference into the cycle is a $ref, and documents validate.
func Test_MutualRecursion(t *testing.T) {
	schema := (&recursivePb.TreeNode{}).JsonSchema()
	if schema == nil {
		t.Fatal("JsonSchema() returned nil")
	}
	resolved, err := ValidateSchemaWithName("TreeNode", schema)
	if err != nil {
		t.Fatalf("TreeNode schema validation failed: %v", err)
	}
	if len(schema.Defs) != 2 || schema.Defs["recursive.v1.TreeNode"] == nil || schema.Defs["recursive.v1.Branch"] == nil {
		t.Fatalf("expected $defs for TreeNode and Branch only, got %v", getDefKeys(schema.Defs))
	}
	if got := schema.Properties["branch"].Ref; got != "#/$defs/recursive.v1.Branch" {
		t.Errorf("branch ref = %q", got)
	}
	if got := schema.Properties["named_children"].AdditionalProperties.Ref; got != "#/$defs/recursive.v1.TreeNode" {
		t.Errorf("named_children value ref = %q", got)
	}
	if got := schema.Defs["recursive.v1.Branch"].Properties["nodes"].Items.Ref; got != "#/$defs/recursive.v1.TreeNode" {
		t.Errorf("Branch.nodes item ref = %q", got)
	}

	doc := map[string]any{
		"label": "root",
		"branch": map[string]any{
			"nodes": []any{
				map[string]any{"label": "child", "branch": map[string]any{"nodes": []any{}}},
			},
		},
		"named_children": map[string]any{
			"left": map[string]any{"label": "left", "branch": map[string]any{"nodes": []any{}}},
		},
	}
	if err := resolved.Validate(doc); err != nil {
		t.Fatalf("valid nested document rejected: %v", err)
	}
	bad := map[string]any{"label": "root", "branch": map[string]any{"nodes": []any{map[string]any{"label": 1}}}}
	if err := resolved.Validate(bad); err == nil {
		t.Error("expected validation to reject a bad node two levels deep")
	}
}

// TestJsonSchemaResolveWithMCP exercises both schema shapes on the MCP path:
// the wire schema (tools/list) and server-side resolution (AddTool/tools/call).
func TestJsonSchemaResolveWithMCP(t *testing.T) {
	inputSchema := (&recursivePb.GetRecursiveConfigRequest{}).JsonSchema()
	if inputSchema == nil {
		t.Fatal("JsonSchema() returned nil")
	}

	// --- Wire schema: what MCP clients see on tools/list ---
	wireJSON, err := json.Marshal(inputSchema)
	if err != nil {
		t.Fatalf("marshal wire inputSchema: %v", err)
	}
	var wireMap map[string]any
	if err := json.Unmarshal(wireJSON, &wireMap); err != nil {
		t.Fatalf("unmarshal wire JSON: %v", err)
	}
	if wireMap["type"] != "object" {
		t.Errorf("wire schema type = %v, want object", wireMap["type"])
	}
	if wireMap["$ref"] != nil {
		t.Error("wire schema root must not be a $ref (MCP clients read root properties)")
	}
	if wireMap["properties"] == nil {
		t.Error("wire schema root must expose properties")
	}
	defs, ok := wireMap["$defs"].(map[string]any)
	if !ok || len(defs) != 1 {
		t.Fatalf("wire schema $defs count = %d, want 1 (only the cyclic RecursiveItem)", len(defs))
	}

	// --- Server-side resolution: same call MCP setSchema makes at AddTool ---
	resolved, err := inputSchema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		t.Fatalf("inputSchema.Resolve failed (MCP AddTool panics here): %v", err)
	}

	// Validate through *Resolved — this is what MCP applySchema uses at tools/call.
	req := newRecursiveConfigRequest()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if err := resolved.Validate(&data); err != nil {
		t.Fatalf("Resolved.Validate failed on json.Marshal output: %v", err)
	}

	// MCP registration smoke test: AddTool must not panic when schemas come from JsonSchema().
	// OutputSchema must be explicit — jsonschema.ForType cannot infer recursive proto types.
	outputSchema := (&recursivePb.RecursiveConfig{}).JsonSchema()
	if _, err := outputSchema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true}); err != nil {
		t.Fatalf("outputSchema.Resolve failed: %v", err)
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	tool := &mcp.Tool{
		Name:         "get_recursive_config",
		Description:  "Get a recursive config",
		InputSchema:  inputSchema,
		OutputSchema: outputSchema,
	}
	mcp.AddTool(server, tool, func(_ context.Context, _ *mcp.CallToolRequest, input *recursivePb.GetRecursiveConfigRequest) (*mcp.CallToolResult, *recursivePb.RecursiveConfig, error) {
		return nil, input.RecursiveConfig, nil
	})
}
`
	s.Require().NoError(os.WriteFile(filepath.Join(tmpDir, "recursive_schema_test.go"), []byte(testContent), 0o644))

	cmd = exec.Command("go", "mod", "tidy")
	cmd.Dir = tmpDir
	output, err = cmd.CombinedOutput()
	s.Require().NoError(err, "go mod tidy failed: %s", string(output))

	cmd = exec.Command("go", "test", "-v", "-timeout", "120s", "./...")
	cmd.Dir = tmpDir
	output, err = cmd.CombinedOutput()
	s.T().Logf("Recursive MCP test output:\n%s", string(output))
	s.Require().NoError(err, "recursive MCP tests failed: %s", string(output))
}
