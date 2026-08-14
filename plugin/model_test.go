package plugin

// In-package tests for the schema model: the plugin's internal seam. They
// assert on decided models — not on generated source text — using the
// checked-in descriptor set, so they need neither protoc nor the network.
// Generated-output shape and end-to-end behaviour are pinned by the golden
// and integration tests in plugin_test/.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		FileToGenerate: []string{"users/v1/user.proto", "users/v1/common.proto", "users/v1/admin.proto"},
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

func mustBuildFieldProperty(t *testing.T, field *protogen.Field, filePrefix string) propertyNode {
	t.Helper()
	prop, err := buildFieldProperty(field, filePrefix)
	if err != nil {
		t.Fatalf("buildFieldProperty failed: %v", err)
	}
	return prop
}

func mustBuildMessageSchema(t *testing.T, msg *protogen.Message, filePrefix string) *messageSchemaModel {
	t.Helper()
	m, err := buildMessageSchema(msg, filePrefix)
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
	if enumValues[0] != 0 {
		t.Errorf("First enum value = %d, want 0 (USER_STATUS_UNSPECIFIED)", enumValues[0])
	}
}

func TestIdentityFor(t *testing.T) {
	p := loadTestPlugin(t)
	userFile := findFile(t, p, "user.proto")

	t.Run("user message", func(t *testing.T) {
		id := identityFor(findMessage(t, userFile, "Address"), "user")
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
		id := identityFor(field.Message, "user")
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

	if len(m.flatGroups) != 0 {
		t.Errorf("User message should have no flat oneof groups, got %d", len(m.flatGroups))
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

func TestBuildMessageSchemaGoogleFlatOneofs(t *testing.T) {
	p := loadTestPlugin(t)

	// google.protobuf.Value (imported via struct.proto) has the 'kind' oneof.
	structFile := findFile(t, p, "google/protobuf/struct.proto")
	value := findMessage(t, structFile, "Value")

	m := mustBuildMessageSchema(t, value, "admin")

	if len(m.oneofWrappers) != 0 {
		t.Errorf("Google type should have no nested oneof wrappers, got %d", len(m.oneofWrappers))
	}
	if len(m.flatGroups) != 1 {
		t.Fatalf("Expected 1 flat oneof group, got %d", len(m.flatGroups))
	}
	group := m.flatGroups[0]
	if group.name != "kind" {
		t.Errorf("group name = %q, want kind", group.name)
	}
	found := false
	for _, f := range group.fields {
		if f == "null_value" {
			found = true
		}
	}
	if !found {
		t.Error("Expected null_value in the flat oneof group")
	}

	// Oneof members stay flat properties for Google types.
	flat := false
	for _, prop := range m.fields {
		if prop.key == "number_value" {
			flat = true
		}
	}
	if !flat {
		t.Error("Google type oneof member number_value should remain a flat property")
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
		return opts != nil && !opts.GetGenerate()
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
