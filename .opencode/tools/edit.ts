import { tool } from "@opencode-ai/plugin"
import path from "path"

export default tool({
  description: "Edit a file by replacing exact text with new text. The project topology is automatically updated.",
  args: {
    file_path: tool.schema.string().describe("The absolute path to the file to edit"),
    old_string: tool.schema.string().describe("The exact text to search for and replace"),
    new_string: tool.schema.string().describe("The replacement text"),
  },
  async execute(args, context) {
    const filePath = args.file_path
    const oldStr = args.old_string
    const newStr = args.new_string

    const file = Bun.file(filePath)
    const content = await file.text()

    if (!content.includes(oldStr)) {
      return "old_string not found in " + filePath
    }

    const newContent = content.replace(oldStr, newStr)
    await Bun.write(filePath, newContent)

    const ltpPath = path.join(
      context.worktree,
      "ltp" + (process.platform === "win32" ? ".exe" : ""),
    )
    const proc = Bun.spawnSync([ltpPath, "update-file", filePath])
    if (proc.exitCode === 0) {
      const out = proc.stdout.toString().trim()
      if (out) {
        return "edit succeeded\n\nTopology warnings:\n" + out
      }
    }
    // topology DB missing or file not tracked -- edit still succeeded
    return "edit succeeded"
  },
})
