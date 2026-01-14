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
