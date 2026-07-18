import { copyFile, mkdir, readFile, rename, stat } from "node:fs/promises"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"

const adapterDir = dirname(fileURLToPath(import.meta.url))
const pluginSource = join(adapterDir, "compound.ts")
const registrationSource = join(adapterDir, "hook-registration.ts")
const pluginRegistration = "./plugins/compound.ts"

type ImportAuthority = (binary: string, registration: string, home: string) => Promise<unknown>

export type InstallOptions = {
  home: string
  compoundBinary: string
  importAuthority?: ImportAuthority
}

export type InstallReport = {
  config_path: string
  plugin_path: string
  backups: string[]
  plugin_registration: string
  authority_import: unknown
}

async function exists(path: string): Promise<boolean> {
  try {
    await stat(path)
    return true
  } catch {
    return false
  }
}

async function backupOnce(path: string): Promise<string | undefined> {
  if (!(await exists(path))) return undefined
  const backup = path + ".bak"
  if (!(await exists(backup))) await copyFile(path, backup)
  return backup
}

async function writeAtomic(path: string, contents: string): Promise<void> {
  await mkdir(dirname(path), { recursive: true })
  const temporary = path + ".compound-install.tmp"
  await Bun.write(temporary, contents)
  await rename(temporary, path)
}

function parseConfig(raw: string, path: string): Record<string, unknown> {
  if (!raw.trim()) return {}
  const parsed = Bun.JSONC.parse(raw) as unknown
  if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") {
    throw new Error(path + " must contain a JSON object")
  }
  return parsed as Record<string, unknown>
}

export const runAuthorityImport: ImportAuthority = async (binary, registration, home) => {
  const proc = Bun.spawn(
    [binary, "contract", "import-hooks", "--hook-registration-ts", registration],
    {
      env: { ...process.env, HOME: home },
      stdout: "pipe",
      stderr: "pipe",
    },
  )
  const [exitCode, stdout, stderr] = await Promise.all([
    proc.exited,
    new Response(proc.stdout).text(),
    new Response(proc.stderr).text(),
  ])
  if (exitCode !== 0) {
    throw new Error(
      "installed-hooks import failed (" +
        exitCode +
        "): " +
        stderr.replace(/\s+/g, " ").trim(),
    )
  }
  return JSON.parse(stdout) as unknown
}

export async function installOpenCode(options: InstallOptions): Promise<InstallReport> {
  const configDir = join(options.home, ".config", "opencode")
  const configPath = join(configDir, "opencode.jsonc")
  const pluginPath = join(configDir, "plugins", "compound.ts")
  const backups = (
    await Promise.all([backupOnce(configPath), backupOnce(pluginPath)])
  ).filter((path): path is string => Boolean(path))

  const raw = (await exists(configPath)) ? await readFile(configPath, "utf8") : "{}"
  const config = parseConfig(raw, configPath)
  const plugins = Array.isArray(config.plugin) ? [...config.plugin] : []
  if (!plugins.includes(pluginRegistration)) plugins.push(pluginRegistration)
  config.plugin = plugins

  const serialized = JSON.stringify(config, null, 2) + "\n"
  JSON.parse(serialized)
  await writeAtomic(configPath, serialized)
  await mkdir(dirname(pluginPath), { recursive: true })
  await copyFile(pluginSource, pluginPath)

  const authorityImport = await (options.importAuthority ?? runAuthorityImport)(
    options.compoundBinary,
    registrationSource,
    options.home,
  )

  return {
    config_path: configPath,
    plugin_path: pluginPath,
    backups,
    plugin_registration: pluginRegistration,
    authority_import: authorityImport,
  }
}

function valueFlag(args: string[], name: string): string | undefined {
  const index = args.indexOf(name)
  return index >= 0 ? args[index + 1] : undefined
}

if (import.meta.main) {
  const args = Bun.argv.slice(2)
  const home = valueFlag(args, "--home") ?? process.env.HOME
  const compoundBinary =
    valueFlag(args, "--compound") ?? process.env.COMPOUND_BIN ?? Bun.which("compound")
  if (!home || !compoundBinary) {
    console.error("usage: bun adapters/opencode/install.ts [--home PATH] [--compound PATH]")
    process.exit(2)
  }
  try {
    console.log(JSON.stringify(await installOpenCode({ home, compoundBinary }), null, 2))
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error))
    process.exit(1)
  }
}
