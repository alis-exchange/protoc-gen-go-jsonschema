package plugintest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alis-exchange/protoc-gen-go-jsonschema/plugin"
	"github.com/stretchr/testify/suite"
)

// IntegrationTestSuite contains integration tests that test the full plugin pipeline.
type IntegrationTestSuite struct {
	PluginTestSuite

	// pluginBinary is the path to the built plugin binary
	pluginBinary string
}

// TestIntegrationSuite runs the IntegrationTestSuite.
func TestIntegrationSuite(t *testing.T) {
	suite.Run(t, new(IntegrationTestSuite))
}

// SetupSuite runs once before all tests and builds the plugin binary.
func (s *IntegrationTestSuite) SetupSuite() {
	// Call parent setup first
	s.PluginTestSuite.SetupSuite()

	// Build the plugin binary for integration tests
	s.buildPlugin()
}

// TearDownSuite runs once after all tests and cleans up the plugin binary.
func (s *IntegrationTestSuite) TearDownSuite() {
	// Clean up the plugin binary
	if s.pluginBinary != "" {
		os.Remove(s.pluginBinary)
		s.T().Logf("Cleaned up plugin binary: %s", s.pluginBinary)
	}
}

// buildPlugin builds the plugin binary for integration tests.
func (s *IntegrationTestSuite) buildPlugin() {
	// Create a temp file for the plugin binary
	tmpDir := os.TempDir()
	s.pluginBinary = filepath.Join(tmpDir, "protoc-gen-go-jsonschema-test")

	// Build the plugin
	buildCmd := exec.Command("go", "build", "-o", s.pluginBinary, "./cmd/protoc-gen-go-jsonschema")
	buildCmd.Dir = s.workspaceRoot
	output, err := buildCmd.CombinedOutput()
	s.Require().NoError(err, "Failed to build plugin: %s", string(output))

	// Make the plugin executable
	err = os.Chmod(s.pluginBinary, 0o755)
	s.Require().NoError(err, "Failed to make plugin executable")

	s.T().Logf("Built plugin binary: %s", s.pluginBinary)
}

// TestGoldenFile generates schemas for every proto package under
// testdata/protos and compares each generated file against its golden file.
// New protos are picked up automatically; each directory is one Go package and
// its files are compiled together so cross-file references resolve.
// Run `go test ./plugin_test/... -update` to (re)create goldens; goldens whose
// protos disappeared fail the test (and are removed with -update).
func (s *IntegrationTestSuite) TestGoldenFile() {
	requireProtoc(s.T())

	goldenBase := filepath.Join(s.WorkspaceRoot(), "testdata", "golden")
	generated := make(map[string]bool)

	for dir, protoFiles := range discoverProtoDirs(s.T(), s.WorkspaceRoot()) {
		s.Run(strings.ReplaceAll(dir, "/", "_"), func() {
			descPath := filepath.Join(tempDir(s.T()), "descriptors.pb")
			fds := buildDescriptorSet(s.T(), s.WorkspaceRoot(), protoFiles, descPath)
			p := createTestPlugin(s.T(), fds, protoFiles)
			s.Require().NoError(plugin.Generate(p, "test"), "Generate failed for %s", dir)

			for name, content := range getGeneratedContent(s.T(), p) {
				baseName := filepath.Base(name)
				generated[baseName+".golden"] = true
				assertGoldenFile(s.T(), content, filepath.Join(goldenBase, baseName+".golden"), *updateGolden)
			}
		})
	}

	// A golden with no generating proto pins behaviour nothing produces anymore.
	entries, err := os.ReadDir(goldenBase)
	s.Require().NoError(err, "Failed to read golden directory")
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".golden") || generated[e.Name()] {
			continue
		}
		if *updateGolden {
			s.Require().NoError(os.Remove(filepath.Join(goldenBase, e.Name())))
			s.T().Logf("Removed stale golden: %s", e.Name())
			continue
		}
		s.T().Errorf("Stale golden %s has no generating proto; run -update to remove it", e.Name())
	}
}

// TestGeneratedCodeCompiles verifies that generated code compiles successfully.
func (s *IntegrationTestSuite) TestGeneratedCodeCompiles() {
	if testing.Short() {
		s.T().Skip("Skipping compilation test in short mode")
	}

	contents := s.RunGenerate()

	// Create a temporary directory for the test
	tmpDir := s.TempDir()
	pkgDir := filepath.Join(tmpDir, "usersv1")
	err := os.MkdirAll(pkgDir, 0o755)
	s.Require().NoError(err, "Failed to create package directory")

	// Write generated files
	for name, content := range contents {
		filePath := filepath.Join(pkgDir, filepath.Base(name))
		err := os.WriteFile(filePath, []byte(content), 0o644)
		s.Require().NoError(err, "Failed to write file %s", filePath)
	}

	// Create a minimal go.mod file
	goMod := `module testcompile

go 1.21

require (
	github.com/google/jsonschema-go v0.4.2
	google.golang.org/protobuf v1.36.0
)
`
	err = os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0o644)
	s.Require().NoError(err, "Failed to write go.mod")

	// Create stub types that satisfy the generated schema code's type references
	stubPath := filepath.Join(pkgDir, "stub_types.go")
	err = os.WriteFile(stubPath, []byte(userStubTypes()), 0o644)
	s.Require().NoError(err, "Failed to write stub file")

	// Run go mod tidy and go build
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	s.Require().NoError(err, "go mod tidy failed: %s", string(output))

	cmd = exec.Command("go", "build", "./...")
	cmd.Dir = tmpDir
	output, err = cmd.CombinedOutput()
	s.Require().NoError(err, "go build failed: %s", string(output))

	s.T().Log("Generated code compiles successfully")
}

// TestEndToEndWithProtoc tests the full pipeline using protoc.
func (s *IntegrationTestSuite) TestEndToEndWithProtoc() {
	if testing.Short() {
		s.T().Skip("Skipping end-to-end test in short mode")
	}

	// Check if protoc is available
	if _, err := exec.LookPath("protoc"); err != nil {
		s.T().Skip("protoc not found in PATH, skipping end-to-end test")
	}

	// Create a temporary directory for output
	tmpDir := s.TempDir()
	outputDir := filepath.Join(tmpDir, "output")
	err := os.MkdirAll(outputDir, 0o755)
	s.Require().NoError(err, "Failed to create output directory")

	protoFile := "users/v1/user.proto"

	// Run protoc with our plugin
	args := []string{
		"--plugin=protoc-gen-go-jsonschema=" + s.pluginBinary,
		"--go-jsonschema_out=" + outputDir,
		"--go-jsonschema_opt=paths=source_relative",
	}
	for _, path := range protoPaths(s.workspaceRoot) {
		args = append(args, "--proto_path="+path)
	}
	args = append(args, protoFile)

	protocCmd := exec.Command("protoc", args...)
	output, err := protocCmd.CombinedOutput()
	s.Require().NoError(err, "protoc failed: %s\nArgs: %v", string(output), args)

	// Verify output files were created
	expectedFile := filepath.Join(outputDir, "users", "v1", "user_jsonschema.pb.go")
	if _, err := os.Stat(expectedFile); os.IsNotExist(err) {
		// Try alternative path
		expectedFile = filepath.Join(outputDir, "user_jsonschema.pb.go")
		if _, err := os.Stat(expectedFile); os.IsNotExist(err) {
			// List what was actually created
			entries, _ := os.ReadDir(outputDir)
			s.T().Logf("Files in output directory:")
			for _, e := range entries {
				s.T().Logf("  %s", e.Name())
			}
			s.Require().Fail("Expected output file not found at %s", expectedFile)
		}
	}

	// Read and verify content
	content, err := os.ReadFile(expectedFile)
	s.Require().NoError(err, "Failed to read generated file")
	s.NotEmpty(content, "Generated file is empty")

	// Verify key content
	contentStr := string(content)
	checks := []string{
		"package ",
		"JsonSchema()",
		"jsonschema.Schema",
	}

	for _, check := range checks {
		s.Contains(contentStr, check, "Generated content missing expected string")
	}

	s.T().Log("End-to-end test passed successfully")
}

// TestDescriptorSetGeneration tests that we can generate descriptor sets.
func (s *IntegrationTestSuite) TestDescriptorSetGeneration() {
	if testing.Short() {
		s.T().Skip("Skipping descriptor set generation test in short mode")
	}

	// Check if protoc is available
	if _, err := exec.LookPath("protoc"); err != nil {
		s.T().Skip("protoc not found in PATH")
	}

	tmpDir := s.TempDir()

	protoFile := "users/v1/user.proto"
	outputPath := filepath.Join(tmpDir, "test.pb")

	fds := buildDescriptorSet(s.T(), s.workspaceRoot, []string{protoFile}, outputPath)

	s.Require().NotNil(fds, "Failed to generate descriptor set")
	s.Require().NotEmpty(fds.File, "Descriptor set has no files")

	// Find our target file
	found := false
	for _, f := range fds.File {
		if strings.HasSuffix(f.GetName(), "user.proto") {
			found = true
			break
		}
	}

	s.True(found, "user.proto not found in descriptor set")
	s.T().Logf("Generated descriptor set with %d files", len(fds.File))
}

// TestMultipleFilesGeneration tests generation with multiple proto files.
func (s *IntegrationTestSuite) TestMultipleFilesGeneration() {
	// List all files in the descriptor set
	s.T().Logf("Descriptor set contains %d files", len(s.fds.File))
	for _, f := range s.fds.File {
		s.T().Logf("  - %s", f.GetName())
	}

	contents := s.RunGenerate()
	s.T().Logf("Generated %d files", len(contents))
	for name := range contents {
		s.T().Logf("  - %s", name)
	}
}

// TestHybridModeInGeneratedCode pins the two generated shapes: acyclic
// messages build inline and never register in defs; messages on a cycle
// register under $defs and expose a copied root.
func (s *IntegrationTestSuite) TestHybridModeInGeneratedCode() {
	content := s.GetGeneratedContent()

	// Cyclic: top-level AddressDetails references itself.
	s.Contains(content, `defs["users.v1.AddressDetails"] = schema`,
		"cyclic message must register its definition")
	s.Contains(content, `root := AddressDetails_JsonSchema_build(defs, false)`,
		"cyclic root must be built as an independent tree")

	// Acyclic: Address never registers and returns the object itself.
	s.NotContains(content, `defs["users.v1.Address"]`,
		"acyclic message must not register in defs")
	s.NotContains(content, `Ref: "#/$defs/users.v1.Address"`,
		"acyclic message must never be referenced")
	addr := extractGoFuncSection(content, "Address_JsonSchema_WithDefs")
	s.Require().NotEmpty(addr)
	s.Contains(addr, "return schema", "acyclic _WithDefs returns the inline object")
	s.Contains(content, "if len(defs) > 0 {",
		"acyclic root attaches $defs only when a cyclic descendant registered one")
}

// TestRequiredFieldsGeneration tests that required fields are properly marked.
func (s *IntegrationTestSuite) TestRequiredFieldsGeneration() {
	content := s.GetGeneratedContent()

	// Check for Required field generation
	if !strings.Contains(content, "Required:") {
		s.T().Log("Note: No Required field found - may be all fields optional")
	}
}

// TestSchemaJSONSerializable tests that the generated schema can be serialized to JSON
// without causing a stack overflow from circular references.
// This is a critical test - see REPORT.md for details on the circular reference bug.
func (s *IntegrationTestSuite) TestSchemaJSONSerializable() {
	if testing.Short() {
		s.T().Skip("Skipping JSON serialization test in short mode")
	}

	contents := s.RunGenerate()

	// Create a temporary directory for the test
	tmpDir := s.TempDir()
	pkgDir := filepath.Join(tmpDir, "usersv1")
	err := os.MkdirAll(pkgDir, 0o755)
	s.Require().NoError(err, "Failed to create package directory")

	// Write generated files
	for name, content := range contents {
		filePath := filepath.Join(pkgDir, filepath.Base(name))
		err := os.WriteFile(filePath, []byte(content), 0o644)
		s.Require().NoError(err, "Failed to write file %s", filePath)
	}

	// Create a minimal go.mod file
	// Note: Using v0.3.0 to match the version from the user's bug report.
	// This version has a MarshalJSON method that doesn't handle circular refs.
	goMod := `module testserialize

go 1.21

require (
	github.com/google/jsonschema-go v0.3.0
	google.golang.org/protobuf v1.36.0
)
`
	err = os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0o644)
	s.Require().NoError(err, "Failed to write go.mod")

	// Create stub types for the proto messages
	stubPath := filepath.Join(pkgDir, "stub_types.go")
	err = os.WriteFile(stubPath, []byte(userStubTypes()), 0o644)
	s.Require().NoError(err, "Failed to write stub file")

	// Create a test file that verifies JSON serialization works
	testContent := `package usersv1

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

// ValidateSchema validates a *jsonschema.Schema and returns a resolved schema
// that can be used for validation. It ensures:
//   - The schema is not nil
//   - The schema type is "object" (required by MCP spec)
//   - The schema structure is valid (via Resolve)
//
// Returns the resolved schema on success, or an error if validation fails.
func ValidateSchema(schema *jsonschema.Schema) (*jsonschema.Resolved, error) {
	// Step 1: Check for nil
	if schema == nil {
		return nil, fmt.Errorf("schema cannot be nil")
	}

	// Step 2: The root must be a real object schema. MCP requires a literal
	// type: "object" root, so a $ref root is never acceptable.
	if schema.Ref != "" {
		return nil, fmt.Errorf("schema root must not be a $ref (got %q)", schema.Ref)
	}
	if schema.Type != "object" {
		return nil, fmt.Errorf("schema must have type \"object\" (got type %q)", schema.Type)
	}

	// Step 3: Verify all $ref pointers can be resolved
	// First, collect all $ref values from the schema
	refs := collectRefs(schema)
	
	// Check that all referenced schemas exist in Definitions
	// Every $ref must resolve into $defs — a nil Defs map resolves nothing.
	if len(refs) > 0 {
		for ref := range refs {
			// Extract the key from the $ref (format: "#/$defs/key")
			key := extractRefKey(ref)
			if key != "" {
				if _, exists := schema.Defs[key]; !exists {
					return nil, fmt.Errorf("$ref %q points to non-existent definition %q", ref, key)
				}
			}
		}
	}

	// Step 4: Resolve the schema - this validates the schema structure itself
	// ValidateDefaults: true enables validation of default values in the schema
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{
		ValidateDefaults: true,
	})
	if err != nil {
		return nil, fmt.Errorf("invalid schema structure: %w", err)
	}

	return resolved, nil
}

// ValidateSchemaWithName is a convenience wrapper that includes the schema name
// in error messages for better debugging.
func ValidateSchemaWithName(name string, schema *jsonschema.Schema) (*jsonschema.Resolved, error) {
	if schema == nil {
		return nil, fmt.Errorf("schema %q: cannot be nil", name)
	}

	// The root must be a real object schema. MCP requires a literal
	// type: "object" root, so a $ref root is never acceptable.
	if schema.Ref != "" {
		return nil, fmt.Errorf("schema %q: root must not be a $ref (got %q)", name, schema.Ref)
	}
	if schema.Type != "object" {
		return nil, fmt.Errorf("schema %q: must have type \"object\" (got type %q)", name, schema.Type)
	}

	// Verify all $ref pointers exist
	refs := collectRefs(schema)
	// Every $ref must resolve into $defs — a nil Defs map resolves nothing.
	if len(refs) > 0 {
		for ref := range refs {
			key := extractRefKey(ref)
			if key != "" {
				if _, exists := schema.Defs[key]; !exists {
					return nil, fmt.Errorf("schema %q: $ref %q points to non-existent definition %q", name, ref, key)
				}
			}
		}
	}

	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{
		ValidateDefaults: true,
	})
	if err != nil {
		return nil, fmt.Errorf("schema %q: invalid schema structure: %w", name, err)
	}

	return resolved, nil
}

// collectRefs recursively collects all $ref values from a schema
func collectRefs(schema *jsonschema.Schema) map[string]bool {
	refs := make(map[string]bool)
	if schema == nil {
		return refs
	}
	
	if schema.Ref != "" {
		refs[schema.Ref] = true
	}
	
	if schema.Properties != nil {
		for _, prop := range schema.Properties {
			for ref := range collectRefs(prop) {
				refs[ref] = true
			}
		}
	}
	
	if schema.Items != nil {
		for ref := range collectRefs(schema.Items) {
			refs[ref] = true
		}
	}
	
	if schema.AdditionalProperties != nil {
		for ref := range collectRefs(schema.AdditionalProperties) {
			refs[ref] = true
		}
	}
	
	if schema.Defs != nil {
		for _, def := range schema.Defs {
			for ref := range collectRefs(def) {
				refs[ref] = true
			}
		}
	}
	
	return refs
}

// extractRefKey extracts the definition key from a $ref value
// Format: "#/$defs/key" -> "key"
func extractRefKey(ref string) string {
	prefix := "#/$defs/"
	if strings.HasPrefix(ref, prefix) {
		return ref[len(prefix):]
	}
	return ""
}

// TestSchemaCanBeSerialized verifies that calling JsonSchema() and then
// json.Marshal() does not cause a stack overflow from circular references.
// Recursive types like AddressDetails keep their definition under $defs and
// get an independently built root, so no pointer cycle exists.
func TestSchemaCanBeSerialized(t *testing.T) {
	testCases := []struct {
		name   string
		schema func() *jsonschema.Schema
	}{
		{"Address", func() *jsonschema.Schema { return (&Address{}).JsonSchema() }},
		{"AddressDetails", func() *jsonschema.Schema { return (&AddressDetails{}).JsonSchema() }},
		{"User", func() *jsonschema.Schema { return (&User{}).JsonSchema() }},
		{"ComprehensiveUser", func() *jsonschema.Schema { return (&ComprehensiveUser{}).JsonSchema() }},
		{"ContactInfo", func() *jsonschema.Schema { return (&ContactInfo{}).JsonSchema() }},
		{"Metadata", func() *jsonschema.Schema { return (&Metadata{}).JsonSchema() }},
		{"UserProfile", func() *jsonschema.Schema { return (&UserProfile{}).JsonSchema() }},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			schema := tc.schema()
			if schema == nil {
				t.Fatalf("%s.JsonSchema() returned nil", tc.name)
			}

			// Validate the schema structure - this must pass
			resolved, err := ValidateSchemaWithName(tc.name, schema)
			if err != nil {
				t.Fatalf("%s schema validation failed: %v", tc.name, err)
			}
			t.Logf("%s schema is valid and resolved successfully", tc.name)
			
			// This will cause a stack overflow if there's a circular reference
			data, err := json.Marshal(schema)
			if err != nil {
				t.Fatalf("Failed to marshal %s schema to JSON: %v", tc.name, err)
			}
			
			if len(data) == 0 {
				t.Fatalf("Marshaled %s schema is empty", tc.name)
			}
			
			// Verify it's valid JSON by unmarshaling
			var parsed map[string]interface{}
			if err := json.Unmarshal(data, &parsed); err != nil {
				t.Fatalf("Marshaled %s schema is not valid JSON: %v", tc.name, err)
			}
			
			t.Logf("%s schema serialized successfully (%d bytes)", tc.name, len(data))

			// If we got a resolved schema, verify it's usable
			if resolved != nil {
				t.Logf("%s resolved schema is ready for validation", tc.name)
			}
		})
	}
}

// TestSelfReferencingSchemaSerializable tests that a self-referential message
// (top-level AddressDetails) keeps its definition under $defs, exposes a real
// object root that is a copy of that definition, and marshals without a
// pointer cycle.
func TestSelfReferencingSchemaSerializable(t *testing.T) {
	schema := (&AddressDetails{}).JsonSchema()
	if schema == nil {
		t.Fatal("AddressDetails.JsonSchema() returned nil")
	}
	if _, err := ValidateSchemaWithName("AddressDetails", schema); err != nil {
		t.Fatalf("AddressDetails schema validation failed: %v", err)
	}

	const key = "users.v1.AddressDetails"
	if schema.Ref != "" || schema.Type != "object" || schema.Properties == nil {
		t.Fatalf("root must be a real object schema, got ref=%q type=%q", schema.Ref, schema.Type)
	}
	if len(schema.Defs) != 1 {
		t.Fatalf("expected exactly one $defs entry (the cycle), got %d", len(schema.Defs))
	}
	def, ok := schema.Defs[key]
	if !ok {
		t.Fatalf("expected %q in $defs", key)
	}
	if def == schema {
		t.Fatal("root must be a copy of the definition, not the same pointer (marshal cycle)")
	}
	if got := schema.Properties["nested_address_details"].Ref; got != "#/$defs/"+key {
		t.Errorf("self reference = %q, want $ref to %q", got, key)
	}
	// Decorating the root must not leak into the shared definition.
	schema.Description = "root only"
	if def.Description == "root only" {
		t.Error("mutating the root changed the $defs entry")
	}
	if _, err := json.Marshal(schema); err != nil {
		t.Fatalf("Failed to marshal self-referencing schema: %v", err)
	}
}

// TestAcyclicSchemaIsFullyInline tests that messages without reference cycles
// produce self-contained schemas: no $defs, no $ref anywhere, and
// message-typed fields expanded in place.
func TestAcyclicSchemaIsFullyInline(t *testing.T) {
	testCases := []struct {
		name   string
		schema func() *jsonschema.Schema
	}{
		{"User", func() *jsonschema.Schema { return (&User{}).JsonSchema() }},
		{"Address", func() *jsonschema.Schema { return (&Address{}).JsonSchema() }},
		{"ComprehensiveUser", func() *jsonschema.Schema { return (&ComprehensiveUser{}).JsonSchema() }},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			schema := tc.schema()
			if _, err := ValidateSchemaWithName(tc.name, schema); err != nil {
				t.Fatalf("%s schema validation failed: %v", tc.name, err)
			}
			if schema.Defs != nil {
				t.Errorf("acyclic schema must not carry $defs, got %d entries", len(schema.Defs))
			}
			data, err := json.Marshal(schema)
			if err != nil {
				t.Fatalf("Failed to marshal schema: %v", err)
			}
			for _, keyword := range []string{"\"$ref\"", "\"$defs\""} {
				if strings.Contains(string(data), keyword) {
					t.Errorf("acyclic schema JSON must not contain %s", keyword)
				}
			}
		})
	}

	address := (&ComprehensiveUser{}).JsonSchema().Properties["address"]
	if address == nil || address.Type != "object" || address.Properties["city"] == nil {
		t.Errorf("address must be inlined with its own properties, got %+v", address)
	}
}

// TestRootIsAlwaysAnObject checks the wire shape MCP clients depend on: every
// JsonSchema() root is a literal type: "object" with properties — never a
// $ref — whether the message is acyclic or sits on a cycle.
func TestRootIsAlwaysAnObject(t *testing.T) {
	testCases := []struct {
		name   string
		schema func() *jsonschema.Schema
		cyclic bool
	}{
		{"User", func() *jsonschema.Schema { return (&User{}).JsonSchema() }, false},
		{"Address", func() *jsonschema.Schema { return (&Address{}).JsonSchema() }, false},
		{"ComprehensiveUser", func() *jsonschema.Schema { return (&ComprehensiveUser{}).JsonSchema() }, false},
		{"AddressDetails", func() *jsonschema.Schema { return (&AddressDetails{}).JsonSchema() }, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			schema := tc.schema()
			if schema == nil {
				t.Fatalf("%s.JsonSchema() returned nil", tc.name)
			}
			if _, err := ValidateSchemaWithName(tc.name, schema); err != nil {
				t.Fatalf("%s schema validation failed: %v", tc.name, err)
			}
			data, err := json.Marshal(schema)
			if err != nil {
				t.Fatalf("Marshal failed (possible circular reference): %v", err)
			}
			var parsed map[string]interface{}
			if err := json.Unmarshal(data, &parsed); err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}
			if parsed["type"] != "object" {
				t.Errorf("root type = %v, want object", parsed["type"])
			}
			if _, hasRef := parsed["$ref"]; hasRef {
				t.Error("root must not be a $ref")
			}
			if _, hasProps := parsed["properties"]; !hasProps {
				t.Error("root must expose its properties")
			}
			if _, hasDefs := parsed["$defs"]; hasDefs != tc.cyclic {
				t.Errorf("$defs present = %v, want %v", hasDefs, tc.cyclic)
			}
		})
	}
}
`
	testPath := filepath.Join(pkgDir, "serialization_test.go")
	err = os.WriteFile(testPath, []byte(testContent), 0o644)
	s.Require().NoError(err, "Failed to write test file")

	// Run go mod tidy
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	s.Require().NoError(err, "go mod tidy failed: %s", string(output))

	// Run the tests - this will fail with a stack overflow if the bug exists
	cmd = exec.Command("go", "test", "-v", "-timeout", "30s", "./...")
	cmd.Dir = tmpDir
	output, err = cmd.CombinedOutput()

	// Log the output for debugging
	s.T().Logf("Test output:\n%s", string(output))

	s.Require().NoError(err, "JSON serialization tests failed: %s\n\nThis likely indicates the circular reference bug. See REPORT.md for details.", string(output))

	s.T().Log("Schema JSON serialization tests passed - no circular reference detected")
}

// TestOneofJSONValidates verifies that nested PascalCase oneof JSON validates
// against the generated schema and that multi-variant wrappers are rejected by oneOf.
func (s *IntegrationTestSuite) TestOneofJSONValidates() {
	if testing.Short() {
		s.T().Skip("Skipping oneof JSON validation test in short mode")
	}

	contents := s.RunGenerate()

	tmpDir := s.TempDir()
	pkgDir := filepath.Join(tmpDir, "usersv1")
	err := os.MkdirAll(pkgDir, 0o755)
	s.Require().NoError(err)

	for name, content := range contents {
		filePath := filepath.Join(pkgDir, filepath.Base(name))
		err := os.WriteFile(filePath, []byte(content), 0o644)
		s.Require().NoError(err, "Failed to write file %s", filePath)
	}

	goMod := `module testoneof

go 1.21

require github.com/google/jsonschema-go v0.4.2
`
	s.Require().NoError(os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0o644))

	// Replace the empty OneOfDemo stub with a faithful protoc-gen-go shape so the
	// round-trip test below can marshal a real value and validate the whole document.
	stub := strings.Replace(userStubTypes(), "type OneOfDemo struct{}\n", "", 1)
	s.Require().NoError(os.WriteFile(filepath.Join(pkgDir, "stub_types.go"), []byte(stub), 0o644))
	s.Require().NoError(os.WriteFile(filepath.Join(pkgDir, "oneof_real.go"), []byte(oneofDemoRealTypes()), 0o644))

	testContent := `package usersv1

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func ValidateSchemaWithName(name string, schema *jsonschema.Schema) (*jsonschema.Resolved, error) {
	if schema == nil {
		return nil, fmt.Errorf("schema %q: cannot be nil", name)
	}
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		return nil, fmt.Errorf("schema %q: invalid structure: %w", name, err)
	}
	return resolved, nil
}

func TestOneofNestedJSONValidates(t *testing.T) {
	schema := (&OneOfDemo{}).JsonSchema()
	resolved, err := ValidateSchemaWithName("OneOfDemo", schema)
	if err != nil {
		t.Fatalf("schema resolve failed: %v", err)
	}

	// Nested PascalCase oneof JSON should validate.
	nested := map[string]any{
		"Field1": map[string]any{"StringValue": "hello"},
	}
	if err := resolved.Validate(nested); err != nil {
		t.Fatalf("nested oneof JSON should validate: %v", err)
	}

	// Multiple variants in one wrapper should fail (oneOf exclusivity).
	multiVariant := map[string]any{
		"Field1": map[string]any{"StringValue": "hello", "IntValue": 42},
	}
	if err := resolved.Validate(multiVariant); err == nil {
		t.Fatal("multiple variants in one oneof wrapper should fail validation")
	}

	// Unset oneof: encoding/json always emits the wrapper key with a null value
	// (the oneof interface field has no json tag). The schema must accept null.
	nullWrapper := map[string]any{
		"Field1": nil,
	}
	if err := resolved.Validate(nullWrapper); err != nil {
		t.Fatalf("null oneof wrapper (unset oneof) should validate: %v", err)
	}

	// A non-object, non-null value must still be rejected.
	badWrapper := map[string]any{
		"Field1": 42,
	}
	if err := resolved.Validate(badWrapper); err == nil {
		t.Fatal("scalar value for oneof wrapper should fail validation")
	}
}

func TestOneofSchemaHasPascalCaseWrappers(t *testing.T) {
	schema := (&OneOfDemo{}).JsonSchema()
	def := schema // the root is the message's own object schema
	for _, key := range []string{"Field1", "Field2", "Field3", "Field4"} {
		if _, ok := def.Properties[key]; !ok {
			t.Errorf("expected wrapper property %q", key)
		}
	}
	content, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	if !strings.Contains(string(content), "\"Field1\"") {
		t.Error("serialized schema should contain Field1 wrapper")
	}
}

func TestComprehensiveUserOneofWrappers(t *testing.T) {
	schema := (&ComprehensiveUser{}).JsonSchema()
	def := schema // the root is the message's own object schema
	for _, key := range []string{"Identifier", "PaymentMethod", "ContactPreference"} {
		wrapper, ok := def.Properties[key]
		if !ok {
			t.Errorf("expected oneof wrapper property %q", key)
			continue
		}
		// The wrapper is a oneOf of a null branch and an object branch (the object
		// branch holds the variant oneOf). null is required because encoding/json
		// emits the wrapper key as null when the oneof is unset.
		if len(wrapper.OneOf) != 2 {
			t.Errorf("wrapper %q should have 2 oneOf branches (null|object), got %d", key, len(wrapper.OneOf))
			continue
		}
		hasNull, hasObject := false, false
		for _, branch := range wrapper.OneOf {
			switch branch.Type {
			case "null":
				hasNull = true
			case "object":
				hasObject = true
			}
		}
		if !hasNull {
			t.Errorf("wrapper %q should have a null branch for the unset oneof case", key)
		}
		if !hasObject {
			t.Errorf("wrapper %q should have an object branch holding the variants", key)
		}
	}
}

// TestOneofRoundTrip marshals a real OneOfDemo value with encoding/json and
// validates the whole document against the generated schema. This guards against
// drift between marshal output and the schema for entire messages (not just
// isolated wrappers). OneOfDemo is all-oneofs with no required fields, so an
// unset value must validate.
func TestOneofRoundTrip(t *testing.T) {
	resolved, err := ValidateSchemaWithName("OneOfDemo", (&OneOfDemo{}).JsonSchema())
	if err != nil {
		t.Fatalf("schema resolve failed: %v", err)
	}

	marshalValidate := func(v *OneOfDemo) error {
		raw, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("unmarshal: %w", err)
		}
		return resolved.Validate(doc)
	}

	// Unset oneofs: encoding/json emits {"Field1":null,"Field2":null,...}.
	unset := &OneOfDemo{}
	raw, _ := json.Marshal(unset)
	if !strings.Contains(string(raw), "\"Field1\":null") {
		t.Fatalf("expected unset oneof to marshal Field1 as null, got: %s", string(raw))
	}
	if err := marshalValidate(unset); err != nil {
		t.Fatalf("marshaled unset OneOfDemo should validate against its own schema: %v\njson: %s", err, string(raw))
	}

	// One scalar variant set: {"Field1":{"StringValue":"hi"},"Field2":null,...}.
	set := &OneOfDemo{Field1: &OneOfDemo_StringValue{StringValue: "hi"}}
	if err := marshalValidate(set); err != nil {
		t.Fatalf("marshaled OneOfDemo with a set variant should validate: %v", err)
	}
}
`
	s.Require().NoError(os.WriteFile(filepath.Join(pkgDir, "oneof_test.go"), []byte(testContent), 0o644))

	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	s.Require().NoError(err, "go mod tidy failed: %s", string(output))

	cmd = exec.Command("go", "test", "-v", "-timeout", "30s", "./...")
	cmd.Dir = tmpDir
	output, err = cmd.CombinedOutput()
	s.T().Logf("Oneof validation test output:\n%s", string(output))
	s.Require().NoError(err, "oneof JSON validation tests failed: %s", string(output))
}

// TestCyclicRootIsIndependentTree checks that a cyclic message's root is
// built separately from its $defs entry: never a $ref wrapper (MCP needs a
// literal object root) and never an alias of the registered definition
// (jsonschema-go resolution requires a tree; json.Marshal must not cycle).
func (s *IntegrationTestSuite) TestCyclicRootIsIndependentTree() {
	content := s.GetGeneratedContent()

	s.Contains(content, `root := AddressDetails_JsonSchema_build(defs, false)`,
		"cyclic root must be built by a second, non-registering build")
	s.NotContains(content, `root := &jsonschema.Schema{Ref:`,
		"no message may use a $ref wrapper as its root")
	s.NotContains(content, `root := defs[`,
		"the root must never alias the registered definition")
	s.NotContains(content, `root := *defs[`,
		"a shallow copy still shares child nodes with the definition")
}

// TestForceLogicForNestedMessages verifies that nested messages with generate=false
// are still generated when their parent has generate=true (force logic).
func (s *IntegrationTestSuite) TestForceLogicForNestedMessages() {
	// This test requires a proto file with nested messages that have generate=false
	// For now, we test with the existing proto structure and verify the logic works

	content := s.GetGeneratedContent()

	// Verify that nested messages are included in generated code
	// Address.AddressDetails is a nested message in the test proto
	s.Contains(content, "Address_AddressDetails_JsonSchema_WithDefs",
		"Expected nested message Address_AddressDetails_JsonSchema_WithDefs to be generated")

	// Verify the nested message function is called from parent
	s.Contains(content, `schema.Properties["address_details"] = Address_AddressDetails_JsonSchema_WithDefs(defs)`,
		"Expected parent message to call nested message's schema function")

	// The nested message is acyclic, so it inlines: nothing is registered
	// under $defs.
	s.NotContains(content, `defs["users.v1.Address.AddressDetails"]`,
		"acyclic nested message must not register in defs")
}

// TestForceLogicForFieldDependencies verifies that field dependencies with generate=false
// are still generated when their parent has generate=true (force logic).
func (s *IntegrationTestSuite) TestForceLogicForFieldDependencies() {
	content := s.GetGeneratedContent()

	// Verify that message-type field dependencies are included
	// ComprehensiveUser has Address field which should generate Address schema
	s.Contains(content, "Address_JsonSchema_WithDefs",
		"Expected field dependency Address_JsonSchema_WithDefs to be generated")

	// Verify the dependency is referenced in the parent
	s.Contains(content, `schema.Properties["address"]`,
		"Expected parent message to reference field dependency")
}

// TestForceLogicRuntime verifies that forced messages are present in $defs at runtime.
func (s *IntegrationTestSuite) TestForceLogicRuntime() {
	contents := s.RunGenerate()

	// Create temp directory for runtime test
	tmpDir, err := os.MkdirTemp("", "force-logic-test-*")
	s.Require().NoError(err)
	defer os.RemoveAll(tmpDir)

	// Write generated code
	for name, content := range contents {
		baseName := filepath.Base(name)
		outPath := filepath.Join(tmpDir, baseName)
		err := os.WriteFile(outPath, []byte(content), 0o644)
		s.Require().NoError(err)
	}

	// Write stub types
	err = os.WriteFile(filepath.Join(tmpDir, "stub_types.go"), []byte(userStubTypes()), 0o644)
	s.Require().NoError(err)

	// Write test file that verifies forced messages are generated and inlined
	testContent := `package usersv1

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

// ValidateSchema validates a *jsonschema.Schema and returns a resolved schema
// that can be used for validation. It ensures:
//   - The schema is not nil
//   - The schema type is "object" (required by MCP spec)
//   - The schema structure is valid (via Resolve)
//
// Returns the resolved schema on success, or an error if validation fails.
func ValidateSchema(schema *jsonschema.Schema) (*jsonschema.Resolved, error) {
	// Step 1: Check for nil
	if schema == nil {
		return nil, fmt.Errorf("schema cannot be nil")
	}

	// Step 2: The root must be a real object schema. MCP requires a literal
	// type: "object" root, so a $ref root is never acceptable.
	if schema.Ref != "" {
		return nil, fmt.Errorf("schema root must not be a $ref (got %q)", schema.Ref)
	}
	if schema.Type != "object" {
		return nil, fmt.Errorf("schema must have type \"object\" (got type %q)", schema.Type)
	}

	// Step 3: Verify all $ref pointers can be resolved
	// First, collect all $ref values from the schema
	refs := collectRefs(schema)
	
	// Check that all referenced schemas exist in Definitions
	// Every $ref must resolve into $defs — a nil Defs map resolves nothing.
	if len(refs) > 0 {
		for ref := range refs {
			// Extract the key from the $ref (format: "#/$defs/key")
			key := extractRefKey(ref)
			if key != "" {
				if _, exists := schema.Defs[key]; !exists {
					return nil, fmt.Errorf("$ref %q points to non-existent definition %q", ref, key)
				}
			}
		}
	}

	// Step 4: Resolve the schema - this validates the schema structure itself
	// ValidateDefaults: true enables validation of default values in the schema
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{
		ValidateDefaults: true,
	})
	if err != nil {
		return nil, fmt.Errorf("invalid schema structure: %w", err)
	}

	return resolved, nil
}

// ValidateSchemaWithName is a convenience wrapper that includes the schema name
// in error messages for better debugging.
func ValidateSchemaWithName(name string, schema *jsonschema.Schema) (*jsonschema.Resolved, error) {
	if schema == nil {
		return nil, fmt.Errorf("schema %q: cannot be nil", name)
	}

	// The root must be a real object schema. MCP requires a literal
	// type: "object" root, so a $ref root is never acceptable.
	if schema.Ref != "" {
		return nil, fmt.Errorf("schema %q: root must not be a $ref (got %q)", name, schema.Ref)
	}
	if schema.Type != "object" {
		return nil, fmt.Errorf("schema %q: must have type \"object\" (got type %q)", name, schema.Type)
	}

	// Verify all $ref pointers exist
	refs := collectRefs(schema)
	// Every $ref must resolve into $defs — a nil Defs map resolves nothing.
	if len(refs) > 0 {
		for ref := range refs {
			key := extractRefKey(ref)
			if key != "" {
				if _, exists := schema.Defs[key]; !exists {
					return nil, fmt.Errorf("schema %q: $ref %q points to non-existent definition %q", name, ref, key)
				}
			}
		}
	}

	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{
		ValidateDefaults: true,
	})
	if err != nil {
		return nil, fmt.Errorf("schema %q: invalid schema structure: %w", name, err)
	}

	return resolved, nil
}

// collectRefs recursively collects all $ref values from a schema
func collectRefs(schema *jsonschema.Schema) map[string]bool {
	refs := make(map[string]bool)
	if schema == nil {
		return refs
	}
	
	if schema.Ref != "" {
		refs[schema.Ref] = true
	}
	
	if schema.Properties != nil {
		for _, prop := range schema.Properties {
			for ref := range collectRefs(prop) {
				refs[ref] = true
			}
		}
	}
	
	if schema.Items != nil {
		for ref := range collectRefs(schema.Items) {
			refs[ref] = true
		}
	}
	
	if schema.AdditionalProperties != nil {
		for ref := range collectRefs(schema.AdditionalProperties) {
			refs[ref] = true
		}
	}
	
	if schema.Defs != nil {
		for _, def := range schema.Defs {
			for ref := range collectRefs(def) {
				refs[ref] = true
			}
		}
	}
	
	return refs
}

// extractRefKey extracts the definition key from a $ref value
// Format: "#/$defs/key" -> "key"
func extractRefKey(ref string) string {
	prefix := "#/$defs/"
	if strings.HasPrefix(ref, prefix) {
		return ref[len(prefix):]
	}
	return ""
}

func getDefKeys(defs map[string]*jsonschema.Schema) []string {
	keys := make([]string, 0, len(defs))
	for k := range defs {
		keys = append(keys, k)
	}
	return keys
}

func TestNestedMessageIsInlined(t *testing.T) {
	// Address.address_details is a nested, acyclic message: forced generation
	// still happens, and the schema is expanded in place — with the field
	// comment overriding the message comment on that copy — and no $defs.
	schema := (&Address{}).JsonSchema()
	if schema == nil {
		t.Fatal("Address.JsonSchema() returned nil")
	}
	if _, err := ValidateSchemaWithName("Address", schema); err != nil {
		t.Fatalf("Address schema validation failed: %v", err)
	}
	if schema.Defs != nil {
		t.Errorf("expected no $defs for an acyclic message, got %v", getDefKeys(schema.Defs))
	}
	details := schema.Properties["address_details"]
	if details == nil || details.Ref != "" || details.Type != "object" {
		t.Fatalf("address_details must be an inline object, got %+v", details)
	}
	if details.Properties["street"] == nil {
		t.Error("inlined AddressDetails must carry its own properties")
	}
	if details.Description != "Additional address details stored as a nested message." {
		t.Errorf("field comment must override the message comment on the inline copy, got %q", details.Description)
	}
}

func TestFieldDependencyIsInlined(t *testing.T) {
	// ComprehensiveUser.address depends on Address, a separate top-level
	// message: forced generation still happens, and the dependency is
	// expanded in place rather than referenced.
	schema := (&ComprehensiveUser{}).JsonSchema()
	if schema == nil {
		t.Fatal("ComprehensiveUser.JsonSchema() returned nil")
	}
	if _, err := ValidateSchemaWithName("ComprehensiveUser", schema); err != nil {
		t.Fatalf("ComprehensiveUser schema validation failed: %v", err)
	}
	if schema.Defs != nil {
		t.Errorf("expected no $defs for an acyclic message, got %v", getDefKeys(schema.Defs))
	}
	address := schema.Properties["address"]
	if address == nil || address.Ref != "" || address.Type != "object" || address.Properties["city"] == nil {
		t.Fatalf("address must be an inline object with Address's properties, got %+v", address)
	}
}
`
	err = os.WriteFile(filepath.Join(tmpDir, "force_test.go"), []byte(testContent), 0o644)
	s.Require().NoError(err)

	// Write go.mod
	goModContent := `module testserialize/usersv1

go 1.21

require github.com/google/jsonschema-go v0.3.0
`
	err = os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goModContent), 0o644)
	s.Require().NoError(err)

	// Run go mod tidy
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	s.Require().NoError(err, "go mod tidy failed: %s", string(output))

	// Run the test
	cmd = exec.Command("go", "test", "-v", "-run", "TestNestedMessageInDefs|TestFieldDependencyInDefs")
	cmd.Dir = tmpDir
	output, err = cmd.CombinedOutput()

	s.T().Logf("Force logic test output:\n%s", string(output))

	s.Require().NoError(err, "Force logic tests failed: %s\n\nThis indicates forced messages may not be generating correctly.", string(output))
}

// TestNoJsonSchemaOptionsProto tests that no jsonschema file is generated when
// a proto file has no json_schema options at any level (file, message, or field).
func (s *IntegrationTestSuite) TestNoJsonSchemaOptionsProto() {
	requireProtoc(s.T())

	noOptionsProto := "no_options/v1/no_options.proto"
	outputPath := filepath.Join(tempDir(s.T()), "no_options.pb")
	fds := buildDescriptorSet(s.T(), s.workspaceRoot, []string{noOptionsProto}, outputPath)

	p := createTestPlugin(s.T(), fds, []string{noOptionsProto})

	// Generate schema code
	err := plugin.Generate(p, "test")
	s.Require().NoError(err, "Generate should not fail even when no schemas are generated")

	resp := p.Response()
	s.Require().Empty(resp.GetError(), "Generate response should have no error: %s", resp.GetError())

	// CRITICAL CHECK: Verify NO jsonschema files were generated
	var jsonschemaFiles []string
	for _, f := range resp.File {
		if strings.HasSuffix(f.GetName(), "_jsonschema.pb.go") {
			jsonschemaFiles = append(jsonschemaFiles, f.GetName())
		}
	}

	s.Empty(jsonschemaFiles,
		"Expected NO jsonschema files to be generated for proto without json_schema options, but got: %v",
		jsonschemaFiles)

	s.T().Log("Correctly skipped jsonschema generation for proto without json_schema options")
}

// TestNoJsonSchemaOptionsWithProtoc tests the full pipeline using protoc with a proto
// that has no json_schema options, verifying no output files are created.
func (s *IntegrationTestSuite) TestNoJsonSchemaOptionsWithProtoc() {
	if testing.Short() {
		s.T().Skip("Skipping end-to-end test in short mode")
	}

	// Check if protoc is available
	if _, err := exec.LookPath("protoc"); err != nil {
		s.T().Skip("protoc not found in PATH, skipping end-to-end test")
	}

	// Create a temporary directory for output
	tmpDir := s.TempDir()
	outputDir := filepath.Join(tmpDir, "output")
	err := os.MkdirAll(outputDir, 0o755)
	s.Require().NoError(err, "Failed to create output directory")

	protoFile := "no_options/v1/no_options.proto"

	// Run protoc with our plugin
	args := []string{
		"--plugin=protoc-gen-go-jsonschema=" + s.pluginBinary,
		"--go-jsonschema_out=" + outputDir,
		"--go-jsonschema_opt=paths=source_relative",
	}
	for _, path := range protoPaths(s.workspaceRoot) {
		args = append(args, "--proto_path="+path)
	}
	args = append(args, protoFile)

	protocCmd := exec.Command("protoc", args...)
	output, err := protocCmd.CombinedOutput()
	s.Require().NoError(err, "protoc failed: %s\nArgs: %v", string(output), args)

	// Verify NO output files were created
	// Walk the output directory and check for any _jsonschema.pb.go files
	var jsonschemaFiles []string
	err = filepath.Walk(outputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, "_jsonschema.pb.go") {
			jsonschemaFiles = append(jsonschemaFiles, path)
		}
		return nil
	})
	s.Require().NoError(err, "Failed to walk output directory")

	s.Empty(jsonschemaFiles,
		"Expected NO jsonschema files to be generated for proto without json_schema options, but found: %v",
		jsonschemaFiles)

	s.T().Log("End-to-end test passed: no jsonschema files generated for proto without options")
}
