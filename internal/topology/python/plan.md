I think we can expand in the TopologyWarnings in a way that makes incremental updates manageable.
# Here is a rough draft, you should develop this into a more solid plan:
TopologyWarnings are saved as nodes in the database, and connected to Resources. Altough they should *not* share the "Connections" field, should be isolated.
Each resource has a list of Warnings its attached to, and you can use the DB to search for specific warnings.
Warnings have an ID, a source Node, a kind and (sometimes) a target. Warning targets don't have to exist yet, but since NodeIDs are predictable, it should be doable. If you find any situation where you couldn't predict a target ID, *ask me what to do*
A function calling another function that doesnt exist yet should create a warning with: source=nodeThatExistsID, kind=UseMissingNode, target=unexistantNodeId
Warnings should be created for more than just that. Editing functions input/output, removing functions/structs, trying to use functions, structs or packages that dont exist, etc.

When a file is edited, it should try and solve the warnings targeted to its resources. Be aware of resources that were JUST created or that were REMOVED by the edit and solve according to common sense depending on the warnings themselves.

Warnigns should be cleared at the start of full scan, even without --hard, and re-calculated during the scan.

It should be *efficient* and *fast* to search for warnings with a specific node as target or as source.