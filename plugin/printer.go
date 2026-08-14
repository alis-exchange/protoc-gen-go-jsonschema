package plugin

import (
	"fmt"
	"strconv"

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

// printMessageSchema emits the two generated functions for one message: the
// public JsonSchema() entry point (method for user types, standalone function
// for Google types) and the _WithDefs helper that populates the shared
// definitions map.
func (p *schemaPrinter) printMessageSchema(m *messageSchemaModel) {
	id := m.id

	// --- Public Entry Point ---
	// Ref-as-root pattern: return a $ref wrapper with full defs. This avoids
	// circular references when marshaling (root != defs[key]) and enables
	// recursive types.
	if id.isGoogle {
		p.g.P(fmt.Sprintf("// %s_JsonSchema returns the JSON schema for the %s message.", id.funcBase, id.protoName))
		p.g.P(fmt.Sprintf("func %s_JsonSchema() *jsonschema.Schema {", id.funcBase))
	} else {
		p.g.P(fmt.Sprintf("// JsonSchema returns the JSON schema for the %s message.", id.protoName))
		p.g.P(fmt.Sprintf("func (x *%s) JsonSchema() *jsonschema.Schema {", id.goName))
	}
	p.g.P("defs := make(map[string]*jsonschema.Schema)")
	p.g.P(fmt.Sprintf("_ = %s(defs)", id.withDefsName()))
	p.g.P(fmt.Sprintf("root := &jsonschema.Schema{Ref: \"#/$defs/%s\", Type: \"object\"}", id.defKey))
	p.g.P("root.Defs = defs")
	p.g.P("return root")
	p.g.P("}")
	p.g.P()

	// --- Internal Helper ---
	p.g.P(fmt.Sprintf("func %s(defs map[string]*jsonschema.Schema) *jsonschema.Schema {", id.withDefsName()))

	// Return early if already defined (handles circular references).
	p.g.P(fmt.Sprintf("if _, ok := defs[\"%s\"]; ok {", id.defKey))
	p.g.P(fmt.Sprintf("return &jsonschema.Schema{Ref: \"#/$defs/%s\"}", id.defKey))
	p.g.P("}")
	p.g.P()

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

	p.g.P(`// Register schema BEFORE processing fields to handle self-references.`)
	p.g.P(`// This prevents infinite recursion when a message contains itself.`)
	p.g.P(fmt.Sprintf("defs[\"%s\"] = schema", id.defKey))
	p.g.P()

	// --- Field Properties ---
	for _, prop := range m.fields {
		if prop.ref != nil {
			p.g.P(fmt.Sprintf(`schema.Properties["%s"] = %s`, prop.key, p.refCall(prop.ref)))
			if prop.node != nil {
				p.printRefSiblings(fmt.Sprintf(`schema.Properties["%s"]`, prop.key), prop.node)
			}
		} else {
			p.printFieldNode(prop.node, fmt.Sprintf(`schema.Properties["%s"] = `, prop.key), "")
		}
		p.g.P("")
	}

	// --- Google Types: Flat OneOf Constraints ---
	if len(m.flatGroups) == 1 {
		p.g.P(`schema.OneOf = []*jsonschema.Schema{`)
		p.printFlatGroupBranches(m.flatGroups[0].fields)
		p.g.P(`}`)
	} else if len(m.flatGroups) > 1 {
		p.g.P(`schema.AllOf = []*jsonschema.Schema{`)
		for _, group := range m.flatGroups {
			p.g.P(`{`)
			p.g.P(`OneOf: []*jsonschema.Schema{`)
			p.printFlatGroupBranches(group.fields)
			p.g.P(`},`)
			p.g.P(`},`)
		}
		p.g.P(`}`)
	}

	// --- User Messages: Nested PascalCase Oneof Wrappers ---
	for _, wrapper := range m.oneofWrappers {
		p.printOneofWrapper(wrapper)
		p.g.P("")
	}

	// Return a $ref to this message's schema definition.
	p.g.P(fmt.Sprintf("    return &jsonschema.Schema{Ref: \"#/$defs/%s\"}", id.defKey))
	p.g.P("}")
}

// printFlatGroupBranches emits the Required alternatives of a flat Google
// oneof group plus the "none present" branch that makes the group optional
// (proto3 semantics: a oneof does not require any alternative to be set).
func (p *schemaPrinter) printFlatGroupBranches(fields []string) {
	for _, f := range fields {
		p.g.P(fmt.Sprintf(`{Required: []string{"%s"}},`, f))
	}
	p.g.P(`{Not: &jsonschema.Schema{AnyOf: []*jsonschema.Schema{`)
	for _, f := range fields {
		p.g.P(fmt.Sprintf(`{Required: []string{"%s"}},`, f))
	}
	p.g.P(`}}},`)
}

// printOneofWrapper emits the nested PascalCase wrapper property for one user
// message oneof: a oneOf of a null branch (unset oneof) and an object branch
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
			// Ref with siblings inside a composite literal: a self-contained
			// closure decorates the fresh $ref schema the _WithDefs call
			// returns (never the definition itself).
			p.g.P(fmt.Sprintf(`"%s": func() *jsonschema.Schema {`, variant.key))
			p.g.P(fmt.Sprintf(`s := %s`, p.refCall(variant.ref)))
			p.printRefSiblings("s", variant.node)
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
			} else {
				// Fallback for external types without explicit type info.
				p.g.P(`Type: "object",`)
			}
			p.printValueConstraints(node.element.value)
			p.g.P(`},`)
		}
	} else {
		p.printValueConstraints(node.value)
	}

	if node.propertyNamesPattern != "" {
		p.g.P(`PropertyNames: &jsonschema.Schema{`)
		p.g.P(fmt.Sprintf(`Pattern: "%s",`, escapeGoString(node.propertyNamesPattern)))
		p.g.P(`},`)
	}

	p.g.P("}" + closing)
}

// printRefSiblings emits assignment statements that decorate a $ref schema
// with sibling keywords (Draft 2020-12 keeps siblings of $ref applicable).
// recv is the Go expression holding the fresh $ref schema — a Properties map
// index or a local variable. The _WithDefs call always returns a fresh
// &Schema{Ref: ...}, never the definition itself, so mutation is safe.
func (p *schemaPrinter) printRefSiblings(recv string, node *fieldNode) {
	if node.title != "" {
		p.g.P(fmt.Sprintf(`%s.Title = "%s"`, recv, escapeGoString(node.title)))
	}
	if node.description != "" {
		p.g.P(fmt.Sprintf(`%s.Description = "%s"`, recv, escapeGoString(node.description)))
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
	if vc.minLength != nil {
		p.g.P(fmt.Sprintf(`%s.MinLength = &[]int{%d}[0]`, recv, *vc.minLength))
	}
	if vc.maxLength != nil {
		p.g.P(fmt.Sprintf(`%s.MaxLength = &[]int{%d}[0]`, recv, *vc.maxLength))
	}
	if len(vc.enum) > 0 {
		p.g.P(fmt.Sprintf(`%s.Enum = []any{`, recv))
		for _, enumValue := range vc.enum {
			p.g.P(fmt.Sprintf(`%d,`, enumValue))
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

	if vc.minLength != nil {
		p.g.P(fmt.Sprintf(`MinLength: &[]int{%d}[0],`, *vc.minLength))
	}
	if vc.maxLength != nil {
		p.g.P(fmt.Sprintf(`MaxLength: &[]int{%d}[0],`, *vc.maxLength))
	}

	if len(vc.enum) > 0 {
		p.g.P(`Enum: []any{`)
		for _, enumValue := range vc.enum {
			p.g.P(fmt.Sprintf(`%d,`, enumValue))
		}
		p.g.P(`},`)
	}
}

// escapeGoString prepares a string for embedding in generated Go source code.
// It handles special characters (quotes, newlines, etc.) via strconv.Quote,
// then strips the outer quotes since the caller adds its own.
func escapeGoString(s string) string {
	quoted := strconv.Quote(s)
	return quoted[1 : len(quoted)-1]
}
