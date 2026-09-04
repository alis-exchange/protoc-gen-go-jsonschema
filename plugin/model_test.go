package plugin

// In-package tests for the schema model: the plugin's internal seam. They
// assert on decided models — not on generated source text — using the
// checked-in descriptor set, so they need neither protoc nor the network.
// Generated-output shape and end-to-end behaviour are pinned by the golden
// and integration tests in plugin_test/.

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	optionsPb "go.alis.build/common/alis/open/options"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

// loadTestPlugin builds a protogen.Plugin from the checked-in descriptor set.
func loadTestPlugin(t *testing.T) *protogen.Plugin {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "testdata", "descriptors", "user.pb"))
	if err != nil {
		t.Fatalf("Failed to read checked-in descriptor set: %v", err)
	}
	var fds descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(data, &fds); err != nil {
		t.Fatalf("Failed to unmarshal descriptor set: %v", err)
	}

	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"users/v1/user.proto", "users/v1/common.proto", "users/v1/admin.proto", "users/v1/force.proto"},
		ProtoFile:      fds.File,
	}
	p, err := protogen.Options{}.New(req)
	if err != nil {
		t.Fatalf("Failed to create protogen.Plugin: %v", err)
	}
	return p
}

func findFile(t *testing.T, p *protogen.Plugin, pathSuffix string) *protogen.File {
	t.Helper()
	for _, f := range p.Files {
		if strings.HasSuffix(f.Desc.Path(), pathSuffix) {
			return f
		}
	}
	t.Fatalf("Could not find file with suffix %q", pathSuffix)
	return nil
}

func findMessage(t *testing.T, file *protogen.File, name string) *protogen.Message {
	t.Helper()
	for _, msg := range file.Messages {
		if string(msg.Desc.Name()) == name {
			return msg
		}
	}
	t.Fatalf("Could not find message %q in %q", name, file.Desc.Path())
	return nil
}

func findField(t *testing.T, msg *protogen.Message, name string) *protogen.Field {
	t.Helper()
	for _, field := range msg.Fields {
		if string(field.Desc.Name()) == name {
			return field
		}
	}
	t.Fatalf("Could not find field %q in message %q", name, msg.Desc.Name())
	return nil
}

// testCtx builds a schema context with the given file prefix and no cycle
// information: every message is treated as acyclic. Use msgCtx when the
// message under test (or a field's target) may sit on a cycle.
func testCtx(filePrefix string) *schemaContext {
	return &schemaContext{filePrefix: filePrefix}
}

// msgCtx builds a schema context whose cycle set is analysed from msg, so
// the model is exercised with real inline/$defs modes.
func msgCtx(filePrefix string, msg *protogen.Message) *schemaContext {
	return &schemaContext{filePrefix: filePrefix, cycles: analyzeCycles([]*protogen.Message{msg})}
}

func mustBuildFieldProperty(t *testing.T, field *protogen.Field, filePrefix string) propertyNode {
	t.Helper()
	prop, err := buildFieldProperty(field, msgCtx(filePrefix, field.Parent))
	if err != nil {
		t.Fatalf("buildFieldProperty failed: %v", err)
	}
	return prop
}

func mustBuildMessageSchema(t *testing.T, msg *protogen.Message, filePrefix string) *messageSchemaModel {
	t.Helper()
	m, err := buildMessageSchema(msg, msgCtx(filePrefix, msg))
	if err != nil {
		t.Fatalf("buildMessageSchema failed: %v", err)
	}
	return m
}

func TestEscapeGoString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty string", "", ""},
		{"simple string", "hello world", "hello world"},
		{"string with quotes", `say "hello"`, `say \"hello\"`},
		{"string with newline", "line1\nline2", `line1\nline2`},
		{"string with tab", "col1\tcol2", `col1\tcol2`},
		{"string with backslash", `path\to\file`, `path\\to\\file`},
		{"string with carriage return", "line1\r\nline2", `line1\r\nline2`},
		{"unicode characters", "Hello, 世界", "Hello, 世界"},
		{"mixed special characters", "\"Hello\"\n\tWorld\\!", `\"Hello\"\n\tWorld\\!`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := escapeGoString(tt.input); got != tt.expected {
				t.Errorf("escapeGoString(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestGetKindTypeNameAllKinds(t *testing.T) {
	p := loadTestPlugin(t)
	msg := findMessage(t, findFile(t, p, "user.proto"), "ComprehensiveUser")

	kindTests := []struct {
		fieldName    string
		expectedKind protoreflect.Kind
		expectedType string
	}{
		{"is_active", protoreflect.BoolKind, jsBoolean},
		{"age", protoreflect.Int32Kind, jsInteger},
		{"user_id", protoreflect.Int64Kind, jsInteger},
		{"score", protoreflect.Uint32Kind, jsInteger},
		{"account_number", protoreflect.Uint64Kind, jsInteger},
		{"signed_score", protoreflect.Sint32Kind, jsInteger},
		{"signed_id", protoreflect.Sint64Kind, jsInteger},
		{"fixed_uint", protoreflect.Fixed32Kind, jsInteger},
		{"fixed_ulong", protoreflect.Fixed64Kind, jsInteger},
		{"sfixed_int", protoreflect.Sfixed32Kind, jsInteger},
		{"sfixed_long", protoreflect.Sfixed64Kind, jsInteger},
		{"rating", protoreflect.FloatKind, jsNumber},
		{"balance", protoreflect.DoubleKind, jsNumber},
		{"id", protoreflect.StringKind, jsString},
		{"avatar", protoreflect.BytesKind, jsString},
		{"status", protoreflect.EnumKind, jsInteger},
		{"address", protoreflect.MessageKind, jsObject},
	}

	for _, tt := range kindTests {
		t.Run(tt.fieldName, func(t *testing.T) {
			field := findField(t, msg, tt.fieldName)
			if field.Desc.Kind() != tt.expectedKind {
				t.Fatalf("Field %s kind = %v, want %v", tt.fieldName, field.Desc.Kind(), tt.expectedKind)
			}
			typeName, err := getKindTypeName(field.Desc)
			if err != nil {
				t.Fatalf("getKindTypeName failed: %v", err)
			}
			if typeName != tt.expectedType {
				t.Errorf("getKindTypeName(%s) = %q, want %q", tt.fieldName, typeName, tt.expectedType)
			}
		})
	}
}

func TestGetTitleAndDescription(t *testing.T) {
	p := loadTestPlugin(t)
	file := findFile(t, p, "user.proto")

	tests := []struct {
		messageName  string
		descContains string
	}{
		{"Address", "Address represents a physical mailing address"},
		{"ComprehensiveUser", "ComprehensiveUser is a comprehensive user message"},
		{"User", "User represents a basic user account"},
	}

	for _, tt := range tests {
		t.Run(tt.messageName, func(t *testing.T) {
			msg := findMessage(t, file, tt.messageName)
			_, desc := getTitleAndDescription(msg.Desc)
			if desc == "" {
				t.Fatalf("Expected description for %s", tt.messageName)
			}
			if !strings.Contains(desc, tt.descContains) {
				t.Errorf("Description for %s = %q, should contain %q", tt.messageName, desc, tt.descContains)
			}
		})
	}
}

func TestGetEnumValues(t *testing.T) {
	p := loadTestPlugin(t)
	msg := findMessage(t, findFile(t, p, "user.proto"), "User")
	statusField := findField(t, msg, "status")

	enumValues := getEnumValues(statusField)
	expectedValues := []int32{0, 1, 2, 3, 4}
	if len(enumValues) != len(expectedValues) {
		t.Fatalf("Expected %d enum values, got %d", len(expectedValues), len(enumValues))
	}
	for i, expected := range expectedValues {
		if enumValues[i] != expected {
			t.Errorf("Enum value %d = %d, want %d", i, enumValues[i], expected)
		}
	}
}

func TestGetEnumValuesFromDescriptor(t *testing.T) {
	p := loadTestPlugin(t)
	msg := findMessage(t, findFile(t, p, "user.proto"), "MapFieldsDemo")
	enumMapField := findField(t, msg, "string_enum_map")

	mapValue := enumMapField.Desc.MapValue()
	if mapValue.Kind() != protoreflect.EnumKind {
		t.Fatalf("Expected enum kind for map value, got %v", mapValue.Kind())
	}

	enumValues := getEnumValuesFromDescriptor(mapValue.Enum())
	if len(enumValues) == 0 {
		t.Fatal("Expected enum values")
	}
	if enumValues[0] != any(int32(0)) {
		t.Errorf("First enum value = %v, want int32 0 (USER_STATUS_UNSPECIFIED)", enumValues[0])
	}
}

func TestIdentityFor(t *testing.T) {
	p := loadTestPlugin(t)
	userFile := findFile(t, p, "user.proto")

	t.Run("user message", func(t *testing.T) {
		id := identityFor(findMessage(t, userFile, "Address"), testCtx("user"))
		if id.isGoogle {
			t.Error("Address should not be a Google type")
		}
		if id.defKey != "users.v1.Address" {
			t.Errorf("defKey = %q, want users.v1.Address", id.defKey)
		}
		if id.withDefsName() != "Address_JsonSchema_WithDefs" {
			t.Errorf("withDefsName = %q", id.withDefsName())
		}
	})

	t.Run("google type gets file-prefixed standalone name", func(t *testing.T) {
		wkt := findMessage(t, userFile, "WellKnownTypesDemo")
		field := findField(t, wkt, "created_at")
		id := identityFor(field.Message, testCtx("user"))
		if !id.isGoogle {
			t.Error("google.protobuf.Timestamp should be a Google type")
		}
		if id.funcBase != "user_google_protobuf_Timestamp" {
			t.Errorf("funcBase = %q, want user_google_protobuf_Timestamp", id.funcBase)
		}
		if id.withDefsName() != "user_google_protobuf_Timestamp_JsonSchema_WithDefs" {
			t.Errorf("withDefsName = %q", id.withDefsName())
		}
	})
}

func TestIdentityForCyclic(t *testing.T) {
	p := loadTestPlugin(t)
	userFile := findFile(t, p, "user.proto")
	ctx := &schemaContext{filePrefix: "user", cycles: analyzeCycles(userFile.Messages)}

	if !identityFor(findMessage(t, userFile, "AddressDetails"), ctx).cyclic {
		t.Error("self-referencing AddressDetails must be cyclic ($defs mode)")
	}
	if identityFor(findMessage(t, userFile, "Address"), ctx).cyclic {
		t.Error("Address must not be cyclic (inline mode)")
	}
}

func TestBuildFreeFormWellKnownTypeFields(t *testing.T) {
	structT := ".google.protobuf.Struct"
	valueT := ".google.protobuf.Value"
	listT := ".google.protobuf.ListValue"

	t.Run("singular Struct inlines as an object", func(t *testing.T) {
		msg := dynMessage(t, dynOneFieldMessage(dynMsgField("s", 1, structT)))
		prop := mustBuildFieldProperty(t, findField(t, msg, "s"), "dyn")
		if prop.ref != nil {
			t.Fatal("Struct must inline, not reference a generated schema")
		}
		if prop.node == nil || prop.node.typeName != jsObject || prop.node.element != nil {
			t.Errorf("node = %+v, want free-form object", prop.node)
		}
	})

	t.Run("singular Value inlines as any JSON value", func(t *testing.T) {
		msg := dynMessage(t, dynOneFieldMessage(dynMsgField("v", 1, valueT)))
		prop := mustBuildFieldProperty(t, findField(t, msg, "v"), "dyn")
		if prop.ref != nil || prop.node == nil || prop.node.typeName != "" {
			t.Fatalf("prop = %+v, want an inline node without a single type", prop)
		}
		// An empty schema marshals as the boolean `true`, which strict clients
		// reject: "any value" is spelled out as the full JSON type list.
		if got := prop.node.types; len(got) != len(anyJSONTypes) {
			t.Errorf("types = %v, want every JSON type", got)
		}
	})

	t.Run("repeated Value has untyped items", func(t *testing.T) {
		msg := dynMessage(t, dynOneFieldMessage(dynRepeated(dynMsgField("vs", 1, valueT))))
		prop := mustBuildFieldProperty(t, findField(t, msg, "vs"), "dyn")
		if prop.node == nil || prop.node.typeName != jsArray || prop.node.element == nil {
			t.Fatalf("prop = %+v, want an array node with an element", prop)
		}
		if prop.node.element.ref != nil || prop.node.element.typeName != "" || len(prop.node.element.types) != len(anyJSONTypes) {
			t.Errorf("element = %+v, want the full JSON type list", prop.node.element)
		}
	})

	t.Run("map of Struct has object values", func(t *testing.T) {
		msg := dynMessage(t, dynMapOfMsg("M", structT))
		prop := mustBuildFieldProperty(t, findField(t, msg, "m"), "dyn")
		if prop.node == nil || prop.node.element == nil || !prop.node.elementIsAdditionalProperties {
			t.Fatalf("prop = %+v, want a map node", prop)
		}
		if prop.node.element.ref != nil || prop.node.element.typeName != jsObject {
			t.Errorf("element = %+v, want free-form object", prop.node.element)
		}
	})

	t.Run("oneof ListValue variant inlines as an array", func(t *testing.T) {
		f := dynMsgField("l", 1, listT)
		f.OneofIndex = proto.Int32(0)
		desc := dynOneFieldMessage(f)
		desc.OneofDecl = []*descriptorpb.OneofDescriptorProto{{Name: proto.String("choice")}}
		m := mustBuildMessageSchema(t, dynMessage(t, desc), "dyn")
		if len(m.oneofWrappers) != 1 || len(m.oneofWrappers[0].variants) != 1 {
			t.Fatalf("oneofWrappers = %+v, want one wrapper with one variant", m.oneofWrappers)
		}
		v := m.oneofWrappers[0].variants[0]
		if v.ref != nil || v.node == nil || v.node.typeName != jsArray {
			t.Errorf("variant = %+v, want free-form array", v)
		}
	})
}

func TestBuildScalarFieldNodes(t *testing.T) {
	p := loadTestPlugin(t)
	msg := findMessage(t, findFile(t, p, "user.proto"), "ComprehensiveUser")

	tests := []struct {
		fieldName      string
		expectedType   string
		expectBase64   bool
		expectEnumVals bool
	}{
		{"id", jsString, false, false},
		{"is_active", jsBoolean, false, false},
		{"age", jsInteger, false, false},
		{"user_id", jsInteger, false, false},
		{"rating", jsNumber, false, false},
		{"avatar", jsString, true, false},
		{"status", jsInteger, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.fieldName, func(t *testing.T) {
			field := findField(t, msg, tt.fieldName)
			prop := mustBuildFieldProperty(t, field, "user")
			if prop.node == nil {
				t.Fatal("Expected an inline node for a scalar field")
			}
			if prop.node.typeName != tt.expectedType {
				t.Errorf("typeName = %q, want %q", prop.node.typeName, tt.expectedType)
			}
			if got := prop.node.value.contentEncoding == "base64"; got != tt.expectBase64 {
				t.Errorf("base64 encoding = %v, want %v", got, tt.expectBase64)
			}
			if got := len(prop.node.value.enum) > 0; got != tt.expectEnumVals {
				t.Errorf("enum values present = %v, want %v", got, tt.expectEnumVals)
			}
		})
	}
}

func TestBuildMessageFieldIsRef(t *testing.T) {
	p := loadTestPlugin(t)
	msg := findMessage(t, findFile(t, p, "user.proto"), "ComprehensiveUser")
	field := findField(t, msg, "address")

	prop := mustBuildFieldProperty(t, field, "user")
	if prop.ref == nil {
		t.Fatal("Message-type field should decide to a ref")
	}
	if prop.ref.defKey != "users.v1.Address" {
		t.Errorf("ref defKey = %q, want users.v1.Address", prop.ref.defKey)
	}
	// Any node alongside the ref carries only sibling keywords, never a
	// structural schema.
	if prop.node != nil && (prop.node.typeName != "" || prop.node.element != nil) {
		t.Error("Ref siblings must not carry structural schema keywords")
	}
}

func TestBuildRefSiblingsFromOptions(t *testing.T) {
	p := loadTestPlugin(t)
	msg := findMessage(t, findFile(t, p, "user.proto"), "ConstraintDemo")
	field := findField(t, msg, "shipping_address")

	prop := mustBuildFieldProperty(t, field, "user")
	if prop.ref == nil {
		t.Fatal("shipping_address should decide to a ref")
	}
	if prop.node == nil {
		t.Fatal("shipping_address has a description option; expected ref siblings")
	}
	if !strings.Contains(prop.node.description, "Shipping address") {
		t.Errorf("sibling description = %q, want the option override", prop.node.description)
	}
}

func TestBuildRefSiblingsReplaceMetadataOnInlineTargets(t *testing.T) {
	p := loadTestPlugin(t)
	userFile := findFile(t, p, "user.proto")

	t.Run("inline target: field comment replaces title and description", func(t *testing.T) {
		wkt := findMessage(t, userFile, "WellKnownTypesDemo")
		prop := mustBuildFieldProperty(t, findField(t, wkt, "created_at"), "user")
		if prop.ref == nil || prop.node == nil {
			t.Fatalf("created_at should be a decorated reference, got %+v", prop)
		}
		if !prop.node.replaceMetadata {
			t.Error("a field comment on an inline target must replace the target's own title/description")
		}
	})

	t.Run("cyclic target: metadata stays $ref siblings", func(t *testing.T) {
		details := findMessage(t, userFile, "AddressDetails")
		prop := mustBuildFieldProperty(t, findField(t, details, "nested_address_details"), "user")
		if prop.ref == nil || !prop.ref.cyclic || prop.node == nil {
			t.Fatalf("nested_address_details should reference the cyclic AddressDetails, got %+v", prop)
		}
		if prop.node.replaceMetadata {
			t.Error("siblings of a $ref must not be marked as replacing metadata")
		}
	})

	t.Run("inline target without field metadata keeps the target's own", func(t *testing.T) {
		// Dynamic descriptors carry no comments: the field contributes nothing.
		file := dynFile(t, dynMsg("P", dynMsgField("t", 1, "T")), dynMsg("T"))
		prop := mustBuildFieldProperty(t, findField(t, findMessage(t, file, "P"), "t"), "dyn")
		if prop.node != nil {
			t.Errorf("no field metadata: expected a bare reference, got %+v", prop.node)
		}
	})
}

func TestBuildArrayFieldNodes(t *testing.T) {
	p := loadTestPlugin(t)
	msg := findMessage(t, findFile(t, p, "user.proto"), "RepeatedFieldsDemo")

	tests := []struct {
		fieldName    string
		elementType  string
		expectBase64 bool
		expectEnum   bool
		expectRef    bool
	}{
		{"string_list", jsString, false, false, false},
		{"int_list", jsInteger, false, false, false},
		{"long_list", jsInteger, false, false, false},
		{"bool_list", jsBoolean, false, false, false},
		{"bytes_list", jsString, true, false, false},
		{"enum_list", jsInteger, false, true, false},
		{"message_list", "", false, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.fieldName, func(t *testing.T) {
			field := findField(t, msg, tt.fieldName)
			prop := mustBuildFieldProperty(t, field, "user")
			if prop.node == nil || prop.node.typeName != jsArray {
				t.Fatalf("Expected an array node")
			}
			el := prop.node.element
			if el == nil {
				t.Fatal("Expected an element node")
			}
			if prop.node.elementIsAdditionalProperties {
				t.Error("Array element should print under Items, not AdditionalProperties")
			}
			if tt.expectRef {
				if el.ref == nil {
					t.Fatal("Expected element ref for message elements")
				}
				return
			}
			if el.typeName != tt.elementType {
				t.Errorf("element typeName = %q, want %q", el.typeName, tt.elementType)
			}
			if got := el.value.contentEncoding == "base64"; got != tt.expectBase64 {
				t.Errorf("element base64 = %v, want %v", got, tt.expectBase64)
			}
			if got := len(el.value.enum) > 0; got != tt.expectEnum {
				t.Errorf("element enum present = %v, want %v", got, tt.expectEnum)
			}
		})
	}
}

func TestBuildMapFieldNodes(t *testing.T) {
	p := loadTestPlugin(t)
	msg := findMessage(t, findFile(t, p, "user.proto"), "MapFieldsDemo")

	tests := []struct {
		fieldName            string
		propertyNamesPattern string
		elementType          string
		expectEnum           bool
		expectRef            bool
	}{
		{"string_map", "", jsString, false, false},
		{"string_int_map", "", jsInteger, false, false},
		{"string_bool_map", "", jsBoolean, false, false},
		{"string_enum_map", "", jsInteger, true, false},
		{"string_message_map", "", "", false, true},
		{"int_string_map", "^-?[0-9]+$", jsString, false, false},
		{"bool_string_map", "^(true|false)$", jsString, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.fieldName, func(t *testing.T) {
			field := findField(t, msg, tt.fieldName)
			prop := mustBuildFieldProperty(t, field, "user")
			if prop.node == nil || prop.node.typeName != jsObject {
				t.Fatalf("Expected an object node for a map field")
			}
			if prop.node.propertyNamesPattern != tt.propertyNamesPattern {
				t.Errorf("propertyNamesPattern = %q, want %q", prop.node.propertyNamesPattern, tt.propertyNamesPattern)
			}
			el := prop.node.element
			if el == nil {
				t.Fatal("Expected an element node")
			}
			if !prop.node.elementIsAdditionalProperties {
				t.Error("Map values should print under AdditionalProperties")
			}
			if tt.expectRef {
				if el.ref == nil {
					t.Fatal("Expected element ref for message values")
				}
				return
			}
			if el.typeName != tt.elementType {
				t.Errorf("element typeName = %q, want %q", el.typeName, tt.elementType)
			}
			if got := len(el.value.enum) > 0; got != tt.expectEnum {
				t.Errorf("element enum present = %v, want %v", got, tt.expectEnum)
			}
		})
	}
}

func TestBuildMessageSchemaOneofShape(t *testing.T) {
	p := loadTestPlugin(t)
	msg := findMessage(t, findFile(t, p, "user.proto"), "ComprehensiveUser")

	m := mustBuildMessageSchema(t, msg, "user")

	if len(m.constraintGroups) != 0 {
		t.Errorf("User message without declared groups should have no root constraint groups, got %d", len(m.constraintGroups))
	}
	wantWrappers := []string{"Identifier", "PaymentMethod", "ContactPreference"}
	if len(m.oneofWrappers) != len(wantWrappers) {
		t.Fatalf("Expected %d oneof wrappers, got %d", len(wantWrappers), len(m.oneofWrappers))
	}
	for i, want := range wantWrappers {
		if m.oneofWrappers[i].key != want {
			t.Errorf("wrapper %d key = %q, want %q", i, m.oneofWrappers[i].key, want)
		}
	}

	// Message variants decide to refs; scalar variants to inline nodes.
	contact := m.oneofWrappers[2]
	for _, variant := range contact.variants {
		if variant.ref == nil {
			t.Errorf("ContactPreference variant %q should be a ref", variant.key)
		}
	}
	ident := m.oneofWrappers[0]
	for _, variant := range ident.variants {
		if variant.node == nil {
			t.Errorf("Identifier variant %q should be an inline node", variant.key)
		}
	}

	// Oneof members must not appear as flat field properties.
	for _, prop := range m.fields {
		if prop.key == "email" || prop.key == "contact_info" {
			t.Errorf("Oneof member %q leaked into flat field properties", prop.key)
		}
	}
}

func TestBuildMessageSchemaGoogleOneofsUseWrappers(t *testing.T) {
	// Google types without a custom json.Marshaler marshal exactly like user
	// messages under encoding/json: their proto oneofs become PascalCase
	// wrapper properties, never flat members with root-level constraints.
	p := loadTestPlugin(t)
	iamFile := findFile(t, p, "google/iam/admin/v1/iam.proto")
	lint := findMessage(t, iamFile, "LintPolicyRequest")

	m := mustBuildMessageSchema(t, lint, "common")

	if len(m.constraintGroups) != 0 {
		t.Errorf("Google proto oneofs must not emit root-level constraint groups, got %d", len(m.constraintGroups))
	}
	if len(m.oneofWrappers) != 1 || m.oneofWrappers[0].key != "LintObject" {
		t.Fatalf("expected one wrapper keyed LintObject, got %+v", m.oneofWrappers)
	}
	if v := m.oneofWrappers[0].variants; len(v) != 1 || v[0].key != "Condition" || v[0].ref == nil {
		t.Errorf("expected a single Condition message variant, got %+v", v)
	}
	for _, prop := range m.fields {
		if prop.key == "condition" {
			t.Error("oneof member condition must not be a flat property")
		}
	}
}

func TestBuildMessageSchemaRequired(t *testing.T) {
	p := loadTestPlugin(t)
	msg := findMessage(t, findFile(t, p, "user.proto"), "User")

	m := mustBuildMessageSchema(t, msg, "user")

	required := make(map[string]bool)
	for _, f := range m.required {
		required[f] = true
	}
	// Singular non-optional fields are required.
	if !required["id"] {
		t.Error("id should be required")
	}
	// Repeated and map fields are never required.
	for _, field := range msg.Fields {
		if (field.Desc.IsList() || field.Desc.IsMap()) && required[getFieldName(field)] {
			t.Errorf("repeated/map field %q must not be required", getFieldName(field))
		}
	}
}

func TestGetMessagesWithForce(t *testing.T) {
	p := loadTestPlugin(t)
	file := findFile(t, p, "user.proto")

	messageHasExplicitGenerateFalse := func(msg *protogen.Message) bool {
		opts := getMessageJsonSchemaOptions(msg)
		return opts != nil && opts.Generate != nil && !*opts.Generate
	}

	t.Run("collects messages and skips map entries", func(t *testing.T) {
		messages := getMessagesWithForce(file.Messages, true, false, make(map[string]bool))
		if len(messages) == 0 {
			t.Fatal("Expected messages")
		}
		names := make(map[string]bool)
		for _, msg := range messages {
			if msg.Desc.IsMapEntry() {
				t.Errorf("Map entry %s should be filtered out", msg.Desc.Name())
			}
			names[string(msg.Desc.Name())] = true
		}
		for _, want := range []string{"User", "Address", "ComprehensiveUser", "ContactInfo"} {
			if !names[want] {
				t.Errorf("Expected message %s to be included", want)
			}
		}
	})

	t.Run("visited map prevents reprocessing", func(t *testing.T) {
		visited := make(map[string]bool)
		first := getMessagesWithForce(file.Messages, true, false, visited)
		second := getMessagesWithForce(file.Messages, true, false, visited)
		if len(first) == 0 {
			t.Error("Expected messages on first call")
		}
		if len(second) != 0 {
			t.Errorf("Expected 0 messages on second call, got %d", len(second))
		}
	})

	t.Run("force=true ignores explicit generate=false on nested messages", func(t *testing.T) {
		parent := findMessage(t, file, "Address")
		if len(parent.Messages) == 0 {
			t.Fatal("Address should have nested messages")
		}
		withForce := getMessagesWithForce(parent.Messages, true, true, make(map[string]bool))
		found := false
		for _, msg := range withForce {
			if strings.Contains(string(msg.Desc.FullName()), "AddressDetails") {
				found = true
			}
		}
		if !found {
			t.Error("Nested AddressDetails should be included when force=true")
		}
	})

	t.Run("force=false respects explicit generate=false", func(t *testing.T) {
		messages := getMessagesWithForce(file.Messages, false, false, make(map[string]bool))
		for _, msg := range messages {
			if messageHasExplicitGenerateFalse(msg) {
				t.Errorf("Message %s with generate=false should not be included when force=false", msg.Desc.Name())
			}
		}
	})
}

// -----------------------------------------------------------------------------
// Dynamic descriptors — invalid option combinations cannot live in testdata
// (they abort generation), so these tests build one-message protos in memory,
// without protoc.
// -----------------------------------------------------------------------------

// dynMessage compiles an in-memory proto file holding the given message (plus
// a Color enum with values 0 and 1 for enum-typed fields) and returns the
// protogen form of that message.
func dynMessage(t *testing.T, msg *descriptorpb.DescriptorProto) *protogen.Message {
	t.Helper()
	return findMessage(t, dynFileWithEnum(t, msg), msg.GetName())
}

// dynField builds a singular field descriptor with optional json_schema options.
func dynField(name string, number int32, typ descriptorpb.FieldDescriptorProto_Type, js *optionsPb.FieldOptions_JsonSchema) *descriptorpb.FieldDescriptorProto {
	f := &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(name),
		Number:   proto.Int32(number),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:     typ.Enum(),
		JsonName: proto.String(name),
	}
	switch typ {
	case descriptorpb.FieldDescriptorProto_TYPE_ENUM:
		f.TypeName = proto.String(".dyn.v1.Color")
	case descriptorpb.FieldDescriptorProto_TYPE_MESSAGE:
		f.TypeName = proto.String(".dyn.v1.M")
	}
	if js != nil {
		f.Options = &descriptorpb.FieldOptions{}
		proto.SetExtension(f.Options, optionsPb.E_Field, &optionsPb.FieldOptions{JsonSchema: js})
	}
	return f
}

// dynOneFieldMessage wraps one field into message "M".
func dynOneFieldMessage(field *descriptorpb.FieldDescriptorProto) *descriptorpb.DescriptorProto {
	return &descriptorpb.DescriptorProto{
		Name:  proto.String("M"),
		Field: []*descriptorpb.FieldDescriptorProto{field},
	}
}

func TestGetMessagesWithForceSkipsFreeFormWellKnownTypes(t *testing.T) {
	p := loadTestPlugin(t)
	userFile := findFile(t, p, "user.proto")
	wkt := findMessage(t, userFile, "WellKnownTypesDemo")

	collected := make(map[string]bool)
	for _, m := range getMessagesWithForce([]*protogen.Message{wkt}, true, false, make(map[string]bool)) {
		collected[string(m.Desc.FullName())] = true
	}
	for _, name := range []string{"google.protobuf.Struct", "google.protobuf.Value", "google.protobuf.ListValue"} {
		if collected[name] {
			t.Errorf("%s must not be collected: it inlines as free-form JSON", name)
		}
	}
	if !collected["google.protobuf.Timestamp"] {
		t.Error("google.protobuf.Timestamp is structural and must still be collected")
	}
}

func TestValidateFieldOptionsFreeFormFields(t *testing.T) {
	structT := ".google.protobuf.Struct"
	valueT := ".google.protobuf.Value"
	listT := ".google.protobuf.ListValue"
	withOpts := func(f *descriptorpb.FieldDescriptorProto, js *optionsPb.FieldOptions_JsonSchema) *descriptorpb.FieldDescriptorProto {
		f.Options = &descriptorpb.FieldOptions{}
		proto.SetExtension(f.Options, optionsPb.E_Field, &optionsPb.FieldOptions{JsonSchema: js})
		return f
	}
	tests := []struct {
		name    string
		field   *descriptorpb.FieldDescriptorProto
		wantErr string // substring; empty means the options are accepted
	}{
		{"Value accepts default_string (any JSON value)", withOpts(dynMsgField("v", 1, valueT), &optionsPb.FieldOptions_JsonSchema{DefaultString: proto.String("x")}), ""},
		{"Value accepts enum_string", withOpts(dynMsgField("v", 1, valueT), &optionsPb.FieldOptions_JsonSchema{EnumString: []string{"a"}}), ""},
		{"Value accepts multiple_of", withOpts(dynMsgField("v", 1, valueT), &optionsPb.FieldOptions_JsonSchema{MultipleOf: proto.Float64(2)}), ""},
		{"ListValue rejects default_string as an array", withOpts(dynMsgField("l", 1, listT), &optionsPb.FieldOptions_JsonSchema{DefaultString: proto.String("x")}), `"array"`},
		{"Struct rejects default_string as an object", withOpts(dynMsgField("s", 1, structT), &optionsPb.FieldOptions_JsonSchema{DefaultString: proto.String("x")}), "singular scalar"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := dynMessage(t, dynOneFieldMessage(tt.field))
			err := validateFieldOptions(msg.Fields[0])
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("unexpected error: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Errorf("error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateFieldOptionsErrors(t *testing.T) {
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING
	i64 := descriptorpb.FieldDescriptorProto_TYPE_INT64
	enum := descriptorpb.FieldDescriptorProto_TYPE_ENUM
	msgT := descriptorpb.FieldDescriptorProto_TYPE_MESSAGE
	dbl := descriptorpb.FieldDescriptorProto_TYPE_DOUBLE

	repeated := func(f *descriptorpb.FieldDescriptorProto) *descriptorpb.FieldDescriptorProto {
		f.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
		return f
	}

	tests := []struct {
		name    string
		field   *descriptorpb.FieldDescriptorProto
		wantErr string
	}{
		{
			"two enum variants",
			dynField("f", 1, str, &optionsPb.FieldOptions_JsonSchema{EnumString: []string{"a"}, EnumInt: []int64{1}}),
			"at most one enum_* variant",
		},
		{
			"enum variant type mismatch",
			dynField("f", 1, str, &optionsPb.FieldOptions_JsonSchema{EnumInt: []int64{1}}),
			"does not match the field's JSON type",
		},
		{
			"enum_int not a subset of the proto enum",
			dynField("f", 1, enum, &optionsPb.FieldOptions_JsonSchema{EnumInt: []int64{5}}),
			"not a declared value",
		},
		{
			"multiple_of on non-numeric field",
			dynField("f", 1, str, &optionsPb.FieldOptions_JsonSchema{MultipleOf: proto.Float64(2)}),
			"numeric fields only",
		},
		{
			"multiple_of must be positive",
			dynField("f", 1, i64, &optionsPb.FieldOptions_JsonSchema{MultipleOf: proto.Float64(0)}),
			"greater than 0",
		},
		{
			"exclusive_minimum without minimum",
			dynField("f", 1, i64, &optionsPb.FieldOptions_JsonSchema{ExclusiveMinimum: proto.Bool(true)}),
			"requires minimum",
		},
		{
			"default variant type mismatch",
			dynField("f", 1, i64, &optionsPb.FieldOptions_JsonSchema{DefaultString: proto.String("x")}),
			"does not match the field's JSON type",
		},
		{
			"two default variants",
			dynField("f", 1, i64, &optionsPb.FieldOptions_JsonSchema{DefaultInt: proto.Int64(1), DefaultBool: proto.Bool(true)}),
			"at most one default_* variant",
		},
		{
			"default on repeated field",
			repeated(dynField("f", 1, str, &optionsPb.FieldOptions_JsonSchema{DefaultString: proto.String("x")})),
			"singular scalar",
		},
		{
			"default on message field",
			dynField("f", 1, msgT, &optionsPb.FieldOptions_JsonSchema{DefaultString: proto.String("x")}),
			"singular scalar",
		},
		{
			"examples on repeated field",
			repeated(dynField("f", 1, str, &optionsPb.FieldOptions_JsonSchema{ExamplesString: []string{"x"}})),
			"singular scalar",
		},
		// protoc accepts inf/-inf/nan for double options; none has a JSON
		// form and the printer would emit them as Go identifiers.
		{
			"minimum must be finite",
			dynField("f", 1, dbl, &optionsPb.FieldOptions_JsonSchema{Minimum: proto.Float64(math.Inf(1))}),
			"minimum must be a finite number",
		},
		{
			"maximum must be finite",
			dynField("f", 1, dbl, &optionsPb.FieldOptions_JsonSchema{Maximum: proto.Float64(math.NaN())}),
			"maximum must be a finite number",
		},
		{
			"multiple_of NaN is rejected before the range check",
			dynField("f", 1, dbl, &optionsPb.FieldOptions_JsonSchema{MultipleOf: proto.Float64(math.NaN())}),
			"multiple_of must be a finite number",
		},
		{
			"default_number must be finite",
			dynField("f", 1, dbl, &optionsPb.FieldOptions_JsonSchema{DefaultNumber: proto.Float64(math.Inf(-1))}),
			"default_number must be a finite number",
		},
		{
			"enum_number must be finite",
			dynField("f", 1, dbl, &optionsPb.FieldOptions_JsonSchema{EnumNumber: []float64{1, math.NaN()}}),
			"enum_number must be a finite number",
		},
		{
			"examples_number must be finite",
			dynField("f", 1, dbl, &optionsPb.FieldOptions_JsonSchema{ExamplesNumber: []float64{math.Inf(1)}}),
			"examples_number must be a finite number",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := dynMessage(t, dynOneFieldMessage(tt.field))
			_, err := buildFieldProperty(msg.Fields[0], testCtx("dyn"))
			if err == nil {
				t.Fatalf("Expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestNewFieldOptionsHappyPaths(t *testing.T) {
	i64 := descriptorpb.FieldDescriptorProto_TYPE_INT64
	enum := descriptorpb.FieldDescriptorProto_TYPE_ENUM
	msgT := descriptorpb.FieldDescriptorProto_TYPE_MESSAGE

	t.Run("presence makes minimum zero expressible", func(t *testing.T) {
		msg := dynMessage(t, dynOneFieldMessage(
			dynField("f", 1, i64, &optionsPb.FieldOptions_JsonSchema{Minimum: proto.Float64(0)})))
		prop := mustBuildFieldProperty(t, msg.Fields[0], "dyn")
		if prop.node.value.minimum == nil || *prop.node.value.minimum != 0 {
			t.Errorf("minimum = %v, want explicit 0", prop.node.value.minimum)
		}
	})

	t.Run("enum_int subset replaces the auto-derived list", func(t *testing.T) {
		msg := dynMessage(t, dynOneFieldMessage(
			dynField("f", 1, enum, &optionsPb.FieldOptions_JsonSchema{EnumInt: []int64{1}})))
		prop := mustBuildFieldProperty(t, msg.Fields[0], "dyn")
		if len(prop.node.value.enum) != 1 || prop.node.value.enum[0] != any(int64(1)) {
			t.Errorf("enum = %v, want [int64(1)]", prop.node.value.enum)
		}
	})

	t.Run("annotations ride message-field refs as siblings", func(t *testing.T) {
		msg := dynMessage(t, dynOneFieldMessage(
			dynField("f", 1, msgT, &optionsPb.FieldOptions_JsonSchema{Deprecated: proto.Bool(true)})))
		prop := mustBuildFieldProperty(t, msg.Fields[0], "dyn")
		if prop.ref == nil {
			t.Fatal("message field should decide to a ref")
		}
		if prop.node == nil || !prop.node.deprecated {
			t.Error("deprecated annotation should ride the $ref as a sibling")
		}
	})

	t.Run("default and examples land on the node", func(t *testing.T) {
		msg := dynMessage(t, dynOneFieldMessage(
			dynField("f", 1, i64, &optionsPb.FieldOptions_JsonSchema{
				DefaultInt:  proto.Int64(7),
				ExamplesInt: []int64{1, 2},
				MultipleOf:  proto.Float64(1),
			})))
		prop := mustBuildFieldProperty(t, msg.Fields[0], "dyn")
		if prop.node.defaultValue != any(int64(7)) {
			t.Errorf("defaultValue = %v, want int64(7)", prop.node.defaultValue)
		}
		if len(prop.node.examples) != 2 {
			t.Errorf("examples = %v, want two values", prop.node.examples)
		}
		if prop.node.value.multipleOf == nil || *prop.node.value.multipleOf != 1 {
			t.Errorf("multipleOf = %v, want 1", prop.node.value.multipleOf)
		}
	})
}

// dynMessageWithOneofOption builds message "M" with two string fields a/b and
// the given json_schema.oneof declarations.
func dynMessageWithOneofOption(t *testing.T, groups []*optionsPb.MessageOptions_JsonSchema_Oneof, mutate func(*descriptorpb.DescriptorProto)) *protogen.Message {
	t.Helper()
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING
	msg := &descriptorpb.DescriptorProto{
		Name: proto.String("M"),
		Field: []*descriptorpb.FieldDescriptorProto{
			dynField("a", 1, str, nil),
			dynField("b", 2, str, nil),
		},
	}
	if mutate != nil {
		mutate(msg)
	}
	msg.Options = &descriptorpb.MessageOptions{}
	proto.SetExtension(msg.Options, optionsPb.E_Message, &optionsPb.MessageOptions{
		JsonSchema: &optionsPb.MessageOptions_JsonSchema{Oneof: groups},
	})
	return dynMessage(t, msg)
}

func TestDeclaredOneofGroups(t *testing.T) {
	t.Run("virtual group excludes members from required and builds presence nodes", func(t *testing.T) {
		msg := dynMessageWithOneofOption(t, []*optionsPb.MessageOptions_JsonSchema_Oneof{
			{Fields: []string{"a", "b"}, Required: proto.Bool(true)},
		}, nil)
		m := mustBuildMessageSchema(t, msg, "dyn")

		if len(m.required) != 0 {
			t.Errorf("group members must leave the plain required list, got %v", m.required)
		}
		if len(m.constraintGroups) != 1 {
			t.Fatalf("Expected 1 constraint group, got %d", len(m.constraintGroups))
		}
		group := m.constraintGroups[0]
		if !group.required {
			t.Error("required: true should carry through")
		}
		if len(group.members) != 2 || group.members[0].fieldName != "a" || group.members[1].fieldName != "b" {
			t.Errorf("members = %+v, want virtual presence nodes for a and b", group.members)
		}
	})

	t.Run("real proto-oneof members use wrapper presence", func(t *testing.T) {
		msg := dynMessageWithOneofOption(t, []*optionsPb.MessageOptions_JsonSchema_Oneof{
			{Fields: []string{"a", "b"}, Required: proto.Bool(true)},
		}, func(d *descriptorpb.DescriptorProto) {
			d.OneofDecl = []*descriptorpb.OneofDescriptorProto{{Name: proto.String("sel")}}
			d.Field[0].OneofIndex = proto.Int32(0)
			d.Field[1].OneofIndex = proto.Int32(0)
			d.Field[0].Label = descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
		})
		m := mustBuildMessageSchema(t, msg, "dyn")

		if len(m.constraintGroups) != 1 {
			t.Fatalf("Expected 1 constraint group, got %d", len(m.constraintGroups))
		}
		member := m.constraintGroups[0].members[0]
		if member.wrapperKey != "Sel" || member.variantKey != "A" {
			t.Errorf("member = %+v, want wrapper Sel / variant A", member)
		}
	})

	errorTests := []struct {
		name    string
		groups  []*optionsPb.MessageOptions_JsonSchema_Oneof
		mutate  func(*descriptorpb.DescriptorProto)
		wantErr string
	}{
		{
			"unknown field",
			[]*optionsPb.MessageOptions_JsonSchema_Oneof{{Fields: []string{"a", "nope"}}},
			nil,
			"unknown field",
		},
		{
			"fewer than two members",
			[]*optionsPb.MessageOptions_JsonSchema_Oneof{{Fields: []string{"a"}}},
			nil,
			"at least two fields",
		},
		{
			"repeated member",
			[]*optionsPb.MessageOptions_JsonSchema_Oneof{{Fields: []string{"a", "b"}}},
			func(d *descriptorpb.DescriptorProto) {
				d.Field[0].Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
			},
			"repeated or map",
		},
		{
			"member in two groups",
			[]*optionsPb.MessageOptions_JsonSchema_Oneof{
				{Fields: []string{"a", "b"}},
				{Fields: []string{"a", "b"}},
			},
			nil,
			"only appear once",
		},
	}

	for _, tt := range errorTests {
		t.Run(tt.name, func(t *testing.T) {
			msg := dynMessageWithOneofOption(t, tt.groups, tt.mutate)
			_, err := buildMessageSchema(msg, testCtx("dyn"))
			if err == nil {
				t.Fatalf("Expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestBuildNameIsUnexported(t *testing.T) {
	p := loadTestPlugin(t)
	userFile := findFile(t, p, "user.proto")
	ctx := &schemaContext{filePrefix: "user", cycles: analyzeCycles(userFile.Messages)}

	var nested *protogen.Message
	for _, m := range findMessage(t, userFile, "Address").Messages {
		if m.Desc.Name() == "AddressDetails" {
			nested = m
		}
	}
	if nested == nil {
		t.Fatal("fixture drift: Address.AddressDetails not found")
	}

	tests := []struct {
		msg  *protogen.Message
		want string
	}{
		{findMessage(t, userFile, "AddressDetails"), "addressDetails_JsonSchema_build"},
		{nested, "address_AddressDetails_JsonSchema_build"},
	}
	for _, tt := range tests {
		got := identityFor(tt.msg, ctx).buildName()
		if got != tt.want {
			t.Errorf("buildName(%s) = %q, want %q", tt.msg.Desc.FullName(), got, tt.want)
		}
		if r, _ := utf8.DecodeRuneInString(got); !unicode.IsLower(r) {
			t.Errorf("buildName(%s) = %q must be unexported: it is not generated API", tt.msg.Desc.FullName(), got)
		}
	}
}

func TestBuildGroupFieldsReferenceTheGroupMessage(t *testing.T) {
	// proto2 groups: protoc-gen-go emits a nested message type and a pointer
	// field, so the schema treats them exactly like message fields.
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING
	record := dynMsg("Record",
		dynGroupField("attributes", 1, "Record.Attributes"),
		dynRepeated(dynGroupField("tags", 2, "Record.Tags")),
	)
	record.NestedType = []*descriptorpb.DescriptorProto{
		dynMsg("Attributes", dynField("key", 1, str, nil)),
		dynMsg("Tags", dynField("name", 1, str, nil)),
	}
	file := dynFileCustom(t, "dyn/v1/dyn.proto", "proto2", record)
	m := mustBuildMessageSchema(t, findMessage(t, file, "Record"), "dyn")
	if len(m.fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(m.fields))
	}

	attrs := m.fields[0]
	if attrs.key != "attributes" || attrs.ref == nil || attrs.ref.defKey != "dyn.v1.Record.Attributes" || attrs.ref.funcBase != "Record_Attributes" {
		t.Errorf("singular group must reference the nested group message, got key=%q ref=%+v", attrs.key, attrs.ref)
	}
	tags := m.fields[1]
	if tags.ref != nil || tags.node == nil || tags.node.typeName != jsArray ||
		tags.node.element == nil || tags.node.element.ref == nil || tags.node.element.ref.defKey != "dyn.v1.Record.Tags" {
		t.Errorf("repeated group must be an array whose items reference the group message, got %+v", tags)
	}
}

func TestPrefixFromPathSanitizes(t *testing.T) {
	tests := map[string]string{
		"users/v1/admin.proto": "admin",
		"snake_case.proto":     "snake_case",
		"sd/v1/my-d.proto":     "my_d",
		"auth/2fa.proto":       "_2fa",
		"foo.bar.proto":        "foo_bar",
	}
	for path, want := range tests {
		if got := prefixFromPath(path); got != want {
			t.Errorf("prefixFromPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestGenerateAcceptsNonIdentifierFileNames(t *testing.T) {
	// Google-type function names carry the file prefix. A base name that is
	// not a Go identifier fragment used to make protogen reject the whole
	// generated file as unparsable.
	event := dynMsg("Event", dynMsgField("when", 1, ".google.protobuf.Timestamp"))
	event.Options = &descriptorpb.MessageOptions{}
	proto.SetExtension(event.Options, optionsPb.E_Message, &optionsPb.MessageOptions{
		JsonSchema: &optionsPb.MessageOptions_JsonSchema{Generate: proto.Bool(true)},
	})
	p := dynPluginCustom(t, "sd/v1/my-d.proto", "proto3", event)
	if err := Generate(p, "test"); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	resp := p.Response()
	if resp.GetError() != "" {
		t.Fatalf("generated source rejected: %s", resp.GetError())
	}
	var content string
	for _, f := range resp.File {
		if strings.HasSuffix(f.GetName(), "my-d_jsonschema.pb.go") {
			content = f.GetContent()
		}
	}
	if !strings.Contains(content, "func my_d_google_protobuf_Timestamp_JsonSchema()") {
		t.Errorf("expected a sanitised Google-type function name in:\n%s", content)
	}
}

func TestGetMessagesWithForceSkipsIgnoredFields(t *testing.T) {
	// An ignored field is not a reference: neither its user message nor a
	// Google type reached only through it is forced to generate.
	file := dynFile(t,
		dynMsg("Root",
			dynIgnored(dynMsgField("hidden", 1, "Internal")),
			dynIgnored(dynMsgField("when", 2, ".google.protobuf.Timestamp")),
		),
		dynMsg("Internal"),
	)
	got := getMessagesWithForce([]*protogen.Message{findMessage(t, file, "Root")}, true, false, make(map[string]bool))
	var names []string
	for _, m := range got {
		names = append(names, string(m.Desc.FullName()))
	}
	if strings.Join(names, ",") != "dyn.v1.Root" {
		t.Errorf("expected only Root to be collected, got %v", names)
	}
}

func TestCollectTargetsForcesCrossFileDependencies(t *testing.T) {
	p := loadTestPlugin(t)
	targets := collectTargets(p)

	// force.proto's ParentWithCrossFileDependency references a generate=false
	// message defined in common.proto: the request-wide collection targets
	// it, so common.proto generates it.
	if !targets["users.v1.CrossFileDependency"] {
		t.Error("CrossFileDependency must be targeted by the request-wide collection")
	}
	if targets["users.v1.IgnoredDependency"] {
		t.Error("IgnoredDependency is reachable only through an ignored field and must not be targeted")
	}

	common := findFile(t, p, "common.proto")
	gen := &Generator{Version: "test"}
	if src := generatedSource(t, gen, p, common, targets); !strings.Contains(src, "func (x *CrossFileDependency) JsonSchema()") {
		t.Error("common.proto must generate the dependency a sibling file forced")
	}
	if src := generatedSource(t, gen, p, common, targetSet{}); strings.Contains(src, "CrossFileDependency") {
		t.Error("without a forcing sibling, an opted-out message must not generate")
	}
}

// generatedSource runs generateFile and returns the formatted output, failing
// the test if protogen cannot parse it.
func generatedSource(t *testing.T, gen *Generator, p *protogen.Plugin, file *protogen.File, targets targetSet) string {
	t.Helper()
	g, err := gen.generateFile(p, file, targets)
	if err != nil {
		t.Fatalf("generateFile(%s) failed: %v", file.Desc.Path(), err)
	}
	if g == nil {
		return ""
	}
	content, err := g.Content()
	if err != nil {
		t.Fatalf("generated source for %s does not parse: %v", file.Desc.Path(), err)
	}
	return string(content)
}
