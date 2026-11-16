/**
 * Which app or codebase a work item belongs to. One pure resolver, so the app
 * page and the Work page group work the same way, from data the system
 * already owns: the app catalog (`compound product growth --json`: each app's
 * id, name and aliases) and the product inventory (`compound system inventory
 * --kind product --json`: each product's `code_in` folders). Nothing here
 * names an app, a system or a personal folder; roots and names come from the
 * data. A work item resolves to one of:
 *
 *  - an app from the catalog (`kind: "app"`, id = the catalog `product:` id);
 *  - a codebase: a folder directly under a projects group folder that is not
 *    an app (`kind: "codebase"`, id `codebase:<name>`);
 *  - a product the record itself names that the catalog does not list
 *    (`kind: "unlisted"`, id `unlisted:<name>`);
 *  - not linked (`kind: "none"`), when nothing on the record names either.
 *
 * The product-identity owner's app groups (`compound product where --all
 * --json`) can be folded in first (`foldAppGroups`): their id spellings join
 * the catalog aliases and their folders join the code folders.
 *
 * Rules, first match wins (`via` says which one placed the item):
 *  0. `scope`: the record's own `scope.product_id`, folded through the
 *     catalog ids and aliases. When it names no catalog app the record is
 *     its own `unlisted` group (`scope-unlisted`): a recorded product is never
 *     overruled by a guess from folders.
 *  1. `product-id`: a `product:<x>` surface whose id, compared lowercase
 *     without `-`/`_` and cut at a second `:`, is a catalog app's id or alias.
 *  2. `code-folder`: the worktree folder (`target_worktree_root`) or a folder
 *     surface lies inside a folder the inventory records as an app's code.
 *  3. `folder-name`: the folder under the projects root, or any folder of a
 *     path outside it (a lane folder), starts with the name of a catalog app
 *     or of an app's code folder (`<app>-release-2` → that app).
 *     `surface-name`: a surface that is a plain name (`<app>-pricing-fix`,
 *     `<app>:topic`) starts with a catalog app's name.
 *  4. `codebase-folder`: the folder directly under a projects group folder
 *     names a codebase. The projects root is learned from the inventory's
 *     own folders (`~/<root>/<group>/<name>` next to `<group>/<name>`).
 *  5. `codebase-name` (batch only, `resolveProjects`): a surface or folder
 *     name that starts with the name of a codebase that rule 4 found for
 *     other work in the same list (`<codebase>:topic`, a lane folder
 *     `<codebase>-task-date`, `system:<codebase>`).
 *  A folder under an operating-system temporary directory is never evidence
 *  (`via: "temp-folder"` when it was the only one). Anything else is
 *  `none`: the record names no app or codebase.
 *
 * Pure: no reads, no clock. Imports use relative `.ts` paths so the read layer
 * (lib/compound.ts) can load this file under the node strip-types runner.
 */

import { isInProgress, isOnHold, isStale, isWaitingForCheck } from "./work-state.ts";

/** An app from the catalog. `GrowthApp` (lib/growth.ts) has this shape. */
export type ProjectCatalogApp = { readonly productId: string; readonly name: string; readonly aliases?: readonly string[] };

/** A product from the inventory with the folders its code lives in (`code_in` evidence). */
export type ProjectInventoryProduct = { readonly id: string; readonly codeIn: readonly string[] };

/**
 * `app`: a catalog app. `codebase`: a folder under a projects group folder
 * that is not an app. `unlisted`: a product the record itself names (its
 * `scope.product_id`) that the catalog does not list. `none`: not linked.
 */
export type WorkProjectKind = "app" | "codebase" | "unlisted" | "none";

export type WorkProjectVia =
  | "scope"
  | "scope-unlisted"
  | "product-id"
  | "code-folder"
  | "folder-name"
  | "surface-name"
  | "codebase-folder"
  | "codebase-name"
  | "temp-folder"
  | "none";

export type WorkProject = {
  /** a catalog `product:` id, `codebase:<name>`, `unlisted:<name>`, or NOT_LINKED_PROJECT_ID */
  id: string;
  /** the catalog app's name, the codebase folder's name, the recorded product id, or NOT_LINKED_PROJECT_NAME */
  name: string;
  kind: WorkProjectKind;
  /** the rule that placed the item */
  via: WorkProjectVia;
};

export const NOT_LINKED_PROJECT_ID = "none";
export const NOT_LINKED_PROJECT_NAME = "Not linked to a project";

/** Folders whose contents are temporary on macOS and Linux. Work there is never placed by its folder. */
const TEMP_ROOTS = ["/tmp", "/private/tmp", "/var/tmp", "/private/var/tmp", "/var/folders", "/private/var/folders"];

/** Operating-system home roots: `/Users/<name>`, `/home/<name>`. Their first two folders are never a name. */
const HOME_ROOTS = new Set(["users", "home"]);

type Row = { readonly [field: string]: unknown };

const str = (value: unknown): string => (typeof value === "string" ? value.trim() : "");

/** Lowercase, letters and digits only, `product:` and anything after a second `:` dropped. */
function idKey(raw: string): string {
  let value = raw.trim().toLowerCase();
  if (value.startsWith("product:")) value = value.slice("product:".length);
  const cut = value.indexOf(":");
  if (cut >= 0) value = value.slice(0, cut);
  return value.replace(/[^\p{L}\p{N}]+/gu, "");
}

/** Lowercase name tokens: `PocketLog-release_2` → ["pocketlog", "release", "2"]. */
function tokens(name: string): string[] {
  return name.split(/[^\p{L}\p{N}]+/u).filter(Boolean).map((token) => token.toLowerCase());
}

const isAnchored = (path: string): boolean => path.startsWith("/") || path.startsWith("~/");
const isTemp = (path: string): boolean => TEMP_ROOTS.some((root) => path === root || path.startsWith(`${root}/`));

/** Path folders as written (`~` dropped), and lowercased for comparison. */
function folders(path: string): { names: string[]; lower: string[] } {
  const names = path.split("/").filter((part) => part && part !== "." && part !== "~");
  return { names, lower: names.map((name) => name.toLowerCase()) };
}

type Prepared = {
  /** normalized catalog id, alias or name → app; null when two apps claim it */
  catalogKeys: Map<string, ProjectCatalogApp | null>;
  /** the catalog keys plus each app's code-folder names */
  apps: Map<string, ProjectCatalogApp | null>;
  /** inventory code folders (lowercase segments) of catalog apps */
  codeFolders: { segments: string[]; app: ProjectCatalogApp }[];
  /** projects roots as their last folders (`~/<root>` → [root]) */
  rootTails: string[][];
  /** group folders directly under a projects root (`<group>/<name>`) */
  groups: Set<string>;
};

const preparedCache = new WeakMap<object, WeakMap<object, Prepared>>();

function prepare(catalog: readonly ProjectCatalogApp[], inventory: readonly ProjectInventoryProduct[]): Prepared {
  const byInventory = preparedCache.get(catalog);
  const cached = byInventory?.get(inventory);
  if (cached) return cached;

  const catalogKeys = new Map<string, ProjectCatalogApp | null>();
  const claim = (map: Map<string, ProjectCatalogApp | null>, key: string, app: ProjectCatalogApp) => {
    if (!key) return;
    const have = map.get(key);
    if (have === undefined) map.set(key, app);
    else if (have !== null && have !== app) map.set(key, null);
  };
  for (const app of catalog) {
    claim(catalogKeys, idKey(app.productId), app);
    claim(catalogKeys, idKey(app.name), app);
    for (const alias of app.aliases ?? []) claim(catalogKeys, idKey(alias), app);
  }
  const apps = new Map(catalogKeys);

  const groups = new Set<string>();
  for (const product of inventory) {
    for (const folder of product.codeIn) {
      if (!isAnchored(folder)) {
        const group = folders(folder).lower[0];
        if (group) groups.add(group);
      }
    }
  }

  const rootTails: string[][] = [];
  const codeFolders: Prepared["codeFolders"] = [];
  const folderNames = new Map<string, ProjectCatalogApp | null>();
  for (const product of inventory) {
    const app = apps.get(idKey(product.id)) ?? null;
    for (const folder of product.codeIn) {
      const { lower } = folders(folder);
      if (!lower.length) continue;
      let groupAt = 0;
      if (isAnchored(folder)) {
        groupAt = lower.findIndex((segment) => groups.has(segment));
        if (groupAt > 0) {
          const tail = lower.slice(0, groupAt);
          if (!rootTails.some((known) => known.join("/") === tail.join("/"))) rootTails.push(tail);
        }
      }
      if (!app) continue;
      codeFolders.push({ segments: lower, app });
      if (groupAt >= 0 && lower[groupAt + 1]) claim(folderNames, idKey(lower[groupAt + 1]), app);
    }
  }
  // A code folder's own name (`<group>/<name>`) also names its app, unless a catalog key already does.
  for (const [key, app] of folderNames) if (app && !apps.has(key)) apps.set(key, app);

  const prepared: Prepared = { catalogKeys, apps, codeFolders, rootTails, groups };
  const next = byInventory ?? new WeakMap<object, Prepared>();
  next.set(inventory, prepared);
  if (!byInventory) preparedCache.set(catalog, next);
  return prepared;
}

/** The longest name prefix (whole tokens) that is a known key: `pocketlog-release-2` → "pocketlog". */
function byLeadingWords<T>(name: string, known: ReadonlyMap<string, T | null>, skip = ""): T | null {
  const words = tokens(name);
  for (let n = words.length; n >= 1; n -= 1) {
    const key = words.slice(0, n).join("");
    if (key === skip) continue;
    const hit = known.get(key);
    if (hit) return hit;
  }
  return null;
}

/** The app whose code folder holds this path (longest folder wins; a tie between two apps places nothing). */
function codeFolderApp(lower: readonly string[], prepared: Prepared): ProjectCatalogApp | null {
  let best: { length: number; app: ProjectCatalogApp | null } = { length: 0, app: null };
  for (const folder of prepared.codeFolders) {
    const size = folder.segments.length;
    if (size < best.length || size > lower.length) continue;
    for (let at = 0; at + size <= lower.length; at += 1) {
      if (!folder.segments.every((segment, i) => lower[at + i] === segment)) continue;
      if (size > best.length) best = { length: size, app: folder.app };
      else if (best.app !== folder.app) best = { length: size, app: null };
      break;
    }
  }
  return best.app;
}

/** Index of the folder directly under a projects group folder, or -1. */
function projectFolderAt(lower: readonly string[], prepared: Prepared): number {
  for (const tail of prepared.rootTails) {
    for (let at = 0; at + tail.length + 1 < lower.length; at += 1) {
      if (tail.every((segment, i) => lower[at + i] === segment)) return at + tail.length + 1;
    }
  }
  const group = lower.findIndex((segment, i) => prepared.groups.has(segment) && i + 1 < lower.length);
  return group >= 0 ? group + 1 : -1;
}

/** Folders a name scan may read: everything after an OS home root's `<root>/<name>`. */
function scannable(names: readonly string[], lower: readonly string[]): string[] {
  return HOME_ROOTS.has(lower[0] ?? "") ? names.slice(2) : [...names];
}

/** The folder paths a record names: its worktree folder, a folder surface, or a folder after `ns:`. */
function folderEvidence(work: Row): { usable: string[]; onlyTemp: boolean } {
  const delivery = work.git_delivery && typeof work.git_delivery === "object" ? (work.git_delivery as Row) : {};
  const surface = str(work.canonical_surface);
  const afterNamespace = surface.includes(":") ? surface.slice(surface.indexOf(":") + 1) : "";
  const candidates = [str(work.target_worktree_root) || str(delivery.target_worktree_root), surface, afterNamespace].filter(
    (path) => path && isAnchored(path),
  );
  const usable = candidates.filter((path) => !isTemp(path));
  return { usable, onlyTemp: candidates.length > 0 && usable.length === 0 };
}

/** Names a non-folder surface offers: `<a>`, or `<a>` and `<b>` of `<a>:<b>`, each up to its first `/`. */
function surfaceNames(surface: string): string[] {
  if (!surface || isAnchored(surface)) return [];
  return surface
    .split(":")
    .slice(0, 2)
    .map((part) => part.split("/")[0] ?? "")
    .filter(Boolean);
}

const appProject = (app: ProjectCatalogApp, via: WorkProjectVia): WorkProject => ({ id: app.productId, name: app.name, kind: "app", via });

function notLinked(via: WorkProjectVia): WorkProject {
  return { id: NOT_LINKED_PROJECT_ID, name: NOT_LINKED_PROJECT_NAME, kind: "none", via };
}

function codebaseProject(name: string, via: WorkProjectVia): WorkProject {
  return { id: `codebase:${idKey(name)}`, name, kind: "codebase", via };
}

/** Rules 0-4 for one record. */
function resolveOne(work: Row, prepared: Prepared): WorkProject {
  const recorded = str(record(work.scope).product_id);
  const recordedKey = idKey(recorded);
  if (recordedKey) {
    const app = prepared.catalogKeys.get(recordedKey);
    if (app) return appProject(app, "scope");
    const name = recorded.replace(/^product:/i, "").trim();
    return { id: `unlisted:${recordedKey}`, name, kind: "unlisted", via: "scope-unlisted" };
  }
  const surface = str(work.canonical_surface);
  if (surface.toLowerCase().startsWith("product:")) {
    const app = prepared.apps.get(idKey(surface));
    if (app) return appProject(app, "product-id");
  }
  const { usable, onlyTemp } = folderEvidence(work);
  for (const path of usable) {
    const app = codeFolderApp(folders(path).lower, prepared);
    if (app) return appProject(app, "code-folder");
  }
  for (const path of usable) {
    const { names, lower } = folders(path);
    const at = projectFolderAt(lower, prepared);
    if (at >= 0) {
      const app = byLeadingWords(names[at], prepared.apps);
      return app ? appProject(app, "folder-name") : codebaseProject(names[at], "codebase-folder");
    }
    for (const name of scannable(names, lower)) {
      const app = byLeadingWords(name, prepared.apps);
      if (app) return appProject(app, "folder-name");
    }
  }
  for (const name of surfaceNames(surface)) {
    const app = byLeadingWords(name, prepared.apps);
    if (app) return appProject(app, "surface-name");
  }
  return notLinked(onlyTemp ? "temp-folder" : "none");
}

/**
 * Which app or codebase one work record belongs to, and by which rule. A
 * work record is any shape that carries `canonical_surface` and a worktree
 * folder (`target_worktree_root` on workbench rows, `git_delivery.
 * target_worktree_root` on `work list` rows). Rule 5 needs the other records
 * of the list, so a record that names a codebase only by name (`<codebase>:
 * topic`) is not linked here; use `resolveProjects` (or the index) to place
 * a list the way every page does.
 */
export function resolveProject(
  work: Row,
  catalog: readonly ProjectCatalogApp[],
  inventory: readonly ProjectInventoryProduct[],
): WorkProject {
  return resolveOne(work, prepare(catalog, inventory));
}

/**
 * Resolve a whole list, in order. Rules 1-4 run per record; then the
 * codebases they found name the rest (rule 5): a not-linked record whose
 * surface or folder starts with a codebase's name joins it, and a codebase
 * folder named `<codebase>-<more>` (a sibling worktree folder) joins
 * `<codebase>`.
 */
export function resolveProjects(
  works: readonly Row[],
  catalog: readonly ProjectCatalogApp[],
  inventory: readonly ProjectInventoryProduct[],
): WorkProject[] {
  const prepared = prepare(catalog, inventory);
  const first = works.map((work) => resolveOne(work, prepared));
  const codebases = new Map<string, WorkProject | null>();
  for (const project of first) {
    if (project.kind === "codebase" && !codebases.has(project.id.slice("codebase:".length))) {
      codebases.set(project.id.slice("codebase:".length), codebaseProject(project.name, "codebase-name"));
    }
  }
  if (!codebases.size) return first;
  const joins = new Map<string, WorkProject>();
  for (const [key, project] of codebases) {
    // Follow `<codebase>-a-b` → `<codebase>-a` → `<codebase>`: each step is a
    // strictly shorter name, so the walk ends.
    let target: WorkProject | null = null;
    let current = project;
    let skip = key;
    while (current) {
      const next = byLeadingWords(current.name, codebases, skip);
      if (!next) break;
      target = next;
      current = next;
      skip = next.id.slice("codebase:".length);
    }
    if (target) joins.set(`codebase:${key}`, target);
  }
  return works.map((work, i) => {
    const project = first[i];
    if (project.kind === "codebase") return joins.get(project.id) ?? project;
    if (project.kind !== "none" || project.via === "temp-folder") return project;
    const surface = str(work.canonical_surface);
    const names = [
      ...folderEvidence(work).usable.flatMap((path) => {
        const { names: all, lower } = folders(path);
        return scannable(all, lower);
      }),
      ...surfaceNames(surface),
    ];
    for (const name of names) {
      const codebase = byLeadingWords(name, codebases);
      if (codebase) return codebase;
    }
    return project;
  });
}

/* ---------- reading the catalog and inventory projections ---------- */

const list = (value: unknown): unknown[] => (Array.isArray(value) ? value : []);
const record = (value: unknown): Row => (value && typeof value === "object" ? (value as Row) : {});

/** The app catalog from `compound product growth --json` (`apps[]`); null when the payload has no app list. */
export function catalogFromGrowth(growth: unknown): ProjectCatalogApp[] | null {
  const apps = record(growth).apps;
  if (!Array.isArray(apps)) return null;
  return apps
    .map((raw) => {
      const app = record(raw);
      const productId = str(app.product_id) || str(app.productId);
      const aliases = list(app.aliases).map(str).filter(Boolean);
      return { productId, name: str(app.name) || productId, aliases };
    })
    .filter((app) => app.productId);
}

/** Products and their `code_in` folders from `compound system inventory --kind product --json`; null when it has no resource list. */
export function inventoryFromJson(inventory: unknown): ProjectInventoryProduct[] | null {
  const resources = record(inventory).resources;
  if (!Array.isArray(resources)) return null;
  return resources
    .map((raw) => {
      const resource = record(raw);
      const codeIn = list(resource.links)
        .map(record)
        .filter((link) => str(link.rel) === "code_in")
        .map((link) => str(link.evidence))
        .filter(Boolean);
      return { id: str(resource.id), codeIn };
    })
    .filter((product) => product.id);
}

/**
 * One app as the product-identity owner groups it (`compound product where
 * --all --json`): its id, every id spelling that folds into it (`members`),
 * and what it claims (`surfaces`: folders, web addresses, store ids).
 */
export type ProjectAppGroup = { readonly productId: string; readonly members: readonly string[]; readonly surfaces: readonly string[] };

/** The app groups from `compound product where --all --json` (a list); null when the payload is not a list. */
export function appGroupsFromWhere(where: unknown): ProjectAppGroup[] | null {
  if (!Array.isArray(where)) return null;
  return where
    .map((raw) => {
      const group = record(raw);
      return {
        productId: str(group.product_id),
        members: list(group.members).map(str).filter(Boolean),
        surfaces: list(group.surfaces).map(str).filter(Boolean),
      };
    })
    .filter((group) => group.productId);
}

/**
 * A surface that is a folder or a file, as a path: `/abs/path`, `~/path`,
 * `${HOME}/path` (written as `~/path`), `file://<path>`, `file-exists:<path>`.
 * Web addresses, store ids, remotes and commands are not folders: "".
 */
function surfaceFolder(surface: string): string {
  const path = surface.trim().replace(/^file-exists:/, "").replace(/^file:\/\//, "").replace(/^\$\{HOME\}(?=\/)/, "~");
  return isAnchored(path) ? path : "";
}

/**
 * Fold the identity owner's app groups into the catalog and inventory: every
 * `members` spelling becomes an alias of the catalog app it folds into, and
 * every folder in `surfaces` becomes one of that app's code folders. The
 * catalog's id stays the app's id (app page addresses use it; the two sources
 * pick different primary spellings for some apps). A group no catalog app
 * matches is left out: the catalog is the list of apps.
 */
export function foldAppGroups(
  catalog: readonly ProjectCatalogApp[],
  inventory: readonly ProjectInventoryProduct[],
  groups: readonly ProjectAppGroup[],
): { catalog: ProjectCatalogApp[]; inventory: ProjectInventoryProduct[] } {
  const keyed = new Map<string, number | null>();
  catalog.forEach((app, index) => {
    for (const key of [app.productId, ...(app.aliases ?? [])].map(idKey)) {
      if (!key) continue;
      const have = keyed.get(key);
      keyed.set(key, have === undefined || have === index ? index : null);
    }
  });
  const aliases = catalog.map((app) => new Set(app.aliases ?? []));
  const folders: ProjectInventoryProduct[] = [];
  for (const group of groups) {
    const matches = new Set(
      [group.productId, ...group.members].map((id) => keyed.get(idKey(id))).filter((index): index is number => typeof index === "number"),
    );
    if (matches.size !== 1) continue; // no catalog app, or two: place nothing
    const index = [...matches][0];
    for (const id of [group.productId, ...group.members]) if (id !== catalog[index].productId) aliases[index].add(id);
    const codeIn = group.surfaces.map(surfaceFolder).filter(Boolean);
    if (codeIn.length) folders.push({ id: catalog[index].productId, codeIn });
  }
  return {
    catalog: catalog.map((app, index) => ({ ...app, aliases: [...aliases[index]] })),
    inventory: [...inventory, ...folders],
  };
}

/* ---------- the per-project index pages share ---------- */

export type ProjectWorkCounts = {
  /** open work items */
  open: number;
  /** on hold or blocked: waiting for your decision (lib/work-state.ts `isOnHold`) */
  needYou: number;
  /** in progress, including waiting for a check (`isInProgress`) */
  inProgress: number;
  /** of those, finished running and waiting for a proof check (`isWaitingForCheck`) */
  waitingForCheck: number;
  /** of those, picked up more than STALE_AFTER_DAYS days ago (`isStale`) */
  stale: number;
};

export type ProjectSummary = ProjectWorkCounts & { id: string; name: string; kind: WorkProjectKind };

export type WorkProjectIndex = {
  /** work id → project id, for every open work item */
  byWork: Record<string, string>;
  /** one row per project that holds open work, in the one order (`compareProjects`) */
  projects: ProjectSummary[];
  /** the same counts over all open work; each equals the sum over `projects` */
  total: ProjectWorkCounts;
};

const emptyCounts = (): ProjectWorkCounts => ({ open: 0, needYou: 0, inProgress: 0, waitingForCheck: 0, stale: 0 });

function addTo(counts: ProjectWorkCounts, work: Row, nowMs: number): void {
  counts.open += 1;
  if (isOnHold(work.state)) counts.needYou += 1;
  if (isInProgress(work.state)) counts.inProgress += 1;
  if (isWaitingForCheck(work.state)) counts.waitingForCheck += 1;
  if (isStale(work, nowMs)) counts.stale += 1;
}

/**
 * The one order of projects: most items that need you first, then most
 * stale, most in progress, most open, then name. "Not linked" sorts by the
 * same rule as every other project, never last by default.
 */
export function compareProjects(a: ProjectSummary, b: ProjectSummary): number {
  return (
    b.needYou - a.needYou ||
    b.stale - a.stale ||
    b.inProgress - a.inProgress ||
    b.open - a.open ||
    a.name.localeCompare(b.name) ||
    a.id.localeCompare(b.id)
  );
}

/**
 * Group open work by project. `works` are the open work rows (workbench
 * `open[]`); a row repeated by work id counts once. Pass the clock in.
 */
export function buildWorkProjectIndex(
  works: readonly Row[],
  catalog: readonly ProjectCatalogApp[],
  inventory: readonly ProjectInventoryProduct[],
  nowMs: number,
): WorkProjectIndex {
  const seen = new Set<string>();
  const rows = works.filter((work) => {
    const id = str(work.work_id) || str(work.id);
    if (!id || seen.has(id)) return false;
    seen.add(id);
    return true;
  });
  const placed = resolveProjects(rows, catalog, inventory);
  const byWork: Record<string, string> = {};
  const projects = new Map<string, ProjectSummary>();
  const total = emptyCounts();
  rows.forEach((work, i) => {
    const project = placed[i];
    byWork[str(work.work_id) || str(work.id)] = project.id;
    const summary = projects.get(project.id) ?? { id: project.id, name: project.name, kind: project.kind, ...emptyCounts() };
    addTo(summary, work, nowMs);
    addTo(total, work, nowMs);
    projects.set(project.id, summary);
  });
  return { byWork, projects: [...projects.values()].sort(compareProjects), total };
}
