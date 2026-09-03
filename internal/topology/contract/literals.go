package contract

// LiteralToken maps a parse-node type name to the untyped-constant class it denotes, or ""
// when the node is not a literal.
//
// Shared by the tree-sitter scanners because their grammars agree on most of these names,
// and because the classification has to agree across languages anyway: a literal argument
// is the single most common thing a call passes, and the whole point of recording a CLASS
// rather than a type is that one table of assignability rules can then serve every
// language. A name this table does not know yields "", which records the argument as
// unreadable -- the safe direction, since an unreadable argument produces no verdict while
// a wrongly classified one produces a false warning.
func LiteralToken(nodeType string) string {
	switch nodeType {
	// Integers. JavaScript and TypeScript spell every numeric literal "number", which is
	// exactly right for them: the language has one numeric type, so the distinction the
	// other grammars draw does not exist.
	case "integer_literal", "decimal_integer_literal", "hex_integer_literal",
		"octal_integer_literal", "binary_integer_literal", "number", "int_literal":
		return UntypedInt
	case "float_literal", "decimal_floating_point_literal", "hex_floating_point_literal":
		return UntypedFloat
	case "string_literal", "raw_string_literal", "template_string", "string",
		"interpreted_string_literal", "text_block":
		return UntypedString
	case "char_literal", "character_literal":
		return UntypedRune
	case "boolean_literal", "true", "false":
		return UntypedBool
	case "null_literal", "null", "undefined", "none":
		return UntypedNil
	}
	return ""
}
