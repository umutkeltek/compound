import { expect, test } from "bun:test"
import { mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises"
import { tmpdir } from "node:os"
import { join } from "node:path"

import { installOpenCode } from "./install.ts"

test("installer backs up and registers the local plugin without clobbering other config", async () => {
  const home = await mkdtemp(join(tmpdir(), "compound-opencode-install-"))
  const configDir = join(home, ".config", "opencode")
  const configPath = join(configDir, "opencode.jsonc")
  await mkdir(configDir, { recursive: true })
  const original =
    "{\n" +
    "  // keep the unrelated setting\n" +
    '  "theme": "fixture",\n' +
    '  "plugin": ["existing-plugin"]\n' +
    "}\n"
  await writeFile(configPath, original)
  const imports: string[] = []

  const first = await installOpenCode({
    home,
    compoundBinary: "/fixture/compound",
    importAuthority: async (binary, registration, importHome) => {
      imports.push([binary, registration, importHome].join("|"))
      return { hook_count: 3 }
    },
  })

  expect(await readFile(configPath + ".bak", "utf8")).toBe(original)
  expect(await readFile(first.plugin_path, "utf8")).toContain("export default CompoundPlugin")
  const config = Bun.JSONC.parse(await readFile(configPath, "utf8")) as {
    theme: string
    plugin: string[]
  }
  expect(config.theme).toBe("fixture")
  expect(config.plugin).toEqual(["existing-plugin", "./plugins/compound.ts"])
  expect(first.authority_import).toEqual({ hook_count: 3 })

  await installOpenCode({
    home,
    compoundBinary: "/fixture/compound",
    importAuthority: async () => ({ hook_count: 3 }),
  })
  const secondConfig = Bun.JSONC.parse(await readFile(configPath, "utf8")) as {
    plugin: string[]
  }
  expect(secondConfig.plugin).toEqual(["existing-plugin", "./plugins/compound.ts"])
  expect(imports).toHaveLength(1)
})
