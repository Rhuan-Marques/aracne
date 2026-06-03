import { execFileSync } from "node:child_process"
import path from "node:path"

export const LtpNativeEditSync = async ({ directory, worktree }) => {
  const root = worktree ?? directory ?? process.cwd()

  function normalizeFile(file) {
    if (!file) return ""
    const normalized = path.isAbsolute(file) ? path.relative(root, file) : file
    if (!normalized || normalized.startsWith("..") || path.isAbsolute(normalized)) return file
    return normalized
  }

  function shouldSkip(file) {
    const parts = file.split(/[\\/]+/)
    return parts[0] === ".git" || parts[0] === ".ltp" || parts.includes("node_modules")
  }

  function updateFile(file) {
    file = normalizeFile(file)
    if (!file || shouldSkip(file)) return ""
    try {
      const text = execFileSync("ltp", ["update-file", file], { cwd: root, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] })
      if (/Warning number\s+0/.test(text)) return ""
      return `llm-topology warnings for ${file}:\n${text}`
    } catch (error) {
      const text = `${error.stdout?.toString?.() ?? ""}${error.stderr?.toString?.() ?? ""}`
      return `llm-topology update-file failed for ${file}:\n${text}`
    }
  }

  function pathsFromArgs(tool, args) {
    const paths = new Set()
    for (const key of ["file_path", "filePath", "path"]) if (args?.[key]) paths.add(args[key])
    if (Array.isArray(args?.edits)) {
      for (const edit of args.edits) {
        for (const key of ["file_path", "filePath", "path"]) if (edit?.[key]) paths.add(edit[key])
      }
    }
    if (tool === "apply_patch") {
      const patch = args?.patch ?? args?.patchText
      if (typeof patch === "string") {
        for (const match of patch.matchAll(/^[+]{3} b\/(.+)$/gm)) paths.add(match[1])
        for (const match of patch.matchAll(/^\*\*\* (?:Add|Update|Delete) File: (.+)$/gm)) paths.add(match[1])
        for (const match of patch.matchAll(/^\*\*\* Move to: (.+)$/gm)) paths.add(match[1])
      }
    }
    return [...paths]
  }

  return {
    event: async ({ event }) => {
      if (event.type !== "file.edited" && event.type !== "file.watcher.updated") return
      if (event.type === "file.watcher.updated" && event.properties?.event !== "change") return
      const message = updateFile(event.properties?.file)
      if (message) console.warn(message)
    },
    "tool.execute.after": async (input, output) => {
      if (!["edit", "write", "apply_patch", "multi_edit", "multiedit"].includes(input.tool)) return
      const messages = pathsFromArgs(input.tool, input.args).map(updateFile).filter(Boolean)
      if (messages.length > 0) output.output = `${output.output ?? ""}\n\n${messages.join("\n")}`
    },
  }
}
