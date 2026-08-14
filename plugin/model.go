package plugin

import (
	"fmt"
	"sort"
	"strings"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/reflect/protoreflect"
	optionsPb "open.alis.services/protobuf/alis/open/options/v1"
)

// -----------------------------------------------------------------------------
// JSON Schema Type Constants
// -----------------------------------------------------------------------------
//
// These constants represent the primitive type names defined in JSON Schema Draft 2020-12.
// See: https://json-schema.org/draft/2020-12/json-schema-validation.html#section-6.1.1
const (
	jsArray   = "array"   // JSON array type - used for repeated fields
	jsBoolean = "boolean" // JSON boolean type - used for bool fields
	jsInteger = "integer" // JSON integer type - used for 32-bit integer fields
	jsNull    = "null"    // JSON null type - used in nullable type unions
	jsNumber  = "number"  // JSON number type - used for float/double fields
	jsObject  = "object"  // JSON object type - used for messages and maps
	jsString  = "string"  // JSON string type - used for strings and bytes
)

// -----------------------------------------------------------------------------
// Message Identity
// -----------------------------------------------------------------------------

// messageIdentity answers every naming and strategy question about a message in
// one place: its $defs key, the base name of its generated functions, whether
// JsonSchema() is a method (user types) or a standalone function (Google
// types), and whether its oneofs keep the flat Google shape. It is computed
// once per message and consumed by both the model builder and the printer.
type messageIdentity struct {
	// defKey is the "$defs" map key, e.g. "users.v1.User".
	defKey string

	// funcBase is the base of the generated function names: the Go type name
	// for user messages ("User"), or the file-prefixed full name for Google
	// types ("admin_google_protobuf_Timestamp"). Function names append
	// "_JsonSchema" / "_JsonSchema_WithDefs".
	funcBase string

	// goName is the Go type name, used as the method receiver for user types.
	goName string

	// protoName is the proto message name, used in generated doc comments.
	protoName string

	// importPath qualifies cross-package _WithDefs calls for user types.
	importPath protogen.GoImportPath

	// isGoogle is true for messages in google.* packages: they get standalone
	// functions (methods cannot be added to imported types) and keep flat
	// oneof properties (their custom json.Marshalers use proto JSON semantics).
	isGoogle bool
}

// identityFor computes the identity of a message in the context of the proto
// file currently being generated (filePrefix keeps Google type function names
// unique when multiple files in one package import the same types).
func identityFor(msg *protogen.Message, filePrefix string) messageIdentity {
	id := messageIdentity{
		defKey:     string(msg.Desc.FullName()),
		goName:     msg.GoIdent.GoName,
		protoName:  string(msg.Desc.Name()),
		importPath: msg.GoIdent.GoImportPath,
		isGoogle:   isGoogleType(msg),
	}
	if id.isGoogle {
		id.funcBase = googleTypeFunctionName(msg, filePrefix)
	} else {
		id.funcBase = msg.GoIdent.GoName
	}
	return id
}

// withDefsName returns the name of the generated _WithDefs helper function.
func (id messageIdentity) withDefsName() string {
	return id.funcBase + "_JsonSchema_WithDefs"
}

// isGoogleType checks if a message is from a Google package (google.*).
// This includes well-known types (google.protobuf.*), common types (google.type.*),
// API types (google.api.*), IAM types (google.iam.*), and any other google.* packages.
func isGoogleType(msg *protogen.Message) bool {
	return strings.HasPrefix(string(msg.Desc.FullName()), "google.")
}

// googleTypeFunctionName converts a Google type's full name to a valid Go function name with a file prefix.
// Example: "google.protobuf.Timestamp" with prefix "admin" -> "admin_google_protobuf_Timestamp"
func googleTypeFunctionName(msg *protogen.Message, filePrefix string) string {
	fullName := string(msg.Desc.FullName())
	baseName := strings.ReplaceAll(fullName, ".", "_")
	if filePrefix != "" {
		return filePrefix + "_" + baseName
	}
	return baseName
}

// fileNamePrefix extracts a prefix from the proto file path for use in Google type function names.
// Example: "users/v1/admin.proto" -> "admin"
func fileNamePrefix(file *protogen.File) string {
	path := file.Desc.Path()
	base := strings.TrimSuffix(path, ".proto")
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	return base
}

// -----------------------------------------------------------------------------
// Schema Model
// -----------------------------------------------------------------------------
//
// The model is the symbolic form of one message's generated schema code: every
// keyword the generated Go will set, with all option overrides already applied.
// Ref-valued positions carry the target message's identity instead of a
// literal; the printer turns them into _WithDefs calls.

// valueConstraints are the keywords that constrain a single JSON value. They
// apply at the root for scalar fields and to the element for arrays and maps.
type valueConstraints struct {
	format           string
	pattern          string
	contentEncoding  string
	contentMediaType string
	exclusiveMinimum *float64
	minimum          *float64
	exclusiveMaximum *float64
	maximum          *float64
	minLength        *int64
	maxLength        *int64
	enum             []int32
}

// elementNode is the schema of an array's items or a map's values: either a
// reference to another message's schema, or an inline value schema.
type elementNode struct {
	// ref, when set, prints as a _WithDefs call; all other fields are ignored.
	ref *messageIdentity

	// typeName is the JSON Schema type of the element. When empty (external
	// types without explicit type info), "object" is emitted as a fallback.
	typeName string

	value valueConstraints
}

// fieldNode is the decided schema for one emitted &jsonschema.Schema{...}
// literal: a field property, or a oneof variant's inner value schema.
type fieldNode struct {
	// typeName is the JSON Schema type; empty omits the Type keyword.
	typeName string

	// title and description are emitted only when non-empty.
	title       string
	description string

	// Container constraints from field options.
	minItems      *int64
	maxItems      *int64
	uniqueItems   bool
	minProperties *int64
	maxProperties *int64

	// value constrains the root value for scalar fields (unused when element
	// is set — constraints then live on the element).
	value valueConstraints

	// element is the items/additionalProperties schema for arrays and maps.
	element *elementNode

	// elementIsAdditionalProperties selects the AdditionalProperties keyword
	// (maps) over Items (arrays).
	elementIsAdditionalProperties bool

	// propertyNamesPattern validates map keys serialized as strings (integer
	// and boolean proto map keys).
	propertyNamesPattern string
}

// propertyNode is one `schema.Properties[key] = ...` assignment: a reference
// to another message's schema, an inline field schema, or — when both are set
// — a reference decorated with sibling keywords (Draft 2020-12 keeps keywords
// next to $ref applicable).
type propertyNode struct {
	key  string
	ref  *messageIdentity
	node *fieldNode
}

// oneofVariantNode is one variant branch inside a user-message oneof wrapper.
type oneofVariantNode struct {
	// key is the variant's JSON key: field.GoName (PascalCase), because
	// protoc-gen-go emits no json tags on oneof wrapper structs.
	key string

	// ref is set for message variants ($ref to the message's schema).
	ref *messageIdentity

	// node is the inline schema for scalar variants; for message variants it
	// optionally carries sibling keywords alongside the $ref.
	node *fieldNode
}

// oneofWrapperNode is the nested PascalCase wrapper property emitted for each
// non-synthetic oneof of a user message. The wrapper is a oneOf of a null
// branch (encoding/json emits the wrapper key as null when the oneof is unset)
// and an object branch holding the variant oneOf.
type oneofWrapperNode struct {
	// key is the wrapper property key: oneof.GoName (PascalCase).
	key      string
	variants []oneofVariantNode
}

// flatOneofGroup is one oneof group of a Google type, emitted as flat
// root-level constraints (Required alternatives plus a none-set branch).
type flatOneofGroup struct {
	name   string
	fields []string
}

// messageSchemaModel is the complete decided schema for one message: what the
// generated JsonSchema()/_WithDefs functions will build at runtime.
type messageSchemaModel struct {
	id messageIdentity

	// title and description come from message comments; emitted only when
	// non-empty (unlike field-level metadata).
	title       string
	description string

	// required lists field names in the schema's Required array, in field
	// order.
	required []string

	// fields are the property assignments for regular fields, in field order.
	fields []propertyNode

	// flatGroups holds Google-type oneof groups (sorted by group name); one
	// group emits schema.OneOf, several emit schema.AllOf.
	flatGroups []flatOneofGroup

	// oneofWrappers holds user-message oneof wrapper properties, in oneof
	// declaration order.
	oneofWrappers []oneofWrapperNode
}

// -----------------------------------------------------------------------------
// Model Builder
// -----------------------------------------------------------------------------

// buildMessageSchema turns one message plus its options into a complete schema
// model. Every schema decision — type mapping, option application, required
// membership, oneof shape — happens here; the printer only transcribes.
// An unsupported field kind aborts generation with an error rather than
// silently emitting a broken schema.
func buildMessageSchema(message *protogen.Message, filePrefix string) (*messageSchemaModel, error) {
	id := identityFor(message, filePrefix)
	title, description := getTitleAndDescription(message.Desc)

	m := &messageSchemaModel{
		id:          id,
		title:       title,
		description: description,
	}

	// --- Required Fields ---
	// A field is required only if it's a singular scalar/message field that is
	// not optional. Fields in oneofs, marked optional, repeated (arrays), or
	// maps are not required. An unset message field still marshals as an
	// omitted key under encoding/json's omitempty tags; that known tension is
	// accepted — the schema states the proto3 contract.
	for _, field := range message.Fields {
		opts := getFieldJsonSchemaOptions(field)
		if opts.GetIgnore() {
			continue
		}
		if field.Oneof == nil && !field.Desc.HasOptionalKeyword() && !field.Desc.IsList() && !field.Desc.IsMap() {
			m.required = append(m.required, getFieldName(field))
		}
	}

	// --- Field Properties ---
	// Google types keep flat oneof behavior (custom json.Marshalers use proto
	// JSON semantics): their oneof members are emitted as regular flat
	// properties and collected into flatGroups. User messages skip oneof
	// members here — they become nested PascalCase wrappers below.
	oneofGroups := make(map[string][]string) // only populated for Google types
	for _, field := range message.Fields {
		opts := getFieldJsonSchemaOptions(field)
		if opts.GetIgnore() {
			continue
		}

		if oneof := field.Oneof; oneof != nil && !oneof.Desc.IsSynthetic() {
			if !id.isGoogle {
				continue
			}
			groupName := string(oneof.Desc.Name())
			oneofGroups[groupName] = append(oneofGroups[groupName], getFieldName(field))
		}

		prop, err := buildFieldProperty(field, filePrefix)
		if err != nil {
			return nil, err
		}
		m.fields = append(m.fields, prop)
	}

	if id.isGoogle && len(oneofGroups) > 0 {
		var groupNames []string
		for name := range oneofGroups {
			groupNames = append(groupNames, name)
		}
		sort.Strings(groupNames)
		for _, name := range groupNames {
			m.flatGroups = append(m.flatGroups, flatOneofGroup{name: name, fields: oneofGroups[name]})
		}
	}

	// --- User Message Oneof Wrappers ---
	if !id.isGoogle {
		for _, oneof := range message.Oneofs {
			if oneof.Desc.IsSynthetic() {
				continue
			}
			wrapper, ok, err := buildOneofWrapper(oneof, filePrefix)
			if err != nil {
				return nil, err
			}
			if ok {
				m.oneofWrappers = append(m.oneofWrappers, wrapper)
			}
		}
	}

	return m, nil
}

// buildFieldProperty decides the schema for a single field: a reference for
// message-typed fields, or an inline schema for everything else.
func buildFieldProperty(field *protogen.Field, filePrefix string) (propertyNode, error) {
	title, description := getTitleAndDescription(field.Desc)

	switch {
	case field.Desc.IsList():
		node, err := buildArrayNode(field, title, description, filePrefix)
		if err != nil {
			return propertyNode{}, err
		}
		return propertyNode{key: getFieldName(field), node: node}, nil
	case field.Desc.IsMap():
		node, err := buildMapNode(field, title, description, filePrefix)
		if err != nil {
			return propertyNode{}, err
		}
		return propertyNode{key: getFieldName(field), node: node}, nil
	case field.Desc.Kind() == protoreflect.MessageKind:
		// Message-type fields use a direct $ref for structure. Field comments
		// and field-level options emit as $ref siblings.
		id := identityFor(field.Message, filePrefix)
		return propertyNode{key: getFieldName(field), ref: &id, node: buildRefSiblings(field, title, description)}, nil
	default:
		node, err := buildScalarNode(field, title, description)
		if err != nil {
			return propertyNode{}, err
		}
		return propertyNode{key: getFieldName(field), node: node}, nil
	}
}

// buildScalarNode decides the inline schema for a singular non-message field.
func buildScalarNode(field *protogen.Field, title, description string) (*fieldNode, error) {
	opts := getFieldJsonSchemaOptions(field)
	kindTypeName, err := getKindTypeName(field.Desc)
	if err != nil {
		return nil, fmt.Errorf("field %s: %w", field.Desc.FullName(), err)
	}

	node := &fieldNode{typeName: kindTypeName}
	applyMetadata(node, title, description, opts)
	applyContainerOptions(node, opts)

	var base valueConstraints
	switch field.Desc.Kind() {
	case protoreflect.EnumKind:
		base.enum = getEnumValues(field)
	case protoreflect.BytesKind:
		base.contentEncoding = "base64"
	}
	node.value = applyValueOptions(base, opts)
	return node, nil
}

// buildArrayNode decides the inline schema for a repeated field: an "array"
// root with the element schema under Items. Value-level options apply to the
// element; container options apply to the root.
func buildArrayNode(field *protogen.Field, title, description string, filePrefix string) (*fieldNode, error) {
	opts := getFieldJsonSchemaOptions(field)

	node := &fieldNode{typeName: jsArray}
	applyMetadata(node, title, description, opts)
	applyContainerOptions(node, opts)
	element, err := buildElement(field.Desc, field, field.Message, opts, filePrefix)
	if err != nil {
		return nil, fmt.Errorf("field %s: %w", field.Desc.FullName(), err)
	}
	node.element = element
	return node, nil
}

// buildMapNode decides the inline schema for a map field: an "object" root
// with the value schema under AdditionalProperties and, for non-string keys, a
// PropertyNames pattern (JSON serializes all map keys as strings).
func buildMapNode(field *protogen.Field, title, description string, filePrefix string) (*fieldNode, error) {
	opts := getFieldJsonSchemaOptions(field)

	node := &fieldNode{typeName: jsObject, elementIsAdditionalProperties: true}
	applyMetadata(node, title, description, opts)
	applyContainerOptions(node, opts)

	switch field.Desc.MapKey().Kind() {
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Uint32Kind,
		protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Uint64Kind,
		protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind:
		node.propertyNamesPattern = "^-?[0-9]+$"
	case protoreflect.BoolKind:
		node.propertyNamesPattern = "^(true|false)$"
	}

	// The value message of a map lives on field 2 of the synthetic map entry.
	var valMsg *protogen.Message
	for _, f := range field.Message.Fields {
		if f.Desc.Number() == 2 {
			valMsg = f.Message
			break
		}
	}
	element, err := buildElement(field.Desc.MapValue(), nil, valMsg, opts, filePrefix)
	if err != nil {
		return nil, fmt.Errorf("field %s: %w", field.Desc.FullName(), err)
	}
	node.element = element
	if node.element == nil {
		// Message value with no resolvable message: constraints fall back to
		// the root, matching the historical output.
		node.value = applyValueOptions(valueConstraints{}, opts)
	}
	return node, nil
}

// buildElement decides the JSON shape of one proto value — the single shared
// answer for array elements and map values. enumField carries the
// protogen.Field when available (array elements); map values fall back to the
// descriptor's enum. msg is the element's message when kind is MessageKind.
func buildElement(desc protoreflect.FieldDescriptor, enumField *protogen.Field, msg *protogen.Message, opts *optionsPb.FieldOptions_JsonSchema, filePrefix string) (*elementNode, error) {
	kindTypeName, err := getKindTypeName(desc)
	if err != nil {
		return nil, err
	}

	switch desc.Kind() {
	case protoreflect.MessageKind:
		if msg == nil {
			return nil, nil
		}
		id := identityFor(msg, filePrefix)
		return &elementNode{ref: &id}, nil

	case protoreflect.EnumKind:
		var enum []int32
		if enumField != nil {
			enum = getEnumValues(enumField)
		} else {
			enum = getEnumValuesFromDescriptor(desc.Enum())
		}
		return &elementNode{typeName: kindTypeName, value: applyValueOptions(valueConstraints{enum: enum}, opts)}, nil

	case protoreflect.BytesKind:
		return &elementNode{typeName: kindTypeName, value: applyValueOptions(valueConstraints{contentEncoding: "base64"}, opts)}, nil

	default:
		return &elementNode{typeName: kindTypeName, value: applyValueOptions(valueConstraints{}, opts)}, nil
	}
}

// buildOneofWrapper decides the nested wrapper schema for one user-message
// oneof. Returns ok=false when every variant is ignored.
func buildOneofWrapper(oneof *protogen.Oneof, filePrefix string) (oneofWrapperNode, bool, error) {
	wrapper := oneofWrapperNode{key: oneof.GoName}
	for _, field := range oneof.Fields {
		opts := getFieldJsonSchemaOptions(field)
		if opts.GetIgnore() {
			continue
		}
		variant := oneofVariantNode{key: field.GoName}
		title, description := getTitleAndDescription(field.Desc)
		if field.Desc.Kind() == protoreflect.MessageKind {
			// Message variant: direct $ref with siblings, same rule as
			// buildFieldProperty.
			id := identityFor(field.Message, filePrefix)
			variant.ref = &id
			variant.node = buildRefSiblings(field, title, description)
		} else {
			node, err := buildScalarNode(field, title, description)
			if err != nil {
				return oneofWrapperNode{}, false, err
			}
			variant.node = node
		}
		wrapper.variants = append(wrapper.variants, variant)
	}
	if len(wrapper.variants) == 0 {
		return oneofWrapperNode{}, false, nil
	}
	return wrapper, true, nil
}

// buildRefSiblings decides the keywords that ride alongside a message-typed
// field's $ref: comment/option metadata plus any option constraints. Returns
// nil when the field contributes nothing beyond the $ref itself.
func buildRefSiblings(field *protogen.Field, title, description string) *fieldNode {
	opts := getFieldJsonSchemaOptions(field)
	node := &fieldNode{}
	applyMetadata(node, title, description, opts)
	applyContainerOptions(node, opts)
	node.value = applyValueOptions(valueConstraints{}, opts)
	if node.isEmpty() {
		return nil
	}
	return node
}

// isEmpty reports whether a node carries no keywords at all.
func (n *fieldNode) isEmpty() bool {
	return n.typeName == "" && n.title == "" && n.description == "" &&
		n.minItems == nil && n.maxItems == nil && !n.uniqueItems &&
		n.minProperties == nil && n.maxProperties == nil &&
		n.element == nil && n.propertyNamesPattern == "" &&
		n.value.isEmpty()
}

// isEmpty reports whether no value constraints are set.
func (vc valueConstraints) isEmpty() bool {
	return vc.format == "" && vc.pattern == "" &&
		vc.contentEncoding == "" && vc.contentMediaType == "" &&
		vc.exclusiveMinimum == nil && vc.minimum == nil &&
		vc.exclusiveMaximum == nil && vc.maximum == nil &&
		vc.minLength == nil && vc.maxLength == nil &&
		len(vc.enum) == 0
}

// applyMetadata sets title/description with option overrides.
func applyMetadata(node *fieldNode, title, description string, opts *optionsPb.FieldOptions_JsonSchema) {
	if opts.GetTitle() != "" {
		title = opts.GetTitle()
	}
	if opts.GetDescription() != "" {
		description = opts.GetDescription()
	}
	node.title = title
	node.description = description
}

// applyContainerOptions sets array/object container constraints from options.
func applyContainerOptions(node *fieldNode, opts *optionsPb.FieldOptions_JsonSchema) {
	if v := opts.GetMinItems(); v != 0 {
		node.minItems = &v
	}
	if v := opts.GetMaxItems(); v != 0 {
		node.maxItems = &v
	}
	if opts.GetUniqueItems() {
		node.uniqueItems = true
	}
	if v := opts.GetMinProperties(); v != 0 {
		node.minProperties = &v
	}
	if v := opts.GetMaxProperties(); v != 0 {
		node.maxProperties = &v
	}
}

// applyValueOptions merges field options into base value constraints. Options
// override the base for format/pattern/contentEncoding; numeric and length
// bounds come from options alone.
//
// Per JSON Schema draft 2020-12, ExclusiveMinimum/ExclusiveMaximum are
// standalone numeric values that replace (not supplement) the inclusive
// bounds. The proto option model pairs a bool exclusive flag with a float
// value, so we translate: when exclusive_minimum=true we use the minimum value
// as ExclusiveMinimum (even if that value is 0) and skip Minimum. Otherwise we
// use Minimum only when the value is non-zero — proto3 scalar defaults give us
// no way to distinguish an unset minimum from an explicit minimum of 0 without
// the exclusive flag. Same logic for maximum.
func applyValueOptions(base valueConstraints, opts *optionsPb.FieldOptions_JsonSchema) valueConstraints {
	vc := base
	if opts.GetFormat() != "" {
		vc.format = opts.GetFormat()
	}
	if opts.GetPattern() != "" {
		vc.pattern = opts.GetPattern()
	}
	if opts.GetContentEncoding() != "" {
		vc.contentEncoding = opts.GetContentEncoding()
	}
	if opts.GetContentMediaType() != "" {
		vc.contentMediaType = opts.GetContentMediaType()
	}

	minVal := opts.GetMinimum()
	maxVal := opts.GetMaximum()
	switch {
	case opts.GetExclusiveMinimum():
		vc.exclusiveMinimum = &minVal
	case minVal != 0:
		vc.minimum = &minVal
	}
	switch {
	case opts.GetExclusiveMaximum():
		vc.exclusiveMaximum = &maxVal
	case maxVal != 0:
		vc.maximum = &maxVal
	}

	if v := opts.GetMinLength(); v != 0 {
		vc.minLength = &v
	}
	if v := opts.GetMaxLength(); v != 0 {
		vc.maxLength = &v
	}
	return vc
}

// -----------------------------------------------------------------------------
// Type Mapping Utilities
// -----------------------------------------------------------------------------

// getFieldName returns the proto field name (snake_case) to use in the JSON
// schema. Agents/MCP tools use json.Marshal (not protojson.Marshal), and
// protoc-gen-go tags struct fields with their proto names.
func getFieldName(field *protogen.Field) string {
	return string(field.Desc.Name())
}

// getKindTypeName maps Protocol Buffer field kinds to JSON Schema type names.
//
// This follows the proto3 JSON mapping specification, with special handling:
//   - bytes → "string" (will be base64 encoded)
//   - enums → "integer" (numeric values for encoding/json compatibility)
func getKindTypeName(desc protoreflect.FieldDescriptor) (string, error) {
	switch desc.Kind() {
	case protoreflect.BoolKind:
		return jsBoolean, nil

	case protoreflect.EnumKind:
		return jsInteger, nil

	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Uint32Kind, protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind:
		return jsInteger, nil

	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Uint64Kind, protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind:
		return jsInteger, nil

	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return jsNumber, nil

	case protoreflect.StringKind:
		return jsString, nil

	case protoreflect.BytesKind:
		return jsString, nil

	case protoreflect.MessageKind:
		return jsObject, nil

	case protoreflect.GroupKind:
		// Groups (deprecated proto2 feature) are treated like messages.
		return jsObject, nil

	default:
		return "", fmt.Errorf("unsupported type: %s", desc.Kind())
	}
}

// getTitleAndDescription extracts title and description from proto comments.
//
// If comments contain a blank line (paragraph break), the first paragraph
// becomes the title and the rest becomes the description; otherwise the entire
// comment becomes the description (no title).
func getTitleAndDescription(desc protoreflect.Descriptor) (title string, description string) {
	src := desc.ParentFile().SourceLocations().ByDescriptor(desc)

	if src.LeadingComments != "" {
		comments := strings.TrimSpace(src.LeadingComments)

		parts := strings.SplitN(comments, "\n\n", 2)
		if len(parts) < 2 {
			parts = strings.SplitN(comments, "\r\n\r\n", 2)
		}

		if len(parts) == 2 {
			title = strings.TrimSpace(parts[0])
			description = strings.TrimSpace(parts[1])
		} else {
			description = comments
		}
	}

	return title, description
}

// getEnumValues extracts the allowed enum numeric values from a field.
// Returns numeric values (int32) for encoding/json compatibility.
func getEnumValues(field *protogen.Field) []int32 {
	var enumValues []int32
	for _, value := range field.Enum.Values {
		enumValues = append(enumValues, int32(value.Desc.Number()))
	}
	return enumValues
}

// getEnumValuesFromDescriptor extracts enum numeric values from a descriptor.
// Used for map value enums where only the EnumDescriptor is available.
func getEnumValuesFromDescriptor(enumDesc protoreflect.EnumDescriptor) []int32 {
	var enumValues []int32
	values := enumDesc.Values()
	for i := 0; i < values.Len(); i++ {
		enumValues = append(enumValues, int32(values.Get(i).Number()))
	}
	return enumValues
}
