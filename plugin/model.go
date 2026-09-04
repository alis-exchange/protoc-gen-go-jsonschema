package plugin

import (
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	optionsPb "go.alis.build/common/alis/open/options"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/reflect/protoreflect"
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
// types), and whether it inlines or lives under $defs. It is computed once
// per message and consumed by both the model builder and the printer.
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
	// functions, because methods cannot be added to imported types. Their
	// schemas otherwise follow the same rules as user messages — under
	// encoding/json they marshal identically (the only Google types with a
	// custom json.Marshaler, Struct/Value/ListValue, are free-form and never
	// reach here).
	isGoogle bool

	// cyclic is true for messages that sit on a reference cycle. Their schema
	// lives under $defs and is reached through $ref, because inlining it would
	// never terminate. Every other message inlines: its _WithDefs function
	// returns a fresh, complete object schema and registers nothing.
	cyclic bool
}

// schemaContext carries the per-file inputs every schema decision needs: the
// file prefix that keeps Google-type function names unique across a package,
// and the cycle analysis that decides each message's mode.
type schemaContext struct {
	filePrefix string
	cycles     cycleSet
}

// identityFor computes the identity of a message in the context of the proto
// file currently being generated.
func identityFor(msg *protogen.Message, ctx *schemaContext) messageIdentity {
	id := messageIdentity{
		defKey:     string(msg.Desc.FullName()),
		goName:     msg.GoIdent.GoName,
		protoName:  string(msg.Desc.Name()),
		importPath: msg.GoIdent.GoImportPath,
		isGoogle:   isGoogleType(msg),
		cyclic:     ctx.cycles[msg.Desc.FullName()],
	}
	if id.isGoogle {
		id.funcBase = googleTypeFunctionName(msg, ctx.filePrefix)
	} else {
		id.funcBase = msg.GoIdent.GoName
	}
	return id
}

// withDefsName returns the name of the generated _WithDefs helper function.
func (id messageIdentity) withDefsName() string {
	return id.funcBase + "_JsonSchema_WithDefs"
}

// buildName returns the name of the unexported builder generated for cyclic
// messages. It holds the schema body so the $defs entry and the root can be
// constructed as two independent trees, and stays unexported because only the
// message's own JsonSchema() and _WithDefs call it: it is not part of the
// generated API.
func (id messageIdentity) buildName() string {
	return unexport(id.funcBase) + "_JsonSchema_build"
}

// unexport lower-cases the first rune of a Go identifier.
func unexport(name string) string {
	r, size := utf8.DecodeRuneInString(name)
	return string(unicode.ToLower(r)) + name[size:]
}

// isGoogleType checks if a message is from a Google package (google.*).
// This includes well-known types (google.protobuf.*), common types (google.type.*),
// API types (google.api.*), IAM types (google.iam.*), and any other google.* packages.
func isGoogleType(msg *protogen.Message) bool {
	return strings.HasPrefix(string(msg.Desc.FullName()), "google.")
}

// anyJSONTypes is the full Draft 2020-12 type list, spelling out "any JSON
// value" for google.protobuf.Value. An empty schema would say the same thing,
// but jsonschema-go marshals it as the boolean schema `true`, which strict
// consumers (OpenAI, Gemini) reject.
var anyJSONTypes = []string{jsArray, jsBoolean, jsInteger, jsNull, jsNumber, jsObject, jsString}

// freeFormShape is the JSON shape of a free-form well-known type: a single
// type name, or the explicit list of every type for "any value".
type freeFormShape struct {
	typeName string
	types    []string
}

// freeFormJSONType reports whether msg is one of the well-known types whose
// Go form implements json.Marshaler with plain JSON semantics — Struct, Value
// and ListValue — and, if so, the shape that describes that output: an object
// for Struct, an array for ListValue, any JSON value for Value. These never
// generate schemas of their own and never take part in the reference graph;
// fields of these types inline as free-form nodes.
func freeFormJSONType(msg *protogen.Message) (freeFormShape, bool) {
	return freeFormShapeOf(msg.Desc.FullName())
}

// freeFormShapeOf is freeFormJSONType keyed by full proto name, for callers
// that only hold a descriptor.
func freeFormShapeOf(name protoreflect.FullName) (freeFormShape, bool) {
	switch name {
	case "google.protobuf.Struct":
		return freeFormShape{typeName: jsObject}, true
	case "google.protobuf.ListValue":
		return freeFormShape{typeName: jsArray}, true
	case "google.protobuf.Value":
		return freeFormShape{types: anyJSONTypes}, true
	}
	return freeFormShape{}, false
}

// mapValueMessage returns the value message of a map field, or nil when the
// map's values are not messages. The value lives on field 2 of the synthetic
// map entry.
func mapValueMessage(field *protogen.Field) *protogen.Message {
	if !field.Desc.IsMap() {
		return nil
	}
	for _, f := range field.Message.Fields {
		if f.Desc.Number() == 2 {
			return f.Message
		}
	}
	return nil
}

// isMessageField reports whether a field's value is a message. Proto2 groups
// count: protoc-gen-go emits a nested message type and a pointer field for a
// group exactly as for a message field, and encoding/json marshals both the
// same way.
func isMessageField(desc protoreflect.FieldDescriptor) bool {
	return desc.Kind() == protoreflect.MessageKind || desc.Kind() == protoreflect.GroupKind
}

// fieldMessage returns the message a field's value is — the value message for
// maps — or nil when the value is a scalar.
func fieldMessage(field *protogen.Field) *protogen.Message {
	if field.Desc.IsMap() {
		return mapValueMessage(field)
	}
	if isMessageField(field.Desc) {
		return field.Message
	}
	return nil
}

// messageReferences lists the messages msg's schema refers to, in field order:
// the value message of every non-ignored field (singular, repeated, map value,
// oneof variant, proto2 group) except free-form well-known types, which inline
// as untyped JSON and are not schemas of their own. It is the one definition
// of "reference" shared by the cycle analysis and dependency collection, so a
// message reachable only through an ignored field is neither a cycle member
// nor forced to generate.
func messageReferences(msg *protogen.Message) []*protogen.Message {
	var targets []*protogen.Message
	for _, field := range msg.Fields {
		if getFieldJsonSchemaOptions(field).GetIgnore() {
			continue
		}
		target := fieldMessage(field)
		if target == nil {
			continue
		}
		if _, freeForm := freeFormJSONType(target); freeForm {
			continue
		}
		targets = append(targets, target)
	}
	return targets
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

// fileNamePrefix derives the prefix for Google-type function names from the
// proto file path: the base name without ".proto", rewritten so it can start
// a Go identifier.
func fileNamePrefix(file *protogen.File) string {
	return prefixFromPath(file.Desc.Path())
}

// prefixFromPath is fileNamePrefix on a raw path. Mirrors protoc-gen-go's own
// sanitisation of file names: every rune outside the letter/digit set becomes
// '_' and a leading digit gets one prepended, so "users/v1/admin.proto" ->
// "admin", "sd/v1/my-d.proto" -> "my_d", "auth/2fa.proto" -> "_2fa".
func prefixFromPath(path string) string {
	base := strings.TrimSuffix(path, ".proto")
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	base = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return '_'
	}, base)
	if r, _ := utf8.DecodeRuneInString(base); unicode.IsDigit(r) {
		base = "_" + base
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
// literal; the printer turns them into _WithDefs calls. Whether such a call
// yields an inline object or a $ref is the target's own decision (its cyclic
// flag), never the caller's.

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
	multipleOf       *float64
	minLength        *int64
	maxLength        *int64

	// enum holds the allowed values: int32 elements for auto-derived proto
	// enum values, int64/float64/string elements from the enum_* options
	// (which replace the auto-derived list when set).
	enum []any
}

// elementNode is the schema of an array's items or a map's values: either a
// reference to another message's schema, or an inline value schema.
type elementNode struct {
	// ref, when set, prints as a _WithDefs call; all other fields are ignored.
	ref *messageIdentity

	// typeName is the JSON Schema type of the element. types, when set, is
	// the alternative: an explicit list of allowed types (free-form
	// google.protobuf.Value lists every JSON type). Never both.
	typeName string
	types    []string

	value valueConstraints
}

// fieldNode is the decided schema for one emitted &jsonschema.Schema{...}
// literal: a field property, or a oneof variant's inner value schema.
type fieldNode struct {
	// typeName is the JSON Schema type; empty omits the Type keyword. types,
	// when set, is the alternative: an explicit list of allowed types
	// (free-form google.protobuf.Value lists every JSON type). Never both.
	typeName string
	types    []string

	// title and description are emitted only when non-empty.
	title       string
	description string

	// replaceMetadata marks a decoration of an inline message copy whose
	// field-level comment/options replace the target's own title and
	// description entirely: both keywords are written, an empty one clears.
	// Never set for $ref targets, whose siblings only add.
	replaceMetadata bool

	// Container constraints from field options.
	minItems      *int64
	maxItems      *int64
	uniqueItems   bool
	minProperties *int64
	maxProperties *int64

	// value constrains the root value for scalar fields (unused when element
	// is set — constraints then live on the element).
	value valueConstraints

	// Annotations from field options, applied at the schema root (never on
	// container elements) and included in $ref siblings for message fields.
	deprecated bool
	readOnly   bool
	writeOnly  bool

	// defaultValue is the field's JSON Schema default (nil = unset) and
	// examples its example values. Valid on singular scalar fields only;
	// typed as string/int64/float64/bool, marshaled to JSON by the printer.
	defaultValue any
	examples     []any

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

// oneofVariantNode is one variant branch inside a oneof wrapper.
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
// non-synthetic proto oneof. The wrapper is a oneOf of a null
// branch (encoding/json emits the wrapper key as null when the oneof is unset)
// and an object branch holding the variant oneOf.
type oneofWrapperNode struct {
	// key is the wrapper property key: oneof.GoName (PascalCase).
	key      string
	variants []oneofVariantNode
}

// presenceNode expresses "member field X is set" as one branch of a
// root-level oneof constraint.
type presenceNode struct {
	// fieldName, when set, renders as {Required: ["field_name"]} — the member
	// is an ordinary flat property.
	fieldName string

	// wrapperKey/variantKey render the real-proto-oneof member form: the
	// member's property is its PascalCase wrapper, which is always present
	// (null when unset), so presence is expressed as
	// {Properties: {wrapperKey: {Type: "object", Required: [variantKey]}}}.
	wrapperKey string
	variantKey string
}

// oneofConstraintGroup is a mutually-exclusive group of fields emitted as
// root-level constraints, produced by user-declared json_schema.oneof groups
// (virtual oneofs, at-most-one or exactly-one).
type oneofConstraintGroup struct {
	name string

	// required selects exactly-one semantics (no none-set branch) over
	// at-most-one (a none-set branch joins the alternatives).
	required bool

	members []presenceNode
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

	// constraintGroups holds root-level constraints from user-declared
	// json_schema.oneof groups, in declaration order; one group emits
	// schema.OneOf, several emit schema.AllOf.
	constraintGroups []oneofConstraintGroup

	// oneofWrappers holds the proto-oneof wrapper properties, in oneof
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
func buildMessageSchema(message *protogen.Message, ctx *schemaContext) (*messageSchemaModel, error) {
	id := identityFor(message, ctx)
	title, description := getTitleAndDescription(message.Desc)

	m := &messageSchemaModel{
		id:          id,
		title:       title,
		description: description,
	}

	// --- Declared Oneof Groups (message-level json_schema.oneof option) ---
	declaredGroups, declaredMembers, err := buildDeclaredOneofGroups(message)
	if err != nil {
		return nil, err
	}

	// --- Required Fields ---
	// A field is required only if it's a singular scalar/message field that is
	// not optional. Fields in oneofs, marked optional, repeated (arrays), or
	// maps are not required. Members of a declared oneof group are excluded
	// too: they are conditionally required by the group's constraint. An unset
	// message field still marshals as an omitted key under encoding/json's
	// omitempty tags; that known tension is accepted — the schema states the
	// proto3 contract.
	for _, field := range message.Fields {
		opts := getFieldJsonSchemaOptions(field)
		if opts.GetIgnore() {
			continue
		}
		if declaredMembers[getFieldName(field)] {
			continue
		}
		if field.Oneof == nil && !field.Desc.HasOptionalKeyword() && !field.Desc.IsList() && !field.Desc.IsMap() {
			m.required = append(m.required, getFieldName(field))
		}
	}

	// --- Field Properties ---
	// Members of a real proto oneof are skipped here: they become nested
	// PascalCase wrappers below (encoding/json emits the oneof's Go field
	// name, for Google and user messages alike).
	for _, field := range message.Fields {
		opts := getFieldJsonSchemaOptions(field)
		if opts.GetIgnore() {
			continue
		}
		if oneof := field.Oneof; oneof != nil && !oneof.Desc.IsSynthetic() {
			continue
		}

		prop, err := buildFieldProperty(field, ctx)
		if err != nil {
			return nil, err
		}
		m.fields = append(m.fields, prop)
	}

	m.constraintGroups = append(m.constraintGroups, declaredGroups...)

	// --- Oneof Wrappers ---
	for _, oneof := range message.Oneofs {
		if oneof.Desc.IsSynthetic() {
			continue
		}
		wrapper, ok, err := buildOneofWrapper(oneof, ctx)
		if err != nil {
			return nil, err
		}
		if ok {
			m.oneofWrappers = append(m.oneofWrappers, wrapper)
		}
	}

	return m, nil
}

// buildFieldProperty decides the schema for a single field: a reference for
// message-typed fields (free-form well-known types excepted), or an inline
// schema for everything else.
func buildFieldProperty(field *protogen.Field, ctx *schemaContext) (propertyNode, error) {
	if err := validateFieldOptions(field); err != nil {
		return propertyNode{}, err
	}
	title, description := getTitleAndDescription(field.Desc)

	switch {
	case field.Desc.IsList():
		node, err := buildArrayNode(field, title, description, ctx)
		if err != nil {
			return propertyNode{}, err
		}
		return propertyNode{key: getFieldName(field), node: node}, nil
	case field.Desc.IsMap():
		node, err := buildMapNode(field, title, description, ctx)
		if err != nil {
			return propertyNode{}, err
		}
		return propertyNode{key: getFieldName(field), node: node}, nil
	case isMessageField(field.Desc):
		if shape, ok := freeFormJSONType(field.Message); ok {
			return propertyNode{key: getFieldName(field), node: buildFreeFormNode(field, title, description, shape)}, nil
		}
		// Message-type fields reference the target's schema. Field comments
		// and field-level options decorate the returned schema: overrides on
		// an inline copy, siblings on a $ref.
		id := identityFor(field.Message, ctx)
		return propertyNode{key: getFieldName(field), ref: &id, node: buildRefSiblings(field, title, description, !id.cyclic)}, nil
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
	applyRootOptions(node, opts)

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
func buildArrayNode(field *protogen.Field, title, description string, ctx *schemaContext) (*fieldNode, error) {
	opts := getFieldJsonSchemaOptions(field)

	node := &fieldNode{typeName: jsArray}
	applyMetadata(node, title, description, opts)
	applyContainerOptions(node, opts)
	applyRootOptions(node, opts)
	element, err := buildElement(field.Desc, field, field.Message, opts, ctx)
	if err != nil {
		return nil, fmt.Errorf("field %s: %w", field.Desc.FullName(), err)
	}
	node.element = element
	return node, nil
}

// buildMapNode decides the inline schema for a map field: an "object" root
// with the value schema under AdditionalProperties and, for non-string keys, a
// PropertyNames pattern (JSON serializes all map keys as strings).
func buildMapNode(field *protogen.Field, title, description string, ctx *schemaContext) (*fieldNode, error) {
	opts := getFieldJsonSchemaOptions(field)

	node := &fieldNode{typeName: jsObject, elementIsAdditionalProperties: true}
	applyMetadata(node, title, description, opts)
	applyContainerOptions(node, opts)
	applyRootOptions(node, opts)

	switch field.Desc.MapKey().Kind() {
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Uint32Kind,
		protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Uint64Kind,
		protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind:
		node.propertyNamesPattern = "^-?[0-9]+$"
	case protoreflect.BoolKind:
		node.propertyNamesPattern = "^(true|false)$"
	}

	element, err := buildElement(field.Desc.MapValue(), nil, mapValueMessage(field), opts, ctx)
	if err != nil {
		return nil, fmt.Errorf("field %s: %w", field.Desc.FullName(), err)
	}
	node.element = element
	return node, nil
}

// buildElement decides the JSON shape of one proto value — the single shared
// answer for array elements and map values. enumField carries the
// protogen.Field when available (array elements); map values fall back to the
// descriptor's enum. msg is the element's message for message and group
// kinds (protogen resolves it for every such field, so it is never nil).
func buildElement(desc protoreflect.FieldDescriptor, enumField *protogen.Field, msg *protogen.Message, opts *optionsPb.FieldOptions_JsonSchema, ctx *schemaContext) (*elementNode, error) {
	kindTypeName, err := getKindTypeName(desc)
	if err != nil {
		return nil, err
	}

	switch desc.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		if shape, ok := freeFormJSONType(msg); ok {
			return &elementNode{typeName: shape.typeName, types: shape.types, value: applyValueOptions(valueConstraints{}, opts)}, nil
		}
		id := identityFor(msg, ctx)
		return &elementNode{ref: &id}, nil

	case protoreflect.EnumKind:
		var enum []any
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

// buildOneofWrapper decides the nested wrapper schema for one proto oneof. Returns ok=false when every variant is ignored.
func buildOneofWrapper(oneof *protogen.Oneof, ctx *schemaContext) (oneofWrapperNode, bool, error) {
	wrapper := oneofWrapperNode{key: oneof.GoName}
	for _, field := range oneof.Fields {
		opts := getFieldJsonSchemaOptions(field)
		if opts.GetIgnore() {
			continue
		}
		if err := validateFieldOptions(field); err != nil {
			return oneofWrapperNode{}, false, err
		}
		variant := oneofVariantNode{key: field.GoName}
		title, description := getTitleAndDescription(field.Desc)
		if isMessageField(field.Desc) {
			// Message variant: same rules as buildFieldProperty — free-form
			// well-known types inline, everything else is a decorated
			// reference.
			if shape, ok := freeFormJSONType(field.Message); ok {
				variant.node = buildFreeFormNode(field, title, description, shape)
			} else {
				id := identityFor(field.Message, ctx)
				variant.ref = &id
				variant.node = buildRefSiblings(field, title, description, !id.cyclic)
			}
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

// buildFreeFormNode decides the inline schema for a field whose message type
// marshals as plain JSON (Struct, Value, ListValue): the free-form type plus
// the field's own metadata and options. No structure is described.
func buildFreeFormNode(field *protogen.Field, title, description string, shape freeFormShape) *fieldNode {
	opts := getFieldJsonSchemaOptions(field)
	node := &fieldNode{typeName: shape.typeName, types: shape.types}
	applyMetadata(node, title, description, opts)
	applyContainerOptions(node, opts)
	applyRootOptions(node, opts)
	node.value = applyValueOptions(valueConstraints{}, opts)
	return node
}

// buildRefSiblings decides the keywords that decorate the schema returned by a
// message-typed field's _WithDefs call: comment/option metadata plus any
// option constraints. On an inline copy (inlineTarget) a field comment or
// metadata option replaces the target's own title and description as a pair,
// so a field-only description never sits next to the message's own title; on
// a $ref they ride as siblings. Returns nil when the field contributes
// nothing beyond the reference itself.
func buildRefSiblings(field *protogen.Field, title, description string, inlineTarget bool) *fieldNode {
	opts := getFieldJsonSchemaOptions(field)
	node := &fieldNode{}
	applyMetadata(node, title, description, opts)
	node.replaceMetadata = inlineTarget && (node.title != "" || node.description != "")
	applyContainerOptions(node, opts)
	applyRootOptions(node, opts)
	node.value = applyValueOptions(valueConstraints{}, opts)
	if node.isEmpty() {
		return nil
	}
	return node
}

// isEmpty reports whether a node carries no keywords at all.
func (n *fieldNode) isEmpty() bool {
	return n.typeName == "" && len(n.types) == 0 && n.title == "" && n.description == "" &&
		n.minItems == nil && n.maxItems == nil && !n.uniqueItems &&
		n.minProperties == nil && n.maxProperties == nil &&
		!n.deprecated && !n.readOnly && !n.writeOnly &&
		n.defaultValue == nil && len(n.examples) == 0 &&
		n.element == nil && n.propertyNamesPattern == "" &&
		n.value.isEmpty()
}

// isEmpty reports whether no value constraints are set.
func (vc valueConstraints) isEmpty() bool {
	return vc.format == "" && vc.pattern == "" &&
		vc.contentEncoding == "" && vc.contentMediaType == "" &&
		vc.exclusiveMinimum == nil && vc.minimum == nil &&
		vc.exclusiveMaximum == nil && vc.maximum == nil &&
		vc.multipleOf == nil &&
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
// Reads are presence-based (the option fields are proto3 optional), so an
// explicit 0 is expressible.
func applyContainerOptions(node *fieldNode, opts *optionsPb.FieldOptions_JsonSchema) {
	if opts == nil {
		return
	}
	if opts.MinItems != nil {
		node.minItems = opts.MinItems
	}
	if opts.MaxItems != nil {
		node.maxItems = opts.MaxItems
	}
	if opts.GetUniqueItems() {
		node.uniqueItems = true
	}
	if opts.MinProperties != nil {
		node.minProperties = opts.MinProperties
	}
	if opts.MaxProperties != nil {
		node.maxProperties = opts.MaxProperties
	}
}

// applyRootOptions applies root-level annotations and default/examples from
// field options. These always live on the field's schema root (never on
// container elements); validateFieldOptions has already established that
// default/examples only appear on singular scalar fields.
func applyRootOptions(node *fieldNode, opts *optionsPb.FieldOptions_JsonSchema) {
	if opts == nil {
		return
	}
	node.deprecated = opts.GetDeprecated()
	node.readOnly = opts.GetReadOnly()
	node.writeOnly = opts.GetWriteOnly()

	switch {
	case opts.DefaultString != nil:
		node.defaultValue = *opts.DefaultString
	case opts.DefaultInt != nil:
		node.defaultValue = *opts.DefaultInt
	case opts.DefaultNumber != nil:
		node.defaultValue = *opts.DefaultNumber
	case opts.DefaultBool != nil:
		node.defaultValue = *opts.DefaultBool
	}

	switch {
	case len(opts.GetExamplesString()) > 0:
		node.examples = toAnySlice(opts.GetExamplesString())
	case len(opts.GetExamplesInt()) > 0:
		node.examples = toAnySlice(opts.GetExamplesInt())
	case len(opts.GetExamplesNumber()) > 0:
		node.examples = toAnySlice(opts.GetExamplesNumber())
	}
}

// applyValueOptions merges field options into base value constraints. Options
// override the base for format/pattern/contentEncoding; numeric and length
// bounds come from options alone. Reads are presence-based, so explicit zero
// bounds (e.g. minimum: 0) are expressible.
//
// Per JSON Schema draft 2020-12, ExclusiveMinimum/ExclusiveMaximum are
// standalone numeric values that replace (not supplement) the inclusive
// bounds: when exclusive_minimum=true the minimum value is emitted as
// ExclusiveMinimum and Minimum is skipped (validateFieldOptions guarantees the
// bound is set). Same for maximum.
func applyValueOptions(base valueConstraints, opts *optionsPb.FieldOptions_JsonSchema) valueConstraints {
	vc := base
	if opts == nil {
		return vc
	}
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

	switch {
	case opts.GetExclusiveMinimum():
		vc.exclusiveMinimum = opts.Minimum
	case opts.Minimum != nil:
		vc.minimum = opts.Minimum
	}
	switch {
	case opts.GetExclusiveMaximum():
		vc.exclusiveMaximum = opts.Maximum
	case opts.Maximum != nil:
		vc.maximum = opts.Maximum
	}

	if opts.MultipleOf != nil {
		vc.multipleOf = opts.MultipleOf
	}
	if opts.MinLength != nil {
		vc.minLength = opts.MinLength
	}
	if opts.MaxLength != nil {
		vc.maxLength = opts.MaxLength
	}

	// enum_* replaces any auto-derived enum value list.
	switch {
	case len(opts.GetEnumString()) > 0:
		vc.enum = toAnySlice(opts.GetEnumString())
	case len(opts.GetEnumInt()) > 0:
		vc.enum = toAnySlice(opts.GetEnumInt())
	case len(opts.GetEnumNumber()) > 0:
		vc.enum = toAnySlice(opts.GetEnumNumber())
	}
	return vc
}

func toAnySlice[T any](values []T) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

// jsonTypeFamily classifies a field descriptor's JSON value type for option
// validation. The empty family means "any JSON value" (a free-form
// google.protobuf.Value): typed options are then accepted as written.
func jsonTypeFamily(desc protoreflect.FieldDescriptor) string {
	if isMessageField(desc) {
		if shape, ok := freeFormShapeOf(desc.Message().FullName()); ok {
			return shape.typeName
		}
	}
	switch desc.Kind() {
	case protoreflect.StringKind, protoreflect.BytesKind:
		return jsString
	case protoreflect.EnumKind,
		protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Uint32Kind,
		protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Uint64Kind,
		protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind:
		return jsInteger
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return jsNumber
	case protoreflect.BoolKind:
		return jsBoolean
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return jsObject
	default:
		return ""
	}
}

// validateFieldOptions enforces the kind-dependent rules of the typed option
// variants for one field, before any of them are applied. Every violation is
// a generation-time error naming the field — never a silent drop.
func validateFieldOptions(field *protogen.Field) error {
	opts := getFieldJsonSchemaOptions(field)
	if opts == nil {
		return nil
	}
	name := field.Desc.FullName()

	// Value-level options are governed by the JSON type of the field's value:
	// the element for repeated fields (Kind() already is the element kind),
	// the value for maps.
	valueDesc := field.Desc
	if field.Desc.IsMap() {
		valueDesc = field.Desc.MapValue()
	}
	family := jsonTypeFamily(valueDesc)
	anyValue := family == ""

	// --- numeric options must be finite ---
	// NaN and ±Inf have no JSON form, and the printer would render them as
	// the Go identifiers NaN/+Inf, which do not compile. protoc accepts the
	// literals `inf`, `-inf` and `nan` for double options, so reject them
	// here before any other rule reads the value (NaN compares false with
	// everything, so a range check alone would let it through).
	if err := requireFinite(name, "minimum", opts.Minimum); err != nil {
		return err
	}
	if err := requireFinite(name, "maximum", opts.Maximum); err != nil {
		return err
	}
	if err := requireFinite(name, "multiple_of", opts.MultipleOf); err != nil {
		return err
	}
	if err := requireFinite(name, "default_number", opts.DefaultNumber); err != nil {
		return err
	}
	for _, v := range opts.GetEnumNumber() {
		if err := requireFinite(name, "enum_number", &v); err != nil {
			return err
		}
	}
	for _, v := range opts.GetExamplesNumber() {
		if err := requireFinite(name, "examples_number", &v); err != nil {
			return err
		}
	}

	// --- enum_*: one variant, matching the value's JSON type ---
	enumFamilies := countSetVariants(
		setVariant{jsString, len(opts.GetEnumString()) > 0},
		setVariant{jsInteger, len(opts.GetEnumInt()) > 0},
		setVariant{jsNumber, len(opts.GetEnumNumber()) > 0},
	)
	if len(enumFamilies) > 1 {
		return fmt.Errorf("field %s: at most one enum_* variant may be set", name)
	}
	if len(enumFamilies) == 1 && !anyValue && enumFamilies[0] != family {
		return fmt.Errorf("field %s: the enum_* variant for JSON type %q does not match the field's JSON type %q", name, enumFamilies[0], family)
	}

	// enum_int on a proto enum field must be a subset of its declared values.
	if len(opts.GetEnumInt()) > 0 && valueDesc.Kind() == protoreflect.EnumKind {
		declared := make(map[int64]bool)
		values := valueDesc.Enum().Values()
		for i := 0; i < values.Len(); i++ {
			declared[int64(values.Get(i).Number())] = true
		}
		for _, v := range opts.GetEnumInt() {
			if !declared[v] {
				return fmt.Errorf("field %s: enum_int value %d is not a declared value of enum %s", name, v, valueDesc.Enum().FullName())
			}
		}
	}

	// --- multiple_of: numeric values only, > 0 ---
	if opts.MultipleOf != nil {
		if !anyValue && family != jsInteger && family != jsNumber {
			return fmt.Errorf("field %s: multiple_of applies to numeric fields only", name)
		}
		if *opts.MultipleOf <= 0 {
			return fmt.Errorf("field %s: multiple_of must be greater than 0", name)
		}
	}

	// --- exclusive bounds require their bound ---
	if opts.GetExclusiveMinimum() && opts.Minimum == nil {
		return fmt.Errorf("field %s: exclusive_minimum requires minimum to be set", name)
	}
	if opts.GetExclusiveMaximum() && opts.Maximum == nil {
		return fmt.Errorf("field %s: exclusive_maximum requires maximum to be set", name)
	}

	// --- default_* / examples_*: singular scalar fields, matching variant ---
	singularScalar := !field.Desc.IsList() && !field.Desc.IsMap() && family != jsObject

	defaultFamilies := countSetVariants(
		setVariant{jsString, opts.DefaultString != nil},
		setVariant{jsInteger, opts.DefaultInt != nil},
		setVariant{jsNumber, opts.DefaultNumber != nil},
		setVariant{jsBoolean, opts.DefaultBool != nil},
	)
	if len(defaultFamilies) > 1 {
		return fmt.Errorf("field %s: at most one default_* variant may be set", name)
	}
	if len(defaultFamilies) == 1 {
		if !singularScalar {
			return fmt.Errorf("field %s: default_* is only valid on singular scalar fields", name)
		}
		if !anyValue && defaultFamilies[0] != family {
			return fmt.Errorf("field %s: the default_* variant for JSON type %q does not match the field's JSON type %q", name, defaultFamilies[0], family)
		}
	}

	examplesFamilies := countSetVariants(
		setVariant{jsString, len(opts.GetExamplesString()) > 0},
		setVariant{jsInteger, len(opts.GetExamplesInt()) > 0},
		setVariant{jsNumber, len(opts.GetExamplesNumber()) > 0},
	)
	if len(examplesFamilies) > 1 {
		return fmt.Errorf("field %s: at most one examples_* variant may be set", name)
	}
	if len(examplesFamilies) == 1 {
		if !singularScalar {
			return fmt.Errorf("field %s: examples_* is only valid on singular scalar fields", name)
		}
		if !anyValue && examplesFamilies[0] != family {
			return fmt.Errorf("field %s: the examples_* variant for JSON type %q does not match the field's JSON type %q", name, examplesFamilies[0], family)
		}
	}

	return nil
}

// requireFinite rejects a set numeric option whose value is NaN or infinite.
func requireFinite(field protoreflect.FullName, option string, v *float64) error {
	if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0)) {
		return fmt.Errorf("field %s: %s must be a finite number, got %v", field, option, *v)
	}
	return nil
}

type setVariant struct {
	family string
	set    bool
}

func countSetVariants(variants ...setVariant) []string {
	var families []string
	for _, v := range variants {
		if v.set {
			families = append(families, v.family)
		}
	}
	return families
}

// buildDeclaredOneofGroups reads the message-level json_schema.oneof option
// and turns each declared group into a root-level constraint. The second
// return value is the set of member field names, used to exclude members from
// the plain required list (they are conditionally required by their group).
func buildDeclaredOneofGroups(message *protogen.Message) ([]oneofConstraintGroup, map[string]bool, error) {
	declared := getMessageJsonSchemaOptions(message).GetOneof()
	if len(declared) == 0 {
		return nil, nil, nil
	}
	msgName := message.Desc.FullName()

	fieldsByName := make(map[string]*protogen.Field, len(message.Fields))
	for _, field := range message.Fields {
		fieldsByName[getFieldName(field)] = field
	}

	members := make(map[string]bool)
	var groups []oneofConstraintGroup
	for i, decl := range declared {
		names := decl.GetFields()
		if len(names) < 2 {
			return nil, nil, fmt.Errorf("message %s: json_schema.oneof group %d must name at least two fields", msgName, i)
		}
		group := oneofConstraintGroup{
			name:     fmt.Sprintf("oneof_%d", i),
			required: decl.GetRequired(),
		}
		for _, fieldName := range names {
			field, ok := fieldsByName[fieldName]
			if !ok {
				return nil, nil, fmt.Errorf("message %s: json_schema.oneof group %d references unknown field %q", msgName, i, fieldName)
			}
			if field.Desc.IsList() || field.Desc.IsMap() {
				return nil, nil, fmt.Errorf("message %s: json_schema.oneof member %q must not be a repeated or map field", msgName, fieldName)
			}
			if getFieldJsonSchemaOptions(field).GetIgnore() {
				return nil, nil, fmt.Errorf("message %s: json_schema.oneof member %q is excluded from the schema by ignore", msgName, fieldName)
			}
			if members[fieldName] {
				return nil, nil, fmt.Errorf("message %s: field %q may only appear once across json_schema.oneof groups", msgName, fieldName)
			}
			members[fieldName] = true

			if oneof := field.Oneof; oneof != nil && !oneof.Desc.IsSynthetic() {
				// Real proto-oneof member: its property is the PascalCase
				// wrapper, which is always present (null when unset).
				group.members = append(group.members, presenceNode{wrapperKey: oneof.GoName, variantKey: field.GoName})
			} else {
				group.members = append(group.members, presenceNode{fieldName: fieldName})
			}
		}
		groups = append(groups, group)
	}
	return groups, members, nil
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
// Returns numeric values (int32 elements) for encoding/json compatibility.
func getEnumValues(field *protogen.Field) []any {
	var enumValues []any
	for _, value := range field.Enum.Values {
		enumValues = append(enumValues, int32(value.Desc.Number()))
	}
	return enumValues
}

// getEnumValuesFromDescriptor extracts enum numeric values from a descriptor.
// Used for map value enums where only the EnumDescriptor is available.
func getEnumValuesFromDescriptor(enumDesc protoreflect.EnumDescriptor) []any {
	var enumValues []any
	values := enumDesc.Values()
	for i := 0; i < values.Len(); i++ {
		enumValues = append(enumValues, int32(values.Get(i).Number()))
	}
	return enumValues
}
