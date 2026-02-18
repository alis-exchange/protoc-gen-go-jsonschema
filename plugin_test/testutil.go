//go:build plugintest

package plugintest

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

// JSON Schema type constants (mirror plugin package for test assertions).
const (
	jsArray   = "array"
	jsBoolean = "boolean"
	jsInteger = "integer"
	jsNumber  = "number"
	jsObject  = "object"
	jsString  = "string"
)

// updateGolden is a flag to update golden files instead of comparing against them.
// Usage: go test -update
var updateGolden = flag.Bool("update", false, "update golden files")

// testdataDir returns the path to the testdata directory relative to the plugin_test package.
func testdataDir() string {
	return filepath.Join("..", "testdata")
}

// protosDir returns the path to the protos directory within testdata.
func protosDir() string {
	return filepath.Join(testdataDir(), "protos")
}

// descriptorsDir returns the path to the descriptors directory within testdata.
func descriptorsDir() string {
	return filepath.Join(testdataDir(), "descriptors")
}

// goldenDir returns the path to the golden files directory within testdata.
func goldenDir() string {
	return filepath.Join(testdataDir(), "golden")
}

// loadDescriptorSet loads a FileDescriptorSet from a .pb file.
func loadDescriptorSet(t *testing.T, path string) *descriptorpb.FileDescriptorSet {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read descriptor set file %s: %v", path, err)
	}

	var fds descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(data, &fds); err != nil {
		t.Fatalf("Failed to unmarshal descriptor set from %s: %v", path, err)
	}

	return &fds
}

// createTestPlugin creates a protogen.Plugin for testing from a FileDescriptorSet.
func createTestPlugin(t *testing.T, fds *descriptorpb.FileDescriptorSet, filesToGenerate []string) *protogen.Plugin {
	t.Helper()

	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: filesToGenerate,
		ProtoFile:      fds.File,
	}

	opts := protogen.Options{}
	p, err := opts.New(req)
	if err != nil {
		t.Fatalf("Failed to create protogen.Plugin: %v", err)
	}

	return p
}

// generateDescriptorSet runs protoc to generate a FileDescriptorSet.
// It returns the parsed FileDescriptorSet.
func generateDescriptorSet(t *testing.T, protoPath, protoFile, outputPath string, additionalProtoPaths ...string) *descriptorpb.FileDescriptorSet {
	t.Helper()

	// Create output directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		t.Fatalf("Failed to create output directory: %v", err)
	}

	// Build protoc command arguments
	args := []string{
		"--descriptor_set_out=" + outputPath,
		"--include_imports",
		"--include_source_info",
		"--proto_path=" + protoPath,
	}

	// Add additional proto paths
	for _, path := range additionalProtoPaths {
		args = append(args, "--proto_path="+path)
	}

	// Add the proto file
	args = append(args, protoFile)

	// Run protoc
	cmd := exec.Command("protoc", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to run protoc: %v\nOutput: %s\nArgs: %v", err, output, args)
	}

	return loadDescriptorSet(t, outputPath)
}

// assertGoldenFile compares actual content against a golden file.
// If the -update flag is set, it updates the golden file instead.
// It strips timestamp lines from comparison to avoid false failures.
func assertGoldenFile(t *testing.T, actual, goldenPath string, update bool) {
	t.Helper()

	if update {
		// Create directory if it doesn't exist
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("Failed to create golden file directory: %v", err)
		}
		if err := os.WriteFile(goldenPath, []byte(actual), 0o644); err != nil {
			t.Fatalf("Failed to update golden file %s: %v", goldenPath, err)
		}
		t.Logf("Updated golden file: %s", goldenPath)
		return
	}

	expected, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("Failed to read golden file %s: %v\nRun with -update to create it", goldenPath, err)
	}

	// Normalize both contents by removing timestamp lines for comparison
	actualNorm := normalizeGeneratedContent(actual)
	expectedNorm := normalizeGeneratedContent(string(expected))

	if actualNorm != expectedNorm {
		t.Errorf("Output does not match golden file %s.\nRun with -update to update it.\n\nExpected:\n%s\n\nActual:\n%s",
			goldenPath, string(expected), actual)
	}
}

// normalizeGeneratedContent removes variable content like timestamps for comparison.
func normalizeGeneratedContent(content string) string {
	lines := strings.Split(content, "\n")
	var normalized []string
	for _, line := range lines {
		// Skip the "Generated on:" line as it contains a timestamp
		if strings.Contains(line, "Generated on:") {
			continue
		}
		// Skip the "Plugin version:" line as it may vary
		if strings.Contains(line, "Plugin version:") {
			continue
		}
		normalized = append(normalized, line)
	}
	return strings.Join(normalized, "\n")
}

// findWorkspaceRoot finds the root of the Go module by looking for go.mod.
func findWorkspaceRoot(t *testing.T) string {
	t.Helper()

	// Try using go list first
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}")
	output, err := cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(output))
	}

	// Fallback: walk up from current directory
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("Could not find go.mod in any parent directory")
		}
		dir = parent
	}
}

// getGeneratedContent extracts the generated content from a protogen.Plugin response.
func getGeneratedContent(t *testing.T, p *protogen.Plugin) map[string]string {
	t.Helper()

	resp := p.Response()
	if resp.GetError() != "" {
		t.Fatalf("Plugin response error: %s", resp.GetError())
	}

	result := make(map[string]string)
	for _, file := range resp.File {
		if file.Content != nil {
			result[file.GetName()] = file.GetContent()
		}
	}
	return result
}

// mustFindFile finds a file in the plugin by path suffix.
func mustFindFile(t *testing.T, p *protogen.Plugin, pathSuffix string) *protogen.File {
	t.Helper()

	for _, f := range p.Files {
		if strings.HasSuffix(f.Desc.Path(), pathSuffix) {
			return f
		}
	}
	t.Fatalf("Could not find file with suffix %q", pathSuffix)
	return nil
}

// mustFindMessage finds a message in a file by name.
func mustFindMessage(t *testing.T, file *protogen.File, name string) *protogen.Message {
	t.Helper()

	for _, msg := range file.Messages {
		if string(msg.Desc.Name()) == name {
			return msg
		}
	}
	t.Fatalf("Could not find message %q in file %q", name, file.Desc.Path())
	return nil
}

// mustFindField finds a field in a message by name.
func mustFindField(t *testing.T, msg *protogen.Message, name string) *protogen.Field {
	t.Helper()

	for _, field := range msg.Fields {
		if string(field.Desc.Name()) == name {
			return field
		}
	}
	t.Fatalf("Could not find field %q in message %q", name, msg.Desc.Name())
	return nil
}

// tempDir creates a temporary directory for test artifacts.
func tempDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "protoc-gen-go-jsonschema-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	t.Cleanup(func() {
		os.RemoveAll(dir)
	})
	return dir
}
