package prompts

// Returns instructions to delete stored topology descriptions, optionally filtered by resource kind.
func DescriptionsClearCommand() string {
	return "Run `arac descriptions clear $ARGUMENTS` to delete stored topology descriptions. If arguments were provided, pass them exactly; examples: `--target function,struct` or `--target [Function, Struct]`. Report how many descriptions were cleared."
}
