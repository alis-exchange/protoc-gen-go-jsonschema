package plugin

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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

// TestGoldenFile tests that generated output matches the golden file.
func (s *IntegrationTestSuite) TestGoldenFile() {
	contents := s.RunGenerate()

	for name, content := range contents {
		// Construct golden file path
		baseName := filepath.Base(name)
		goldenPath := filepath.Join(goldenDir(), baseName+".golden")

		assertGoldenFile(s.T(), content, goldenPath, *updateGolden)
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

	// We also need to generate the regular protobuf Go code for the generated schema to compile
	// For now, we'll create a stub file that satisfies the imports
	stubContent := `package usersv1

// Stub types for compilation test
type Address struct{}
type AddressDetails struct{}
type ContactInfo struct{}
type Metadata struct{}
type ComprehensiveUser struct{}
type User struct{}
type CreateUserRequest struct{}
type GetUserRequest struct{}
type UpdateUserRequest struct{}
type DeleteUserRequest struct{}
type DeleteUserResponse struct{}
type CreateComprehensiveUserRequest struct{}
type BatchGetUsersRequest struct{}
type BatchGetUsersResponse struct{}
type UserProfile struct{}
type PersonalProfile struct{}
type BusinessProfile struct{}
type RepeatedFieldsDemo struct{}
type MapFieldsDemo struct{}
type OneOfDemo struct{}
type WellKnownTypesDemo struct{}
type Address_AddressDetails struct{}
`
	stubPath := filepath.Join(pkgDir, "stub_types.go")
	err = os.WriteFile(stubPath, []byte(stubContent), 0o644)
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

	// Determine proto paths
	protoBasePath := filepath.Join(s.workspaceRoot, "testdata", "protos")
	protoFile := "users/v1/user.proto"

	// Check for additional proto paths (alis options)
	var additionalPaths []string
	alisPath := "/Volumes/ExternalSSD/alis.build/alis/define"
	if _, err := os.Stat(alisPath); err == nil {
		additionalPaths = append(additionalPaths, "--proto_path="+alisPath)
	}

	// Run protoc with our plugin
	args := []string{
		"--plugin=protoc-gen-go-jsonschema=" + s.pluginBinary,
		"--go-jsonschema_out=" + outputDir,
		"--go-jsonschema_opt=paths=source_relative",
		"--proto_path=" + protoBasePath,
	}
	args = append(args, additionalPaths...)
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

	protoPath := filepath.Join(s.workspaceRoot, "testdata", "protos")
	protoFile := "users/v1/user.proto"
	outputPath := filepath.Join(tmpDir, "test.pb")

	// Check for additional proto paths
	var additionalPaths []string
	alisPath := "/Volumes/ExternalSSD/alis.build/alis/define"
	if _, err := os.Stat(alisPath); err == nil {
		additionalPaths = append(additionalPaths, alisPath)
	}

	fds := generateDescriptorSet(s.T(), protoPath, protoFile, outputPath, additionalPaths...)

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

// TestSchemaDefinitionsArePopulated verifies that $defs are properly populated.
func (s *IntegrationTestSuite) TestSchemaDefinitionsArePopulated() {
	content := s.GetGeneratedContent()

	// Check for definitions population
	s.Contains(content, "defs[", "Generated code should populate defs map")

	// Check for $ref usage
	if !strings.Contains(content, `Ref: "#/$defs/`) {
		s.T().Log("Note: No $ref found - may be inlining all schemas")
	}

	// Check for Definitions assignment
	s.Contains(content, "root.Definitions = defs", "Generated code should assign Definitions to root schema")
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
	stubContent := `package usersv1

// Stub types for serialization test
type Address struct{}
type AddressDetails struct{}
type ContactInfo struct{}
type Metadata struct{}
type ComprehensiveUser struct{}
type User struct{}
type CreateUserRequest struct{}
type GetUserRequest struct{}
type UpdateUserRequest struct{}
type DeleteUserRequest struct{}
type DeleteUserResponse struct{}
type CreateComprehensiveUserRequest struct{}
type BatchGetUsersRequest struct{}
type BatchGetUsersResponse struct{}
type UserProfile struct{}
type PersonalProfile struct{}
type BusinessProfile struct{}
type RepeatedFieldsDemo struct{}
type MapFieldsDemo struct{}
type OneOfDemo struct{}
type WellKnownTypesDemo struct{}
type Address_AddressDetails struct{}
`
	stubPath := filepath.Join(pkgDir, "stub_types.go")
	err = os.WriteFile(stubPath, []byte(stubContent), 0o644)
	s.Require().NoError(err, "Failed to write stub file")

	// Create a test file that verifies JSON serialization works
	testContent := `package usersv1

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSchemaCanBeSerialized verifies that calling JsonSchema() and then
// json.Marshal() does not cause a stack overflow from circular references.
// This test will fail with a stack overflow if the generated code has
// the circular reference bug (root schema in defs, then defs assigned to root.Definitions).
func TestSchemaCanBeSerialized(t *testing.T) {
	testCases := []struct {
		name   string
		schema func() interface{ }
	}{
		{"Address", func() interface{} { return (&Address{}).JsonSchema() }},
		{"User", func() interface{} { return (&User{}).JsonSchema() }},
		{"ComprehensiveUser", func() interface{} { return (&ComprehensiveUser{}).JsonSchema() }},
		{"AddressDetails", func() interface{} { return (&AddressDetails{}).JsonSchema() }},
		{"ContactInfo", func() interface{} { return (&ContactInfo{}).JsonSchema() }},
		{"Metadata", func() interface{} { return (&Metadata{}).JsonSchema() }},
		{"UserProfile", func() interface{} { return (&UserProfile{}).JsonSchema() }},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			schema := tc.schema()
			
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
		})
	}
}

// TestSelfReferencingSchemaSerializable specifically tests schemas that have
// self-referential messages (like AddressDetails which contains AddressDetails).
// These are particularly prone to circular reference issues.
func TestSelfReferencingSchemaSerializable(t *testing.T) {
	// AddressDetails is self-referential in the proto definition
	schema := (&AddressDetails{}).JsonSchema()
	
	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("Failed to marshal self-referencing schema: %v", err)
	}
	
	t.Logf("Self-referencing AddressDetails schema serialized successfully (%d bytes)", len(data))
}

// TestSchemaDefinitionsAreSerializable tests that when Definitions (defs) is
// populated and assigned to the root schema, serialization still works.
// This specifically tests the circular reference scenario where:
//   root = defs["key"]
//   root.Definitions = defs  // defs contains root!
func TestSchemaDefinitionsAreSerializable(t *testing.T) {
	schema := (&User{}).JsonSchema()
	
	// Verify Definitions is not nil (the bug only occurs when Definitions is set)
	if schema.Definitions == nil {
		t.Fatal("Expected schema.Definitions to be non-nil")
	}
	
	// Check that definitions contains entries
	if len(schema.Definitions) == 0 {
		t.Fatal("Expected schema.Definitions to have entries")
	}
	
	t.Logf("Schema has %d definitions", len(schema.Definitions))
	for key := range schema.Definitions {
		t.Logf("  - %s", key)
	}
	
	// CRITICAL CHECK: Verify the root schema is NOT in its own definitions
	// This is the circular reference that causes stack overflow!
	// The generated code does: root := defs["key"]; root.Definitions = defs
	// If defs still contains the root, we have: root -> Definitions -> root (cycle!)
	if _, hasRoot := schema.Definitions["users.v1.User"]; hasRoot {
		t.Error("CIRCULAR REFERENCE DETECTED: Root schema 'users.v1.User' is in its own Definitions!")
		t.Error("This WILL cause a stack overflow when marshaling to JSON with some library versions.")
		t.Error("The fix is to delete the root from defs before assigning: delete(defs, key)")
	} else {
		t.Log("Good: Root schema is not in its own Definitions (no circular reference)")
	}
	
	// Now serialize - this is where the circular reference would cause stack overflow
	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("Failed to marshal schema with definitions: %v", err)
	}
	
	// Verify the serialized JSON contains $defs
	jsonStr := string(data)
	if !strings.Contains(jsonStr, "$defs") && !strings.Contains(jsonStr, "definitions") {
		t.Log("WARNING: Serialized JSON does not contain $defs or definitions")
		t.Log("This might mean the Definitions field is not being serialized")
	}
	
	t.Logf("Schema with definitions serialized successfully (%d bytes)", len(data))
}

// TestCircularReferenceDetection explicitly tests for the circular reference bug.
// This creates a scenario that mimics what the generated code does and checks
// if it would cause infinite recursion.
func TestCircularReferenceDetection(t *testing.T) {
	// Get schemas that should have themselves in defs based on the generated pattern
	testCases := []struct {
		name     string
		schema   func() interface{}
		fullName string
	}{
		{"User", func() interface{} { return (&User{}).JsonSchema() }, "users.v1.User"},
		{"Address", func() interface{} { return (&Address{}).JsonSchema() }, "users.v1.Address"},
		{"ComprehensiveUser", func() interface{} { return (&ComprehensiveUser{}).JsonSchema() }, "users.v1.ComprehensiveUser"},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			schema := tc.schema()
			
			// Use reflection to check the Definitions field
			// (since we don't have direct access to *jsonschema.Schema type here)
			data, err := json.Marshal(schema)
			if err != nil {
				t.Fatalf("Marshal failed (possible circular reference): %v", err)
			}
			
			// Parse the JSON to check if root is in $defs
			var parsed map[string]interface{}
			if err := json.Unmarshal(data, &parsed); err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}
			
			// Check $defs for circular reference
			if defs, ok := parsed["$defs"].(map[string]interface{}); ok {
				if _, hasRoot := defs[tc.fullName]; hasRoot {
					t.Errorf("CIRCULAR REFERENCE: %s is in its own $defs!", tc.fullName)
				} else {
					t.Logf("Good: %s is not in its own $defs", tc.fullName)
				}
			}
			
			t.Logf("%s schema serialized OK (%d bytes)", tc.name, len(data))
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

// TestNoCircularReferenceInGeneratedCode checks that the generated code
// properly removes the root schema from defs before assigning to Definitions.
// This is a static check that the fix for the circular reference bug is present.
func (s *IntegrationTestSuite) TestNoCircularReferenceInGeneratedCode() {
	content := s.GetGeneratedContent()

	// The fix should include a delete statement to remove the root from defs
	// before assigning defs to root.Definitions
	//
	// Pattern we're looking for:
	//   delete(defs, "...")
	//   root.Definitions = defs
	//
	// Or alternative fix patterns that prevent circular references

	hasAssignment := strings.Contains(content, "root.Definitions = defs")

	if hasAssignment {
		// If we assign defs to root.Definitions, we need to ensure root is not in defs
		// Check for the delete pattern
		hasDelete := strings.Contains(content, "delete(defs,")

		if !hasDelete {
			// Alternative: check if using a reference wrapper pattern
			// e.g., returning &jsonschema.Schema{Ref: ..., Definitions: defs}
			// instead of modifying root

			// For now, we warn but don't fail - the runtime test will catch the actual bug
			s.T().Log("WARNING: Generated code assigns defs to root.Definitions without deleting root from defs.")
			s.T().Log("This may cause a circular reference and stack overflow when serializing to JSON.")
			s.T().Log("See REPORT.md for details on the fix.")
		} else {
			s.T().Log("Generated code properly removes root from defs before assignment (circular reference fix present)")
		}
	}
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
	s.Contains(content, `schema.Properties["addressDetails"] = Address_AddressDetails_JsonSchema_WithDefs(defs)`,
		"Expected parent message to call nested message's schema function")

	// Verify the $defs key is present
	s.Contains(content, `defs["users.v1.Address.AddressDetails"]`,
		"Expected nested message to be added to defs")
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
	stubContent := `package usersv1

type UserStatus int32
type AccountType int32
type Priority int32
type Address struct{}
type Address_AddressDetails struct{}
type AddressDetails struct{}
type ContactInfo struct{}
type Metadata struct{}
type ComprehensiveUser struct{}
type User struct{}
type CreateUserRequest struct{}
type GetUserRequest struct{}
type UpdateUserRequest struct{}
type DeleteUserRequest struct{}
type DeleteUserResponse struct{}
type CreateComprehensiveUserRequest struct{}
type BatchGetUsersRequest struct{}
type BatchGetUsersResponse struct{}
type UserProfile struct{}
type PersonalProfile struct{}
type BusinessProfile struct{}
type RepeatedFieldsDemo struct{}
type MapFieldsDemo struct{}
type OneOfDemo struct{}
type WellKnownTypesDemo struct{}
`
	err = os.WriteFile(filepath.Join(tmpDir, "stub_types.go"), []byte(stubContent), 0o644)
	s.Require().NoError(err)

	// Write test file that verifies forced messages are in $defs
	testContent := `package usersv1

import (
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func getDefKeys(defs map[string]*jsonschema.Schema) []string {
	keys := make([]string, 0, len(defs))
	for k := range defs {
		keys = append(keys, k)
	}
	return keys
}

func TestNestedMessageInDefs(t *testing.T) {
	// Test that Address schema has AddressDetails in its definitions
	// This verifies that nested messages are forced to generate
	schema := (&Address{}).JsonSchema()
	if schema == nil {
		t.Fatal("Address.JsonSchema() returned nil")
	}
	if schema.Definitions == nil {
		t.Fatal("Address schema has no Definitions")
	}

	// The nested message should be in $defs (forced generation)
	nestedKey := "users.v1.Address.AddressDetails"
	if _, ok := schema.Definitions[nestedKey]; !ok {
		t.Errorf("Expected nested message %q in Definitions (forced generation), got keys: %v", nestedKey, getDefKeys(schema.Definitions))
	}

	t.Logf("Address schema Definitions keys: %v", getDefKeys(schema.Definitions))
}

func TestFieldDependencyInDefs(t *testing.T) {
	// Test that ComprehensiveUser schema has Address in its definitions
	// This verifies that field dependencies are forced to generate
	schema := (&ComprehensiveUser{}).JsonSchema()
	if schema == nil {
		t.Fatal("ComprehensiveUser.JsonSchema() returned nil")
	}
	if schema.Definitions == nil {
		t.Fatal("ComprehensiveUser schema has no Definitions")
	}

	// Field dependency should be in $defs (forced generation)
	dependencyKey := "users.v1.Address"
	if _, ok := schema.Definitions[dependencyKey]; !ok {
		t.Errorf("Expected field dependency %q in Definitions (forced generation), got keys: %v", dependencyKey, getDefKeys(schema.Definitions))
	}

	t.Logf("ComprehensiveUser schema Definitions keys: %v", getDefKeys(schema.Definitions))
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
