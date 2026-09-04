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

// updateGolden is a flag to update golden files instead of comparing against them.
// Usage: go test -update
var updateGolden = flag.Bool("update", false, "update golden files")

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
	return createTestPluginWithParams(t, fds, filesToGenerate, "")
}

// createTestPluginWithParams is createTestPlugin with a protoc plugin
// parameter string (e.g. "paths=source_relative,Mfoo.proto=example.com/foo"),
// which protogen applies to import paths and output file names.
func createTestPluginWithParams(t *testing.T, fds *descriptorpb.FileDescriptorSet, filesToGenerate []string, params string) *protogen.Plugin {
	t.Helper()

	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: filesToGenerate,
		ProtoFile:      fds.File,
	}
	if params != "" {
		req.Parameter = &params
	}

	opts := protogen.Options{}
	p, err := opts.New(req)
	if err != nil {
		t.Fatalf("Failed to create protogen.Plugin: %v", err)
	}

	return p
}

// requireProtoc skips the test if protoc is not installed.
func requireProtoc(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("protoc"); err != nil {
		t.Skip("protoc not found in PATH")
	}
}

// protoPaths returns the --proto_path directories used for every protoc
// invocation: the testdata protos plus the vendored third-party protos
// (alis options, google/iam and friends). All imports resolve from the repo
// itself — no machine-specific paths.
func protoPaths(workspaceRoot string) []string {
	return []string{
		filepath.Join(workspaceRoot, "testdata", "protos"),
		filepath.Join(workspaceRoot, "third_party", "protos"),
	}
}

// buildDescriptorSet runs protoc over protoFiles (paths relative to
// testdata/protos) and returns the parsed FileDescriptorSet.
func buildDescriptorSet(t *testing.T, workspaceRoot string, protoFiles []string, outputPath string) *descriptorpb.FileDescriptorSet {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		t.Fatalf("Failed to create output directory: %v", err)
	}

	args := []string{
		"--descriptor_set_out=" + outputPath,
		"--include_imports",
		"--include_source_info",
	}
	for _, path := range protoPaths(workspaceRoot) {
		args = append(args, "--proto_path="+path)
	}
	args = append(args, protoFiles...)

	cmd := exec.Command("protoc", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to run protoc: %v\nOutput: %s\nArgs: %v", err, output, args)
	}

	return loadDescriptorSet(t, outputPath)
}

// discoverProtoDirs walks testdata/protos and groups .proto files by the
// directory they live in, keyed by directory (e.g. "users/v1") with values
// relative to testdata/protos. Files in one directory share a Go package and
// must be compiled together so cross-file references resolve.
func discoverProtoDirs(t *testing.T, workspaceRoot string) map[string][]string {
	t.Helper()

	rootDir := filepath.Join(workspaceRoot, "testdata", "protos")
	groups := make(map[string][]string)
	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".proto") {
			return nil
		}
		relPath, err := filepath.Rel(rootDir, path)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)
		dir := filepath.ToSlash(filepath.Dir(relPath))
		groups[dir] = append(groups[dir], relPath)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to walk proto directory: %v", err)
	}
	return groups
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

// extractGoFuncSection returns the source of a top-level Go function from content,
// from "func name(" through its closing brace. Returns empty string if not found.
func extractGoFuncSection(content, funcName string) string {
	needle := "func " + funcName + "("
	start := strings.Index(content, needle)
	if start < 0 {
		return ""
	}
	braceStart := strings.Index(content[start:], "{")
	if braceStart < 0 {
		return ""
	}
	braceStart += start
	depth := 0
	for i := braceStart; i < len(content); i++ {
		switch content[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return content[start : i+1]
			}
		}
	}
	return ""
}

// userStubTypes returns Go source for stub types that satisfy the generated
// jsonschema code for users/v1. Integration tests that compile generated code
// in a temp module use this instead of maintaining their own copy.
func userStubTypes() string {
	return `package usersv1

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
type ConstraintDemo struct{}
type OneOfDemo struct{}
type WellKnownTypesDemo struct{}
type Common struct{}
type Admin struct{}
type CompleteInterviewRequest struct{}
type CompleteInterviewRequest_InterviewSummary struct{}
type ParentWithNested struct{}
type ParentWithNested_NestedMessage struct{}
type ParentWithForcedNested struct{}
type ParentWithForcedNested_ForcedNested struct{}
type ParentWithForcedDependency struct{}
type DependencyMessage struct{}
type ParentWithCrossFileDependency struct{}
type CrossFileDependency struct{}
type ParentWithIgnoredDependency struct{}
`
}

// oneofDemoRealTypes returns Go source for a faithful OneOfDemo type that mirrors
// the real protoc-gen-go output: oneof interface fields tagged only with
// `protobuf_oneof` (no json tag) and variant wrapper structs whose fields carry a
// `protobuf` tag but no json tag. This lets round-trip tests marshal a real value
// and validate the resulting document against the generated schema. Callers must
// remove the empty `type OneOfDemo struct{}` stub from userStubTypes() first.
//
// Only Field1's string variant is given a concrete type; the other oneof groups
// just need their interface types declared so an unset (all-nil) value marshals to
// {"Field1":null,"Field2":null,"Field3":null,"Field4":null}.
func oneofDemoRealTypes() string {
	return `package usersv1

type OneOfDemo struct {
	Field1 isOneOfDemo_Field1 ` + "`protobuf_oneof:\"field1\"`" + `
	Field2 isOneOfDemo_Field2 ` + "`protobuf_oneof:\"field2\"`" + `
	Field3 isOneOfDemo_Field3 ` + "`protobuf_oneof:\"field3\"`" + `
	Field4 isOneOfDemo_Field4 ` + "`protobuf_oneof:\"field4\"`" + `
}

type isOneOfDemo_Field1 interface{ isOneOfDemo_Field1() }
type isOneOfDemo_Field2 interface{ isOneOfDemo_Field2() }
type isOneOfDemo_Field3 interface{ isOneOfDemo_Field3() }
type isOneOfDemo_Field4 interface{ isOneOfDemo_Field4() }

type OneOfDemo_StringValue struct {
	StringValue string ` + "`protobuf:\"bytes,1,opt,name=string_value,json=stringValue,proto3,oneof\"`" + `
}

func (*OneOfDemo_StringValue) isOneOfDemo_Field1() {}
`
}

// schemaTestHelpersSource returns Go source for the schema-validation helpers
// shared by every compile-and-run test that builds a temp module. Written once
// here so each temp module gets the same ValidateSchema/collectRefs behaviour
// instead of carrying its own embedded copy.
func schemaTestHelpersSource(pkgName string) string {
	return "package " + pkgName + `

import (
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// ValidateSchema validates a *jsonschema.Schema and returns a resolved schema
// that can be used for validation. It ensures:
//   - The schema is not nil
//   - The root is a literal "object" schema, never a $ref
//   - Every $ref points to an existing definition
//   - The schema structure is valid (via Resolve)
func ValidateSchema(schema *jsonschema.Schema) (*jsonschema.Resolved, error) {
	return ValidateSchemaWithName("schema", schema)
}

// ValidateSchemaWithName is ValidateSchema with the schema name included in
// error messages for better debugging.
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

	for _, prop := range schema.Properties {
		for ref := range collectRefs(prop) {
			refs[ref] = true
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

	for _, branch := range schema.OneOf {
		for ref := range collectRefs(branch) {
			refs[ref] = true
		}
	}

	for _, def := range schema.Defs {
		for ref := range collectRefs(def) {
			refs[ref] = true
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
`
}

// writeSchemaTestHelpers writes the shared validation helpers into a temp
// module package directory.
func writeSchemaTestHelpers(t *testing.T, pkgDir, pkgName string) {
	t.Helper()
	path := filepath.Join(pkgDir, "schema_test_helpers.go")
	if err := os.WriteFile(path, []byte(schemaTestHelpersSource(pkgName)), 0o644); err != nil {
		t.Fatalf("Failed to write schema test helpers: %v", err)
	}
}
