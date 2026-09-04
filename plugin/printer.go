package plugin

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"google.golang.org/protobuf/compiler/protogen"
)

// schemaPrinter transcribes a messageSchemaModel into generated Go source. It
// owns every Go-syntax concern — literal layout, string escaping, import
// qualification — and makes no schema decisions of its own.
type schemaPrinter struct {
	g *protogen.GeneratedFile
}

// refCall renders a reference as the Go call expression that retrieves the
// target message's schema: "Target_JsonSchema_WithDefs(defs)", qualified with
// an import alias for cross-package user types. Google type functions live in
// the generated file itself, so their names are used verbatim.
func (p *schemaPrinter) refCall(id *messageIdentity) string {
	if id.isGoogle {
		return id.withDefsName() + "(defs)"
	}
	ident := protogen.GoIdent{GoName: id.withDefsName(), GoImportPath: id.importPath}
	return p.g.QualifiedGoIdent(ident) + "(defs)"
}

// printMessageSchema emits the generated functions for one message: the
// public JsonSchema() entry point (method for user types, standalone function
// for Google types) and the _WithDefs composition helper. Messages in $defs
// mode also get an unexported _build function holding the schema body, so the
// definition and the root can be built as two independent trees. The body
// itself is identical in both modes; only the wrappers differ.
func (p *schemaPrinter) printMessageSchema(m *messageSchemaModel) {
	id := m.id

	// --- Public Entry Point ---
	// The root is always a real object schema (MCP requires a literal
	// type: "object" root) and never shares a node with $defs — jsonschema-go
	// resolution requires a tree. Inline mode: _WithDefs returns the object
	// itself and $defs is attached only when a cyclic descendant registered
	// one. $defs mode: _WithDefs registers the definition, then a second build
	// produces an independent root whose references resolve into it.
	if id.isGoogle {
		p.g.P(fmt.Sprintf("// %s_JsonSchema returns the JSON schema for the %s message.", id.funcBase, id.protoName))
		p.g.P(fmt.Sprintf("func %s_JsonSchema() *jsonschema.Schema {", id.funcBase))
	} else {
		p.g.P(fmt.Sprintf("// JsonSchema returns the JSON schema for the %s message.", id.protoName))
		p.g.P(fmt.Sprintf("func (x *%s) JsonSchema() *jsonschema.Schema {", id.goName))
	}
	p.g.P("defs := make(map[string]*jsonschema.Schema)")
	if id.cyclic {
		p.g.P(fmt.Sprintf("_ = %s(defs)", id.withDefsName()))
		p.g.P(fmt.Sprintf("root := %s(defs)", id.buildName()))
		p.g.P("root.Defs = defs")
		p.g.P("return root")
	} else {
		p.g.P(fmt.Sprintf("root := %s(defs)", id.withDefsName()))
		p.g.P("if len(defs) > 0 {")
		p.g.P("root.Defs = defs")
		p.g.P("}")
		p.g.P("return root")
	}
	p.g.P("}")
	p.g.P()

	// --- Composition Helper ---
	p.g.P(fmt.Sprintf("func %s(defs map[string]*jsonschema.Schema) *jsonschema.Schema {", id.withDefsName()))
	if id.cyclic {
		// Return early if already defined (handles circular references).
		p.g.P(fmt.Sprintf("if _, ok := defs[\"%s\"]; ok {", id.defKey))
		p.g.P(fmt.Sprintf("return &jsonschema.Schema{Ref: \"#/$defs/%s\"}", id.defKey))
		p.g.P("}")
		p.g.P("// Reserve the key before building: references back to this message")
		p.g.P("// (its own fields, or another message on the cycle) then resolve to the")
		p.g.P("// $ref above instead of recursing forever.")
		p.g.P(fmt.Sprintf("defs[\"%s\"] = nil", id.defKey))
		p.g.P(fmt.Sprintf("defs[\"%s\"] = %s(defs)", id.defKey, id.buildName()))
		p.g.P(fmt.Sprintf("return &jsonschema.Schema{Ref: \"#/$defs/%s\"}", id.defKey))
		p.g.P("}")
		p.g.P()

		// --- Builder ---
		p.g.P(fmt.Sprintf("// %s builds the %s object schema: the body shared by the", id.buildName(), id.protoName))
		p.g.P("// $defs entry and the independent root that JsonSchema() returns.")
		p.g.P(fmt.Sprintf("func %s(defs map[string]*jsonschema.Schema) *jsonschema.Schema {", id.buildName()))
	}

	p.printSchemaBody(m)

	p.g.P("    return schema")
	p.g.P("}")
}

// printSchemaBody emits the statements that build a message's object schema
// into a local `schema` variable: the header literal, the field properties,
// the root-level oneof constraints, and the oneof wrappers. The body does not
// depend on the message's mode.
func (p *schemaPrinter) printSchemaBody(m *messageSchemaModel) {
	// --- Schema Object ---
	p.g.P("schema := &jsonschema.Schema{")
	p.g.P(`Type: "object",`)
	if m.title != "" {
		p.g.P(fmt.Sprintf(`Title: "%s",`, escapeGoString(m.title)))
	}
	if m.description != "" {
		p.g.P(fmt.Sprintf(`Description: "%s",`, escapeGoString(m.description)))
	}
	p.g.P(`Properties: make(map[string]*jsonschema.Schema),`)
	if len(m.required) > 0 {
		p.g.P(`Required: []string{`)
		for _, f := range m.required {
			p.g.P(fmt.Sprintf(`"%s",`, f))
		}
		p.g.P(`},`)
	}
	p.g.P("}")
	p.g.P()

	// --- Field Properties ---
	for _, prop := range m.fields {
		if prop.ref != nil {
			p.g.P(fmt.Sprintf(`schema.Properties["%s"] = %s`, prop.key, p.refCall(prop.ref)))
			if prop.node != nil {
				p.printOverrides(fmt.Sprintf(`schema.Properties["%s"]`, prop.key), prop.node)
			}
		} else {
			p.printFieldNode(prop.node, fmt.Sprintf(`schema.Properties["%s"] = `, prop.key), "")
		}
		p.g.P("")
	}

	// --- Root-Level Oneof Constraints ---
	// User-declared json_schema.oneof groups (at-most-one or exactly-one).
	if len(m.constraintGroups) == 1 {
		p.g.P(`schema.OneOf = []*jsonschema.Schema{`)
		p.printConstraintGroupBranches(m.constraintGroups[0])
		p.g.P(`}`)
	} else if len(m.constraintGroups) > 1 {
		p.g.P(`schema.AllOf = []*jsonschema.Schema{`)
		for _, group := range m.constraintGroups {
			p.g.P(`{`)
			p.g.P(`OneOf: []*jsonschema.Schema{`)
			p.printConstraintGroupBranches(group)
			p.g.P(`},`)
			p.g.P(`},`)
		}
		p.g.P(`}`)
	}

	// --- Nested PascalCase Oneof Wrappers ---
	for _, wrapper := range m.oneofWrappers {
		p.printOneofWrapper(wrapper)
		p.g.P("")
	}
}

// printPresenceBranch emits one "member is set" branch of a root-level oneof
// constraint.
func (p *schemaPrinter) printPresenceBranch(member presenceNode) {
	if member.fieldName != "" {
		p.g.P(fmt.Sprintf(`{Required: []string{"%s"}},`, member.fieldName))
		return
	}
	// Real proto-oneof member: presence means the PascalCase wrapper holds an
	// object whose required key is this variant. The wrapper key itself is
	// required in the branch — Properties alone would pass vacuously for
	// documents that omit the wrapper entirely (encoding/json always emits
	// it, but LLM-authored documents may not).
	p.g.P(`{`)
	p.g.P(fmt.Sprintf(`Required: []string{"%s"},`, member.wrapperKey))
	p.g.P(`Properties: map[string]*jsonschema.Schema{`)
	p.g.P(fmt.Sprintf(`"%s": {`, member.wrapperKey))
	p.g.P(`Type: "object",`)
	p.g.P(fmt.Sprintf(`Required: []string{"%s"},`, member.variantKey))
	p.g.P(`},`)
	p.g.P(`},`)
	p.g.P(`},`)
}

// printConstraintGroupBranches emits the member alternatives of a root-level
// oneof constraint. For at-most-one groups a "none present" branch joins the
// alternatives (proto3 semantics: a oneof does not require any alternative to
// be set); exactly-one groups (required: true) omit it.
func (p *schemaPrinter) printConstraintGroupBranches(group oneofConstraintGroup) {
	for _, member := range group.members {
		p.printPresenceBranch(member)
	}
	if group.required {
		return
	}
	p.g.P(`{Not: &jsonschema.Schema{AnyOf: []*jsonschema.Schema{`)
	for _, member := range group.members {
		p.printPresenceBranch(member)
	}
	p.g.P(`}}},`)
}

// printOneofWrapper emits the nested PascalCase wrapper property for one proto
// oneof: a oneOf of a null branch (unset oneof) and an object branch
// whose own oneOf holds one branch per variant.
func (p *schemaPrinter) printOneofWrapper(wrapper oneofWrapperNode) {
	p.g.P(fmt.Sprintf(`schema.Properties["%s"] = &jsonschema.Schema{`, wrapper.key))
	p.g.P(`OneOf: []*jsonschema.Schema{`)
	p.g.P(fmt.Sprintf(`{Type: "%s"},`, jsNull))
	p.g.P(`{`)
	p.g.P(fmt.Sprintf(`Type: "%s",`, jsObject))
	p.g.P(`OneOf: []*jsonschema.Schema{`)

	for _, variant := range wrapper.variants {
		p.g.P(`{`)
		p.g.P(fmt.Sprintf(`Type: "%s",`, jsObject))
		p.g.P(`Properties: map[string]*jsonschema.Schema{`)
		switch {
		case variant.ref != nil && variant.node != nil:
			// Reference with decoration inside a composite literal: a self-contained
			// closure decorates the fresh schema the _WithDefs call
			// returns (never a stored definition).
			p.g.P(fmt.Sprintf(`"%s": func() *jsonschema.Schema {`, variant.key))
			p.g.P(fmt.Sprintf(`s := %s`, p.refCall(variant.ref)))
			p.printOverrides("s", variant.node)
			p.g.P(`return s`)
			p.g.P(`}(),`)
		case variant.ref != nil:
			p.g.P(fmt.Sprintf(`"%s": %s,`, variant.key, p.refCall(variant.ref)))
		default:
			p.printFieldNode(variant.node, fmt.Sprintf(`"%s": `, variant.key), ",")
		}
		p.g.P(`},`)
		p.g.P(fmt.Sprintf(`Required: []string{"%s"},`, variant.key))
		p.g.P(`},`)
	}

	p.g.P(`},`) // close inner OneOf (variant branches)
	p.g.P(`},`) // close object branch
	p.g.P(`},`) // close outer OneOf (null | object)
	p.g.P(`}`)
}

// printFieldNode emits one `&jsonschema.Schema{...}` literal for a field
// schema. prefix is glued before the literal (a statement head or a composite
// literal key); closing is appended after the final brace ("," inside
// composite literals, "" for statements).
func (p *schemaPrinter) printFieldNode(node *fieldNode, prefix, closing string) {
	p.g.P(prefix + `&jsonschema.Schema{`)

	if node.typeName != "" {
		p.g.P(fmt.Sprintf(`Type: "%s",`, node.typeName))
	}
	if len(node.types) > 0 {
		p.g.P(`Types: []string{` + quotedList(node.types) + `},`)
	}

	// Metadata keywords are emitted only when non-empty, matching the
	// message-level rule (jsonschema-go marshals with omitempty, so empty
	// strings never reached the JSON anyway).
	if node.title != "" {
		p.g.P(fmt.Sprintf(`Title: "%s",`, escapeGoString(node.title)))
	}
	if node.description != "" {
		p.g.P(fmt.Sprintf(`Description: "%s",`, escapeGoString(node.description)))
	}

	if node.minItems != nil {
		p.g.P(fmt.Sprintf(`MinItems: &[]int{%d}[0],`, *node.minItems))
	}
	if node.maxItems != nil {
		p.g.P(fmt.Sprintf(`MaxItems: &[]int{%d}[0],`, *node.maxItems))
	}
	if node.uniqueItems {
		p.g.P(`UniqueItems: true,`)
	}
	if node.minProperties != nil {
		p.g.P(fmt.Sprintf(`MinProperties: &[]int{%d}[0],`, *node.minProperties))
	}
	if node.maxProperties != nil {
		p.g.P(fmt.Sprintf(`MaxProperties: &[]int{%d}[0],`, *node.maxProperties))
	}

	// Root-level annotations (on containers they mark the array/object itself).
	if node.deprecated {
		p.g.P(`Deprecated: true,`)
	}
	if node.readOnly {
		p.g.P(`ReadOnly: true,`)
	}
	if node.writeOnly {
		p.g.P(`WriteOnly: true,`)
	}

	if node.element != nil {
		targetField := "Items"
		if node.elementIsAdditionalProperties {
			targetField = "AdditionalProperties"
		}
		if node.element.ref != nil {
			p.g.P(fmt.Sprintf(`%s: %s,`, targetField, p.refCall(node.element.ref)))
		} else {
			p.g.P(fmt.Sprintf(`%s: &jsonschema.Schema{`, targetField))
			if node.element.typeName != "" {
				p.g.P(fmt.Sprintf(`Type: "%s",`, node.element.typeName))
			}
			if len(node.element.types) > 0 {
				p.g.P(`Types: []string{` + quotedList(node.element.types) + `},`)
			}
			p.printValueConstraints(node.element.value)
			p.g.P(`},`)
		}
	} else {
		p.printValueConstraints(node.value)
	}

	if node.defaultValue != nil {
		p.g.P(fmt.Sprintf(`Default: %s(%s),`, p.jsonRawMessageIdent(), rawJSONLiteral(node.defaultValue)))
	}
	if len(node.examples) > 0 {
		p.g.P(`Examples: []any{`)
		for _, v := range node.examples {
			p.g.P(anyLiteral(v) + `,`)
		}
		p.g.P(`},`)
	}

	if node.propertyNamesPattern != "" {
		p.g.P(`PropertyNames: &jsonschema.Schema{`)
		p.g.P(fmt.Sprintf(`Pattern: "%s",`, escapeGoString(node.propertyNamesPattern)))
		p.g.P(`},`)
	}

	p.g.P("}" + closing)
}

// jsonRawMessageIdent qualifies encoding/json's RawMessage type, registering
// the import on the generated file.
func (p *schemaPrinter) jsonRawMessageIdent() string {
	return p.g.QualifiedGoIdent(protogen.GoIdent{GoName: "RawMessage", GoImportPath: "encoding/json"})
}

// rawJSONLiteral renders a typed default value as a quoted JSON text literal
// for embedding in json.RawMessage(...). Marshaling string/int64/float64/bool
// cannot fail.
func rawJSONLiteral(v any) string {
	data, _ := json.Marshal(v)
	return strconv.Quote(string(data))
}

// anyLiteral renders one enum/examples element as a Go literal inside []any.
func anyLiteral(v any) string {
	switch t := v.(type) {
	case string:
		return strconv.Quote(t)
	case int32:
		return strconv.FormatInt(int64(t), 10)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// printOverrides emits assignment statements that decorate the schema a
// _WithDefs call returned. On an inline copy they override the target
// message's own metadata (the field is more specific); on a $ref they are
// sibling keywords (Draft 2020-12 keeps siblings of $ref applicable).
// recv is the Go expression holding that schema — a Properties map index or
// a local variable. _WithDefs always returns a fresh value, never a stored
// definition, so mutation is safe.
func (p *schemaPrinter) printOverrides(recv string, node *fieldNode) {
	if node.replaceMetadata {
		// Inline copy: the field's metadata replaces the target's as a pair.
		p.g.P(fmt.Sprintf(`%s.Title = "%s"`, recv, escapeGoString(node.title)))
		p.g.P(fmt.Sprintf(`%s.Description = "%s"`, recv, escapeGoString(node.description)))
	} else {
		if node.title != "" {
			p.g.P(fmt.Sprintf(`%s.Title = "%s"`, recv, escapeGoString(node.title)))
		}
		if node.description != "" {
			p.g.P(fmt.Sprintf(`%s.Description = "%s"`, recv, escapeGoString(node.description)))
		}
	}
	if node.minItems != nil {
		p.g.P(fmt.Sprintf(`%s.MinItems = &[]int{%d}[0]`, recv, *node.minItems))
	}
	if node.maxItems != nil {
		p.g.P(fmt.Sprintf(`%s.MaxItems = &[]int{%d}[0]`, recv, *node.maxItems))
	}
	if node.uniqueItems {
		p.g.P(fmt.Sprintf(`%s.UniqueItems = true`, recv))
	}
	if node.minProperties != nil {
		p.g.P(fmt.Sprintf(`%s.MinProperties = &[]int{%d}[0]`, recv, *node.minProperties))
	}
	if node.maxProperties != nil {
		p.g.P(fmt.Sprintf(`%s.MaxProperties = &[]int{%d}[0]`, recv, *node.maxProperties))
	}
	if node.deprecated {
		p.g.P(fmt.Sprintf(`%s.Deprecated = true`, recv))
	}
	if node.readOnly {
		p.g.P(fmt.Sprintf(`%s.ReadOnly = true`, recv))
	}
	if node.writeOnly {
		p.g.P(fmt.Sprintf(`%s.WriteOnly = true`, recv))
	}

	vc := node.value
	if vc.format != "" {
		p.g.P(fmt.Sprintf(`%s.Format = "%s"`, recv, escapeGoString(vc.format)))
	}
	if vc.pattern != "" {
		p.g.P(fmt.Sprintf(`%s.Pattern = "%s"`, recv, escapeGoString(vc.pattern)))
	}
	if vc.contentEncoding != "" {
		p.g.P(fmt.Sprintf(`%s.ContentEncoding = "%s"`, recv, escapeGoString(vc.contentEncoding)))
	}
	if vc.contentMediaType != "" {
		p.g.P(fmt.Sprintf(`%s.ContentMediaType = "%s"`, recv, escapeGoString(vc.contentMediaType)))
	}
	if vc.exclusiveMinimum != nil {
		p.g.P(fmt.Sprintf(`%s.ExclusiveMinimum = &[]float64{%g}[0]`, recv, *vc.exclusiveMinimum))
	} else if vc.minimum != nil {
		p.g.P(fmt.Sprintf(`%s.Minimum = &[]float64{%g}[0]`, recv, *vc.minimum))
	}
	if vc.exclusiveMaximum != nil {
		p.g.P(fmt.Sprintf(`%s.ExclusiveMaximum = &[]float64{%g}[0]`, recv, *vc.exclusiveMaximum))
	} else if vc.maximum != nil {
		p.g.P(fmt.Sprintf(`%s.Maximum = &[]float64{%g}[0]`, recv, *vc.maximum))
	}
	if vc.multipleOf != nil {
		p.g.P(fmt.Sprintf(`%s.MultipleOf = &[]float64{%g}[0]`, recv, *vc.multipleOf))
	}
	if vc.minLength != nil {
		p.g.P(fmt.Sprintf(`%s.MinLength = &[]int{%d}[0]`, recv, *vc.minLength))
	}
	if vc.maxLength != nil {
		p.g.P(fmt.Sprintf(`%s.MaxLength = &[]int{%d}[0]`, recv, *vc.maxLength))
	}
	if len(vc.enum) > 0 {
		p.g.P(fmt.Sprintf(`%s.Enum = []any{`, recv))
		for _, enumValue := range vc.enum {
			p.g.P(anyLiteral(enumValue) + `,`)
		}
		p.g.P(`}`)
	}
}

// printValueConstraints emits the value-level keywords of a schema literal.
func (p *schemaPrinter) printValueConstraints(vc valueConstraints) {
	if vc.format != "" {
		p.g.P(fmt.Sprintf(`Format: "%s",`, escapeGoString(vc.format)))
	}
	if vc.pattern != "" {
		p.g.P(fmt.Sprintf(`Pattern: "%s",`, escapeGoString(vc.pattern)))
	}
	if vc.contentEncoding != "" {
		p.g.P(fmt.Sprintf(`ContentEncoding: "%s",`, escapeGoString(vc.contentEncoding)))
	}
	if vc.contentMediaType != "" {
		p.g.P(fmt.Sprintf(`ContentMediaType: "%s",`, escapeGoString(vc.contentMediaType)))
	}

	if vc.exclusiveMinimum != nil {
		p.g.P(fmt.Sprintf(`ExclusiveMinimum: &[]float64{%g}[0],`, *vc.exclusiveMinimum))
	} else if vc.minimum != nil {
		p.g.P(fmt.Sprintf(`Minimum: &[]float64{%g}[0],`, *vc.minimum))
	}
	if vc.exclusiveMaximum != nil {
		p.g.P(fmt.Sprintf(`ExclusiveMaximum: &[]float64{%g}[0],`, *vc.exclusiveMaximum))
	} else if vc.maximum != nil {
		p.g.P(fmt.Sprintf(`Maximum: &[]float64{%g}[0],`, *vc.maximum))
	}
	if vc.multipleOf != nil {
		p.g.P(fmt.Sprintf(`MultipleOf: &[]float64{%g}[0],`, *vc.multipleOf))
	}

	if vc.minLength != nil {
		p.g.P(fmt.Sprintf(`MinLength: &[]int{%d}[0],`, *vc.minLength))
	}
	if vc.maxLength != nil {
		p.g.P(fmt.Sprintf(`MaxLength: &[]int{%d}[0],`, *vc.maxLength))
	}

	if len(vc.enum) > 0 {
		p.g.P(`Enum: []any{`)
		for _, enumValue := range vc.enum {
			p.g.P(anyLiteral(enumValue) + `,`)
		}
		p.g.P(`},`)
	}
}

// quotedList renders strings as a comma-separated list of Go string literals.
func quotedList(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = strconv.Quote(v)
	}
	return strings.Join(quoted, ", ")
}

// escapeGoString prepares a string for embedding in generated Go source code.
// It handles special characters (quotes, newlines, etc.) via strconv.Quote,
// then strips the outer quotes since the caller adds its own.
func escapeGoString(s string) string {
	quoted := strconv.Quote(s)
	return quoted[1 : len(quoted)-1]
}
