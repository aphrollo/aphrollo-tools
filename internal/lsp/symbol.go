package lsp

// SymbolKind is an LSP symbol kind as returned by textDocument/documentSymbol.
// Values follow the LSP spec's SymbolKind enumeration (1-26).
type SymbolKind int

// LSP SymbolKind values (spec enumeration). Only the ones we name explicitly
// are exported; Name covers the full range.
const (
	KindFile          SymbolKind = 1
	KindModule        SymbolKind = 2
	KindNamespace     SymbolKind = 3
	KindPackage       SymbolKind = 4
	KindClass         SymbolKind = 5
	KindMethod        SymbolKind = 6
	KindProperty      SymbolKind = 7
	KindField         SymbolKind = 8
	KindConstructor   SymbolKind = 9
	KindEnum          SymbolKind = 10
	KindInterface     SymbolKind = 11
	KindFunction      SymbolKind = 12
	KindVariable      SymbolKind = 13
	KindConstant      SymbolKind = 14
	KindString        SymbolKind = 15
	KindNumber        SymbolKind = 16
	KindBoolean       SymbolKind = 17
	KindArray         SymbolKind = 18
	KindObject        SymbolKind = 19
	KindKey           SymbolKind = 20
	KindNull          SymbolKind = 21
	KindEnumMember    SymbolKind = 22
	KindStruct        SymbolKind = 23
	KindEvent         SymbolKind = 24
	KindOperator      SymbolKind = 25
	KindTypeParameter SymbolKind = 26
)

// symbolKindNames maps each SymbolKind to a short, lowercase label suited to a
// compact outline (e.g. "func", "struct"). Indexed by kind value.
var symbolKindNames = map[SymbolKind]string{
	KindFile:          "file",
	KindModule:        "module",
	KindNamespace:     "namespace",
	KindPackage:       "package",
	KindClass:         "class",
	KindMethod:        "method",
	KindProperty:      "property",
	KindField:         "field",
	KindConstructor:   "constructor",
	KindEnum:          "enum",
	KindInterface:     "interface",
	KindFunction:      "func",
	KindVariable:      "var",
	KindConstant:      "const",
	KindString:        "string",
	KindNumber:        "number",
	KindBoolean:       "bool",
	KindArray:         "array",
	KindObject:        "object",
	KindKey:           "key",
	KindNull:          "null",
	KindEnumMember:    "enum-member",
	KindStruct:        "struct",
	KindEvent:         "event",
	KindOperator:      "operator",
	KindTypeParameter: "type-param",
}

// Name returns a short lowercase label for the kind, or "symbol" for any value
// outside the known LSP range.
func (k SymbolKind) Name() string {
	if name, ok := symbolKindNames[k]; ok {
		return name
	}
	return "symbol"
}

// DocumentSymbol is the hierarchical result of textDocument/documentSymbol.
// Range spans the whole symbol (including its body); SelectionRange spans just
// the name. Children nests members (methods, fields) under their container.
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail"`
	Kind           SymbolKind       `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children"`
}
