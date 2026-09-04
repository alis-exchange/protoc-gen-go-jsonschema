package plugin

import (
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// cycleSet holds the messages that sit on a reference cycle: a message is
// present (true) when its schema must live under $defs and be reached through
// $ref, because inlining it would never terminate. Every other message
// inlines. Proto forbids circular imports, so a cycle always lives inside one
// .proto file; the answer for a message is therefore the same from every file
// that references it.
type cycleSet map[protoreflect.FullName]bool

// analyzeCycles runs Tarjan's strongly-connected-components algorithm over the
// "message references message" graph reachable from roots (and from their
// nested messages, which generate schemas of their own). The edges of a
// message are exactly its messageReferences: every non-ignored field whose
// value is a message — singular, repeated, map value, oneof variant, proto2
// group. Nesting alone is not an edge, and free-form well-known types are not
// nodes (they inline as untyped JSON).
//
// A message is cyclic when its component has more than one member or it
// references itself directly.
func analyzeCycles(roots []*protogen.Message) cycleSet {
	a := &cycleAnalyzer{
		index:   make(map[*protogen.Message]int),
		lowlink: make(map[*protogen.Message]int),
		onStack: make(map[*protogen.Message]bool),
		cycles:  make(cycleSet),
	}
	a.visitRoots(roots)
	return a.cycles
}

type cycleAnalyzer struct {
	next    int
	index   map[*protogen.Message]int
	lowlink map[*protogen.Message]int
	onStack map[*protogen.Message]bool
	stack   []*protogen.Message
	cycles  cycleSet
}

func (a *cycleAnalyzer) visitRoots(msgs []*protogen.Message) {
	for _, m := range msgs {
		if m.Desc.IsMapEntry() {
			continue
		}
		if _, seen := a.index[m]; !seen {
			a.strongConnect(m)
		}
		a.visitRoots(m.Messages)
	}
}

func (a *cycleAnalyzer) strongConnect(m *protogen.Message) {
	a.index[m] = a.next
	a.lowlink[m] = a.next
	a.next++
	a.stack = append(a.stack, m)
	a.onStack[m] = true

	selfEdge := false
	for _, t := range messageReferences(m) {
		if t == m {
			selfEdge = true
		}
		if _, seen := a.index[t]; !seen {
			a.strongConnect(t)
			a.lowlink[m] = min(a.lowlink[m], a.lowlink[t])
		} else if a.onStack[t] {
			a.lowlink[m] = min(a.lowlink[m], a.index[t])
		}
	}

	if a.lowlink[m] != a.index[m] {
		return
	}
	// m is the root of a component: pop it.
	var component []*protogen.Message
	for {
		n := len(a.stack) - 1
		t := a.stack[n]
		a.stack = a.stack[:n]
		a.onStack[t] = false
		component = append(component, t)
		if t == m {
			break
		}
	}
	if len(component) > 1 || selfEdge {
		for _, t := range component {
			a.cycles[t.Desc.FullName()] = true
		}
	}
}
