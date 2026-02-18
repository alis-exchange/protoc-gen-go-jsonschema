//go:build plugintest

package plugin

import (
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// SchemaFieldConfigResult is the exported result type for schema field configuration.
// Used by tests; schemaFieldConfig remains unexported.
type SchemaFieldConfigResult struct {
	FieldName            string
	Title                string
	Description          string
	TypeName             string
	IsBytes              bool
	Pattern              string
	PropertyNamesPattern string
	EnumValues           []int32
	MessageRef           string
	Nested               *SchemaFieldConfigResult
}

// TestingHelper exposes internal plugin functionality for testing.
// Do not use in production code.
type TestingHelper interface {
	EscapeGoString(s string) string
	GetTitleAndDescription(desc protoreflect.Descriptor) (title, description string)
	GetMessages(messages []*protogen.Message, defaultGenerate bool, visited map[string]bool) []*protogen.Message
	GetMessagesWithForce(messages []*protogen.Message, defaultGenerate bool, force bool, visited map[string]bool) []*protogen.Message
	GenerateFile(plugin *protogen.Plugin, file *protogen.File) (*protogen.GeneratedFile, error)
	GetKindTypeName(desc protoreflect.FieldDescriptor) (string, error)
	GetEnumValues(field *protogen.Field) []int32
	GetEnumValuesFromDescriptor(enumDesc protoreflect.EnumDescriptor) []int32
	GetMessageSchemaConfig(message *protogen.Message) SchemaFieldConfigResult
	GetScalarSchemaConfig(field *protogen.Field, title, desc string) SchemaFieldConfigResult
	GetArraySchemaConfig(field *protogen.Field, title, desc string) SchemaFieldConfigResult
	GetMapSchemaConfig(field *protogen.Field, title, desc string) SchemaFieldConfigResult
	ReferenceName(msg *protogen.Message) string
	// MessageHasExplicitGenerateFalse returns true if the message has generate=false option.
	MessageHasExplicitGenerateFalse(message *protogen.Message) bool
}

// testingHelper implements TestingHelper by delegating to Generator and MessageSchemaGenerator.
type testingHelper struct {
	gr *Generator
	sg *MessageSchemaGenerator
}

var _ TestingHelper = (*testingHelper)(nil)

func (t *testingHelper) EscapeGoString(s string) string {
	return t.gr.escapeGoString(s)
}

func (t *testingHelper) GetTitleAndDescription(desc protoreflect.Descriptor) (string, string) {
	return t.gr.getTitleAndDescription(desc)
}

func (t *testingHelper) GetMessages(messages []*protogen.Message, defaultGenerate bool, visited map[string]bool) []*protogen.Message {
	return t.gr.getMessages(messages, defaultGenerate, visited)
}

func (t *testingHelper) GetMessagesWithForce(messages []*protogen.Message, defaultGenerate bool, force bool, visited map[string]bool) []*protogen.Message {
	return t.gr.getMessagesWithForce(messages, defaultGenerate, force, visited)
}

func (t *testingHelper) GenerateFile(plugin *protogen.Plugin, file *protogen.File) (*protogen.GeneratedFile, error) {
	return t.gr.generateFile(plugin, file)
}

func (t *testingHelper) GetKindTypeName(desc protoreflect.FieldDescriptor) (string, error) {
	return t.sg.getKindTypeName(desc)
}

func (t *testingHelper) GetEnumValues(field *protogen.Field) []int32 {
	return t.sg.getEnumValues(field)
}

func (t *testingHelper) GetEnumValuesFromDescriptor(enumDesc protoreflect.EnumDescriptor) []int32 {
	return t.sg.getEnumValuesFromDescriptor(enumDesc)
}

func (t *testingHelper) GetMessageSchemaConfig(message *protogen.Message) SchemaFieldConfigResult {
	return schemaFieldConfigToResult(t.sg.getMessageSchemaConfig(message))
}

func (t *testingHelper) GetScalarSchemaConfig(field *protogen.Field, title, desc string) SchemaFieldConfigResult {
	return schemaFieldConfigToResult(t.sg.getScalarSchemaConfig(field, title, desc))
}

func (t *testingHelper) GetArraySchemaConfig(field *protogen.Field, title, desc string) SchemaFieldConfigResult {
	return schemaFieldConfigToResult(t.sg.getArraySchemaConfig(field, title, desc))
}

func (t *testingHelper) GetMapSchemaConfig(field *protogen.Field, title, desc string) SchemaFieldConfigResult {
	return schemaFieldConfigToResult(t.sg.getMapSchemaConfig(field, title, desc))
}

func (t *testingHelper) ReferenceName(msg *protogen.Message) string {
	return t.sg.referenceName(msg)
}

func (t *testingHelper) MessageHasExplicitGenerateFalse(message *protogen.Message) bool {
	opts := getMessageJsonSchemaOptions(message)
	return opts != nil && !opts.GetGenerate()
}

func schemaFieldConfigToResult(cfg schemaFieldConfig) SchemaFieldConfigResult {
	res := SchemaFieldConfigResult{
		FieldName:            cfg.fieldName,
		Title:                cfg.title,
		Description:          cfg.description,
		TypeName:             cfg.typeName,
		IsBytes:              cfg.isBytes,
		Pattern:              cfg.pattern,
		PropertyNamesPattern: cfg.propertyNamesPattern,
		EnumValues:           cfg.enumValues,
		MessageRef:           cfg.messageRef,
	}
	if cfg.nested != nil {
		n := schemaFieldConfigToResult(*cfg.nested)
		res.Nested = &n
	}
	return res
}

// NewTestingHelper creates a TestingHelper for the given plugin and file.
// Requires the plugintest build tag. Returns an error if generateFile fails.
func NewTestingHelper(plugin *protogen.Plugin, file *protogen.File) (TestingHelper, error) {
	gr := &Generator{}
	genFile, err := gr.generateFile(plugin, file)
	if err != nil {
		return nil, err
	}
	sg := &MessageSchemaGenerator{
		gr:         gr,
		gen:        genFile,
		visited:    make(map[string]bool),
		filePrefix: fileNamePrefix(file),
	}
	return &testingHelper{gr: gr, sg: sg}, nil
}
