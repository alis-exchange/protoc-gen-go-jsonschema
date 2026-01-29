package plugin

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/stretchr/testify/suite"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

// PluginTestSuite is the base test suite that provides common setup and teardown
// functionality for all plugin tests. It handles:
// - Finding the workspace root
// - Regenerating descriptor sets from proto files
// - Loading FileDescriptorSet and creating protogen.Plugin instances
// - Finding target files and messages for testing
type PluginTestSuite struct {
	suite.Suite

	// workspaceRoot is the absolute path to the project root (where go.mod is)
	workspaceRoot string

	// fds is the FileDescriptorSet loaded from the generated descriptor file
	fds *descriptorpb.FileDescriptorSet

	// plugin is a fresh protogen.Plugin instance created for each test
	plugin *protogen.Plugin

	// file is the target proto file within the plugin
	file *protogen.File

	// generator is a Generator instance for tests that need it
	generator *Generator

	// generatedFile is a GeneratedFile for tests that need message schema generation
	generatedFile *protogen.GeneratedFile
}

// SetupSuite runs once before all tests in the suite.
// It finds the workspace root and regenerates the descriptor set.
func (s *PluginTestSuite) SetupSuite() {
	s.workspaceRoot = s.findWorkspaceRoot()
	s.regenerateDescriptorSet()
}

// TearDownSuite runs once after all tests in the suite complete.
// Override in child suites if cleanup is needed.
func (s *PluginTestSuite) TearDownSuite() {
	// Base implementation does nothing
	// Child suites can override for cleanup
}

// SetupTest runs before each individual test.
// It creates a fresh plugin instance and finds the target file.
func (s *PluginTestSuite) SetupTest() {
	s.loadPlugin()
}

// TearDownTest runs after each individual test.
// Override in child suites if per-test cleanup is needed.
func (s *PluginTestSuite) TearDownTest() {
	// Base implementation does nothing
}

// findWorkspaceRoot finds the root of the Go module by looking for go.mod.
func (s *PluginTestSuite) findWorkspaceRoot() string {
	// Try using go list first
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}")
	output, err := cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(output))
	}

	// Fallback: walk up from current directory
	dir, err := os.Getwd()
	s.Require().NoError(err, "Failed to get working directory")

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			s.T().Fatal("Could not find go.mod in any parent directory")
		}
		dir = parent
	}
}

// regenerateDescriptorSet generates the FileDescriptorSet from the proto files.
// This ensures tests always use fresh descriptors matching the current protos.
// Includes all proto files in the users/v1 package to support multi-file scenarios.
func (s *PluginTestSuite) regenerateDescriptorSet() {
	protoPath := filepath.Join(s.workspaceRoot, "testdata", "protos")
	// Include all proto files in the package - user.proto imports common.proto
	protoFiles := []string{"users/v1/user.proto", "users/v1/common.proto", "users/v1/admin.proto"}
	outputPath := filepath.Join(s.workspaceRoot, "testdata", "descriptors", "user.pb")

	// Create output directory if it doesn't exist
	err := os.MkdirAll(filepath.Dir(outputPath), 0o755)
	s.Require().NoError(err, "Failed to create descriptor output directory")

	// Build protoc command arguments
	args := []string{
		"--descriptor_set_out=" + outputPath,
		"--include_imports",
		"--include_source_info",
		"--proto_path=" + protoPath,
	}

	// Find alis proto path if available (for custom options)
	// Use home directory to make path portable across systems
	if homeDir, err := os.UserHomeDir(); err == nil {
		alisPath := filepath.Join(homeDir, "alis.build", "alis", "define")
		if _, err := os.Stat(alisPath); err == nil {
			args = append(args, "--proto_path="+alisPath)
		}
	}

	// Add all proto files
	args = append(args, protoFiles...)

	// Run protoc
	cmd := exec.Command("protoc", args...)
	output, err := cmd.CombinedOutput()
	s.Require().NoError(err, "Failed to run protoc: %s\nArgs: %v", string(output), args)

	// Load the generated descriptor set
	s.fds = s.loadDescriptorSetFromPath(outputPath)
	s.T().Logf("Regenerated descriptor set with %d files", len(s.fds.File))
}

// loadDescriptorSetFromPath loads a FileDescriptorSet from a .pb file.
func (s *PluginTestSuite) loadDescriptorSetFromPath(path string) *descriptorpb.FileDescriptorSet {
	data, err := os.ReadFile(path)
	s.Require().NoError(err, "Failed to read descriptor set file %s", path)

	var fds descriptorpb.FileDescriptorSet
	err = proto.Unmarshal(data, &fds)
	s.Require().NoError(err, "Failed to unmarshal descriptor set from %s", path)

	return &fds
}

// loadPlugin creates a fresh protogen.Plugin instance and finds the target file.
// Includes all proto files in the users/v1 package to generate all schemas together.
func (s *PluginTestSuite) loadPlugin() {
	req := &pluginpb.CodeGeneratorRequest{
		// Include all proto files in the package - they share the same Go package
		// and reference each other, so they must be generated together
		FileToGenerate: []string{"users/v1/user.proto", "users/v1/common.proto", "users/v1/admin.proto"},
		ProtoFile:      s.fds.File,
	}

	opts := protogen.Options{}
	plugin, err := opts.New(req)
	s.Require().NoError(err, "Failed to create protogen.Plugin")

	s.plugin = plugin
	s.file = s.findFile("user.proto")
	s.generator = &Generator{}
}

// findFile finds a file in the plugin by path suffix.
func (s *PluginTestSuite) findFile(pathSuffix string) *protogen.File {
	for _, f := range s.plugin.Files {
		if strings.HasSuffix(f.Desc.Path(), pathSuffix) {
			return f
		}
	}
	s.T().Fatalf("Could not find file with suffix %q", pathSuffix)
	return nil
}

// FindMessage finds a message in the current file by name.
func (s *PluginTestSuite) FindMessage(name string) *protogen.Message {
	for _, msg := range s.file.Messages {
		if string(msg.Desc.Name()) == name {
			return msg
		}
	}
	s.T().Fatalf("Could not find message %q in file %q", name, s.file.Desc.Path())
	return nil
}

// FindField finds a field in a message by name.
func (s *PluginTestSuite) FindField(msg *protogen.Message, name string) *protogen.Field {
	for _, field := range msg.Fields {
		if string(field.Desc.Name()) == name {
			return field
		}
	}
	s.T().Fatalf("Could not find field %q in message %q", name, msg.Desc.Name())
	return nil
}

// CreateMessageSchemaGenerator creates a MessageSchemaGenerator for testing.
// This is useful for tests that need to test schema generation methods.
func (s *PluginTestSuite) CreateMessageSchemaGenerator() *MessageSchemaGenerator {
	// Generate the file first to get a GeneratedFile
	genFile, err := s.generator.generateFile(s.plugin, s.file)
	s.Require().NoError(err, "Failed to generate file for MessageSchemaGenerator")

	return &MessageSchemaGenerator{
		gr:      s.generator,
		gen:     genFile,
		visited: make(map[string]bool),
	}
}

// RunGenerate runs the Generate function and returns the generated content.
func (s *PluginTestSuite) RunGenerate() map[string]string {
	err := Generate(s.plugin, "test")
	s.Require().NoError(err, "Generate failed")

	resp := s.plugin.Response()
	s.Require().Empty(resp.GetError(), "Generate response error: %s", resp.GetError())

	result := make(map[string]string)
	for _, file := range resp.File {
		if file.Content != nil {
			result[file.GetName()] = file.GetContent()
		}
	}
	return result
}

// GetGeneratedContent is a convenience method that returns the user.proto generated file's content.
// This is the primary test file that contains most message types.
func (s *PluginTestSuite) GetGeneratedContent() string {
	contents := s.RunGenerate()
	// Return the user.proto generated content specifically
	for name, content := range contents {
		if strings.HasSuffix(name, "user_jsonschema.pb.go") {
			return content
		}
	}
	// Fallback to first file if user_jsonschema.pb.go not found
	for _, content := range contents {
		return content
	}
	s.T().Fatal("No generated content found")
	return ""
}

// GetGeneratedContentForFile returns the generated content for a specific proto file.
func (s *PluginTestSuite) GetGeneratedContentForFile(suffix string) string {
	contents := s.RunGenerate()
	for name, content := range contents {
		if strings.HasSuffix(name, suffix) {
			return content
		}
	}
	s.T().Fatalf("No generated content found for %s", suffix)
	return ""
}

// TempDir creates a temporary directory that is automatically cleaned up after the test.
func (s *PluginTestSuite) TempDir() string {
	dir, err := os.MkdirTemp("", "protoc-gen-go-jsonschema-test-*")
	s.Require().NoError(err, "Failed to create temp directory")

	s.T().Cleanup(func() {
		os.RemoveAll(dir)
	})

	return dir
}

// WorkspaceRoot returns the workspace root path.
func (s *PluginTestSuite) WorkspaceRoot() string {
	return s.workspaceRoot
}

// FileDescriptorSet returns the loaded FileDescriptorSet.
func (s *PluginTestSuite) FileDescriptorSet() *descriptorpb.FileDescriptorSet {
	return s.fds
}

// Plugin returns the current protogen.Plugin instance.
func (s *PluginTestSuite) Plugin() *protogen.Plugin {
	return s.plugin
}

// File returns the current target file.
func (s *PluginTestSuite) File() *protogen.File {
	return s.file
}

// Generator returns the Generator instance.
func (s *PluginTestSuite) Generator() *Generator {
	return s.generator
}
