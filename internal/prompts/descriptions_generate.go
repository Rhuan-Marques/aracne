package prompts

func DescriptionsGenerateCommand() string {
	return `The following resources are missing descriptions and need them:

!` + "`ltp list-undocumented`" + `

For each resource listed above, call **read_resource_and_cut** with its ` + "`id`" + ` and ` + "`resource_name`" + `, then call **update_description** to write a concise description. Follow these guidelines:

- Functions: 1-3 lines covering purpose, parameters, return values, side effects
- Structs: 1-3 lines covering what it represents, key fields, usage
- Interfaces: 1-3 lines covering the contract and key methods
- Variables: 1 line covering what it stores and purpose
- Files: 1 line covering the file's role in its package
- Packages: 1-2 lines covering overall purpose

Process ALL resources listed above. Report how many descriptions were generated.`
}