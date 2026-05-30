# Plan: the `Harness` trait — centralize all per-harness logic

**Status:** proposed (Phase 2 of the maintainability refactor). Branch `refactor/maintainability`.
**Goal:** make "add a 7th harness" mean *adding one file + one registry line*, not editing ~15 sites.

---

## 1. The problem, by the numbers

agentpack supports six harnesses — **Claude, Cursor, Codex, OpenCode, Grok, Antigravity (agy)**.
Today each harness's behavior is smeared across the tree as parallel branches, lists, and
near-duplicate files. A full audit (`grep` for harness names + `HarnessTarget::` arms), re-verified against the tree on
branch `refactor/maintainability`, found **~18 distinct scatter sites** that must all be touched in
lockstep to add or change a harness:

| # | Site | File(s) | Shape of the scatter |
|---|------|---------|----------------------|
| 1 | Identity enum + per-harness data tables | `artifacts/harness.rs` (20 arms) | `raw_plugin_subdirs`, `seed_command_frontmatter`, `*_allowed_extra_frontmatter_keys`, `rendered_artifact_kind`, `disables_model_invocation_for_kind` — five separate `match self` tables |
| 2 | Staging path accessors | `staging/harnesses.rs` (9 `StagingPipeline` fns) + `paths.rs` (22 fns: 11 `*_dir_for_mode` + 11 non-mode) | `claude_bundle_dir`, `opencode_root`, `codex_home`, `cursor_pack_plugin_dir`, `grok_home`, `grok_bundle_dir`, `agy_bundle_dir`, `cursor_bundle_root`, `cursor_home` |
| 3 | `prepare_all` | `staging/harnesses.rs:290` | 6 hand-written blocks: mkdir + seed + attribution-off, one per harness |
| 4 | `reset_all` | `staging/harnesses.rs:337` | 8-entry path list to wipe |
| 5 | `verify_*` | `staging/harnesses.rs:164` | already split into 6 methods (Phase 2 slice ✅) — ready to move onto the trait |
| 6 | Attribution-off writers | `staging/attribution.rs` | `force_codex/cursor/grok/opencode/agy_attribution_off` (+ Claude overlay in `claude_home.rs`) |
| 7 | Seed-from-user-config | `staging/seed.rs` + `staging/constants.rs` | `seed_opencode_root`, `seed_codex_home`, `seed_grok_home`, `seed_cursor_root` (`seed.rs:144`) + **8** `*_ENTRIES`/`*_FILES`/`*_SUBDIRS` const lists (`OPENCODE_USER_ROOT_ENTRIES`, `CODEX_HOME_ENTRIES`, `GROK_HOME_ENTRIES`, `GROK_HOME_CREDENTIAL_FILES`, `CURSOR_USER_ROOT_ENTRIES`, `CURSOR_FAKE_HOME_CREDENTIAL_FILES`, `CURSOR_USER_SUBDIRS_IN_FAKE_HOME`, `CURSOR_FAKE_HOME_PACK_SUBDIRS`) |
| 8 | MCP writers | `staging/mcp.rs` | `write_claude_mcp` (JSON), opencode (`opencode.json`), `merge_into_toml_mcp_config` (codex+grok), agy (`mcp_config.json`) — 4 formats |
| 9 | `PackHarnessRoots` + `targets_and_roots` | `staging/pack_overlay.rs:35` | 7-field struct + 6-entry `(HarnessTarget, &Path)` array |
| 10 | `HookHarnessRoots` | `staging/harnesses.rs` / hooks | parallel 6-field struct |
| 11 | Hook support matrix | `hooks/capabilities.rs:24` | `support_for` — 6 arms → per-harness `*_support` fns |
| 12 | Hook asset roots | `hooks/paths.rs:15` | `base_asset_root` — per-harness path layout |
| 13 | Hook output translation | `hooks/runtime/translate.rs:5` | `to_target_output` — 6 arms |
| 14 | Hook renderers | `hooks/render/{claude,codex,cursor,opencode}.rs` | already trait-based (`HookRenderer`) — a precedent to follow |
| 15 | Launchers | `launcher/{claude,codex,cursor_agent,grok,agy,opencode}.rs` + `mod.rs` + `cli/dispatch.rs` | one `run_*` fn per harness, each: `sync_for_launch` → resolve binary → set env/args → exec |
| 16 | Verify-time roots + collision | `staging/mod.rs` `verify_staging()` (~`:117` 6-entry `(&Path,&str)` verify array; calls `collision::resolve_user_claude_bundle_collisions(6 paths)` ~`:95`) **plus** the already-extracted `staging/collision.rs` (`resolve_user_claude_bundle_collisions` `:206`, `_with_home` `:116`) which has **two more** internal parallel lists: a 6-path `harness_roots` (`:150`) and a **5-path** `md_roots` (`:181`, Codex excluded — no `commands/`/`agents/` trees) | a **third+fourth+fifth** parallel harness list, distinct from `PackHarnessRoots`/`HookHarnessRoots`; the collision pass mutates staged roots and enforces "user install wins" (Claude/Grok) |
| 17 | Guidance staging | `staging/guidance.rs` → `stage_guidance_all_harnesses` (called `harnesses.rs:147`) | prompt-level attribution-off text for OpenCode/Grok/Agy — *already* a single shared fn; stays shared |
| 18 | Yolo / approval-bypass | `launcher/common.rs:22-68` (5 `apply_yolo_*` flag injectors) + `launcher/opencode.rs:40-52` | OpenCode bypass is a **staged-file patch** (`permission:"allow"` in `opencode.json`), *not* a CLI flag — see §4 Step 7 |

Plus a per-harness `cursor`/`agy` **workspace-overlay** concern (`finalize_cursor_workspace_overlay`
in `staging/cursor.rs:52`, `finalize_agy_staging` in `staging/agy.rs:24`; manifests under
`$AGENTPACK_HOME/projects/<hash>/`) that only two harnesses use. Cleanup of stale overlays is already
**unconditional** (runs in `prepare_*_staging_without_pack_overlay` for every rebuild), so the trait's
`finalize_workspace_overlay` only needs the *create* half gated on `launch_target`.

**Verification note (re-verified against the current tree, 2026-05-30):** site 1 is precisely **5**
`match self` tables in `artifacts/harness.rs` (`raw_plugin_subdirs` 4 arms, `seed_command_frontmatter`
3 arms, `command_/skill_/agent_allowed_extra_frontmatter_keys` 3/2/2 arms) plus `rendered_artifact_kind`
(tuple `(source, self)`, 6 arms) and `disables_model_invocation_for_kind` (a `matches!`). Note these
methods are `pub(super)` today, so Step 1 must also re-home or re-export `insert_string`/`ArtifactKind`
usage. Sites 2-18 re-verified; line numbers in the table are approximate (the tree has drifted —
`cursor`/`agy`/`collision` are now their own submodules). **Test suite: 144 passing + 1 `#[ignore]`d
= 128 lib unit + 16 integration (1 ignored).** Every slice below must keep the green count at **144
passing** (the earlier draft's "145" counted the ignored test). Seeding (site 7) also includes
`seed_cursor_root` (`seed.rs:144`), already wrapped by `prepare_cursor_staging_without_pack_overlay`,
so it rides along with `prepare` for free.

**Why this hurts:** the six concerns above (path, seed, attribution, mcp, hooks, launch) are each
written *six times*, once per harness, in *different files*. The compiler does not force them to
stay in sync — `support_for` having a Grok arm doesn't guarantee `write_mcp` does. The `HookRenderer`
trait already proves the target shape works; this plan extends that pattern to the other concerns.

---

## 2. The design

A single trait capturing the per-harness contract, one implementor per harness, one registry.

```rust
// src/harness/mod.rs  (new top-level module; declared as `pub(crate) mod harness;`)

mod claude; mod cursor; mod codex; mod opencode; mod grok; mod agy;
use claude::Claude; use cursor::Cursor; use codex::Codex; use opencode::OpenCode; use grok::Grok; use agy::Agy;
// Each submodule exposes only its unit struct to this parent, e.g. `pub(super) struct Claude;`.

pub use crate::artifacts::HarnessTarget;   // the canonical id enum (already unified in Phase 1)

/// Read-only context threaded into every staging step. Borrows, no ownership.
pub struct StageCtx<'a> {
    pub project_root: &'a Path,
    pub mode: &'a EffectiveMode,
    pub lock: &'a PackLock,
    pub manifest: Option<&'a AgentpackManifest>,
    pub launch_target: Option<HarnessTarget>,  // drives workspace-overlay materialization
}

/// One coding-agent integration. Each impl owns *all* of that harness's quirks.
pub trait Harness: Sync {
    fn id(&self) -> HarnessTarget;

    // ---- staging paths (was: 9 StagingPipeline accessors + paths.rs fns) ----
    /// The directory pack content is staged into for this harness (its "root").
    fn staged_root(&self, project_root: &Path, mode: &str) -> Result<PathBuf>;
    /// Extra dirs to wipe on reset (e.g. cursor fake-home, grok bundle). Default: just staged_root.
    fn reset_paths(&self, project_root: &Path, mode: &str) -> Result<Vec<PathBuf>> { ... }

    // ---- prepare: mkdir + seed user config + force attribution off ----
    fn prepare(&self, ctx: &StageCtx) -> Result<()>;

    // ---- artifact rendering knobs (was: 5 match tables in artifacts/harness.rs) ----
    fn raw_plugin_subdirs(&self) -> &'static [&'static str];
    fn seed_command_frontmatter(&self, m: &mut Mapping, name: &str, description: &str);
    fn command_allowed_extra_frontmatter_keys(&self) -> &'static [&'static str];
    fn skill_allowed_extra_frontmatter_keys(&self) -> &'static [&'static str];
    fn agent_allowed_extra_frontmatter_keys(&self) -> &'static [&'static str];
    fn rendered_artifact_kind(&self, source: ArtifactKind) -> ArtifactKind;
    fn disables_model_invocation_for_kind(&self, kind: ArtifactKind) -> bool;

    // ---- MCP (was: 4 format writers in mcp.rs) ----
    fn write_mcp(&self, servers: &StagedMcpEntries, ctx: &StageCtx) -> Result<()>;

    // ---- hooks (was: support_for / base_asset_root / to_target_output arms) ----
    fn hook_support(&self, event: ClaudeEvent, handler: &ClaudeHandler) -> SupportLevel;
    fn hook_asset_root(&self, target_root: &Path) -> PathBuf;
    fn hook_output(&self, event: ClaudeEvent, result: &NormalizedHookResult) -> Value;
    /// `None` when this harness doesn't stage hooks (Grok/agy today).
    fn hook_renderer(&self) -> Option<Box<dyn HookRenderer>>;

    // ---- verify (was: verify_* methods — already split) ----
    fn verify(&self, ctx: &StageCtx) -> Result<()>;

    // ---- launch (was: launcher/*.rs run_* fns) ----
    /// Build the child process: resolve binary, set env (CODEX_HOME etc.), inject default args.
    fn launch_command(&self, ctx: LaunchCtx) -> anyhow::Result<Command>;

    // ---- optional workspace overlay (only cursor + agy) ----
    fn finalize_workspace_overlay(&self, _ctx: &StageCtx) -> Result<()> { Ok(()) }
}

/// Per-launch context (built in `cli/dispatch.rs`, consumed by `launch_command`).
/// NOTE: this was referenced but undefined in the first draft. `launcher::launch(...)`
/// still receives `selected_mode`, calls `sync_for_launch`, then builds this context.
/// `passthrough` is owned because launchers mutate/default args before building `Command`.
pub struct LaunchCtx<'a> {
    pub project_root: &'a Path,
    pub passthrough: Vec<String>,
    pub mode: &'a EffectiveMode,        // output of sync_for_launch(...)
    pub yolo: bool,
    pub ui: &'a Ui,
}

/// The single source of truth for "what harnesses exist".
/// Unit structs are const-constructible, so rvalue static promotion makes these `&'static`.
/// If promotion ever fails to coerce to `dyn`, fall back to explicit `static CLAUDE: Claude = Claude;`.
pub fn all() -> &'static [&'static dyn Harness] {
    &[&Claude, &Cursor, &Codex, &OpenCode, &Grok, &Agy]
}

pub fn get(id: HarnessTarget) -> &'static dyn Harness {
    all().iter().copied().find(|h| h.id() == id).expect("every HarnessTarget has an impl")
}
```

### 2a. Design corrections found during verification (must land in the trait, not discovered mid-impl)

1. **`StageCtx` carries no harness root paths — by design, but say so.** Every staging method
   (`prepare`, `write_mcp`, `verify`, hooks) derives its *own* destination by calling the existing
   `paths.rs` builders for its `mode` (e.g. `staged_root` → `staging_codex_home_dir_for_mode`). The
   shared `collect_merged_mcp(...)` runs **once** before the loop; only the per-harness *write* moves
   onto `write_mcp(&self, &merged, ctx)`. This keeps `StageCtx` a pure borrow of the 5 pipeline fields
   (which `StagingPipeline` already holds — confirmed, no new plumbing).

2. **"staged root" is not single-valued for Grok or Cursor.** Grok writes pack content to
   `grok_bundle`, MCP/attribution to `grok_home/config.toml`, and resets *both* `grok_home` +
   `grok_dir`. Cursor has the pack plugin dir **and** the fake-`HOME` (`cursor-home`). So:
   - `staged_root` = the **pack-content** root (what `targets_and_roots` already maps:
     Claude→bundle, Grok→`grok_bundle`, Cursor→`cursor_pack`). This is the one the shared pack/skill
     overlay loop uses.
   - `reset_paths` returns the **full wipe set** and most harnesses **override the default**:
     Claude→[`plugins_dir`] (parent of bundle), Grok→[`grok_home`,`grok_dir`],
     Cursor→[`cursor_bundle_root`,`cursor_home`], Agy→[`agy_dir`].
     The `default = [staged_root]` only fits OpenCode/Codex. Reword the doc-comment accordingly.
   - MCP/hook/verify destinations are computed inside each method, not assumed equal to `staged_root`.

3. **MCP is written to Cursor twice and the trait must not double-handle it.** `stage_merged_mcp`
   writes `cursor_pack/mcp.json` (generic `write_mcp_servers_json`); separately,
   `materialize_cursor_fake_home` re-merges that with the user's real `~/.cursor/mcp.json` (user wins).
   `Cursor::write_mcp` owns only the first; the fake-home merge stays inside `Cursor::prepare`/finalize.

4. **Hooks split: `hook_renderer()` is `None` for Grok/Agy, but `hook_output`/`hook_support` are NOT.**
   Staging only iterates the 4 renderers (Claude/OpenCode/Codex/Cursor — Grok/Agy disabled at
   `stage.rs:36`). But the hook **runtime** (`agentpack hook exec`) calls `to_target_output` for all
   six (Grok→`claude_fallback_output`, Agy→`codex_output`). So keep `hook_output`/`hook_support` on
   all six impls; only `hook_renderer` is optional. Also note `hook_asset_root` collapses a 2-arm match
   (OpenCode vs rest, `paths.rs:15`) into 6 one-line impls — accept the mild verbosity for uniformity.
   **Signature shift:** the free fns currently take `target: HarnessTarget` as their *first* arg
   (`support_for(target, event, handler)`, `base_asset_root(target, root)`, `to_target_output(target,
   event, result)`); on the trait that leading `target` becomes `&self`, so the trait method
   signatures shown in §2 are correct as written. The renderer dispatch lives in a 4-entry `Vec<(&dyn
   HookRenderer, &Path)>` in `stage.rs` (~`:42`); Step 6 derives that list from `hook_renderer()`
   instead. `HookRenderer` (in `hooks/render/mod.rs`) already has `fn target()` + `fn render()`.

5. **OpenCode yolo is a staged-file mutation, not arg injection** (see §4 Step 7).

6. **Top-level `harness` needs crate-visible facades into staging helpers.** Today the helpers the
   impls need are mostly inside private `staging` submodules and many functions are `pub(super)`
   (`seed_*`, `force_*_attribution_off`, `materialize_claude_settings_overlay`,
   `prepare_cursor_staging_without_pack_overlay`, `finalize_*`, MCP writers, overlay readers, etc.).
   Before moving wiring into `src/harness/*`, promote only those orchestration helpers to
   `pub(crate)` or re-export narrow `pub(crate)` facades from `staging`; keep low-level internals
   private. Also move the local private helpers from `staging/harnesses.rs`
   (`write_bundle_manifest`, Grok's simple `plugin.json` writer `write_simple_plugin_manifest`) into
   the relevant harness impls or focused staging helpers. **`keep_attribution()` is triplicated** —
   `harnesses.rs:366`, `claude_home.rs:81`, `attribution.rs:56` — collapse all three into one shared
   `pub(crate)` helper (e.g. in `staging/attribution.rs`) as part of this step so the trait impls and
   shared passes call a single source of truth.

7. **`HarnessTarget` identity helpers belong with the canonical enum.** `HarnessTarget::all()` and
   `raw_plugin_subdirs()` **already live in `artifacts/harness.rs`** (`:37` and `:48`). Only
   `as_str()` is misplaced — it is an inherent impl in `staging/mod.rs:38`. Move *just* `as_str()`
   to `artifacts/harness.rs`, and add the new `harness()` shim there, so hooks, sync fingerprints,
   launch, and staging do not depend on a staging-side impl for identity.
   Add a registry consistency test: every `HarnessTarget::all()` value resolves through `get()`,
   every `harness::all()` entry has a unique id, and the two lists contain the same ids **as a set**
   (the orders deliberately differ — see point 9 — so the test must compare sets, not sequences).

8. **Agy reset must preserve current behavior.** The current reset removes the parent
   `staging_agy_dir_for_mode(...)`, not only `agy/agentpack-bundle`. Even if the bundle is the only
   child today, `Agy::reset_paths` should override the default and return the parent dir so the
   refactor is behavior-preserving.

9. **Keep shared fan-out passes single-walk.** `stage_pack_plugins_all_harnesses` and
   `stage_pack_skills_all_harnesses` intentionally parse/walk each cache tree once and fan out to
   all target roots. The trait should derive a root view from `harness::all()`, not replace these
   with six independent per-harness walks. **Beware ordering:** `HarnessTarget::all()` is ordered
   `Claude, Cursor, Codex, OpenCode, Grok, Agy`, but `PackHarnessRoots::targets_and_roots()`
   (`pack_overlay.rs:46`) is ordered `Claude, OpenCode, Codex, Cursor, Grok, Agy` (Cursor/OpenCode
   swapped). Order does not affect correctness here (the roots are distinct dirs, no
   last-writer-wins collisions between *harnesses*), but a derived view must not assume the two
   sequences line up positionally — map by id, or keep `targets_and_roots()` as the canonical order
   for these passes.

10. **Collision shadowing is shared mutation, not per-harness verification.**
    `resolve_user_claude_bundle_collisions(...)` mutates staged roots and returns removed skill
    slugs used by later verification. Keep it as an explicit shared post-staging/pre-skill-check
    pass that derives its root list from `harness::all()`; do not bury it inside
    `Harness::verify`, whose job should stay read-only.

**Verify is two passes, not one.** `rebuild_staging_for_mode` (`staging/mod.rs:52`) calls
`pipeline.rebuild()` then the free fn `verify_staging(pipeline, lock, mode)` (`mod.rs:65`).
`verify_staging` first calls `pipeline.verify()` (the 6 `verify_*` methods → Step 2 moves these onto
the trait), **then** runs the shared collision pass (mutating) and the cross-harness skill-`SKILL.md`
existence loop over the 6-entry `harness_roots` array (→ Step 8). Step 2 only touches the per-harness
half; the collision + skill-check half stays a shared pass in `verify_staging` and is handled in Step 8.

Then every fan-out collapses. For example `prepare_all`/`reset_all`/`pipeline.verify` become:

```rust
fn rebuild(&self) -> Result<Vec<PathBuf>> {
    for h in harness::all() {
        for p in h.reset_paths(self.project_root, self.mode.name())? { remove_if_exists(p)?; }
    }
    for h in harness::all() { h.prepare(&ctx)?; }
    // shared pack/skill/hook/mcp/guidance staging (already loops over targets_and_roots) ...
    for h in harness::all() {
        if Some(h.id()) == self.target { h.finalize_workspace_overlay(&ctx)?; }
    }
    Ok(vec![harness::get(HarnessTarget::Claude).staged_root(...)?])
}

fn verify(&self) -> Result<()> {
    for h in harness::all() { h.verify(&ctx)?; }
    Ok(())
}
```

And launch dispatch (`cli/dispatch.rs`) collapses its 6 `run_*` arms to:

```rust
Commands::Claude { args } => launcher::launch(HarnessTarget::Claude, &root, args, &cli, &ui)?,
// ...where launcher::launch(id, ...) = { sync_for_launch(id); get(id).launch_command(ctx).exec() }
```

**Pattern name:** Strategy (one trait, interchangeable per-harness impls) + a small Registry
(`all()`). The existing `HookRenderer` trait is the same idea, scoped to one concern; this
generalizes it.

---

## 3. Module layout after

```
src/harness/
  mod.rs        # trait Harness, StageCtx, LaunchCtx, all(), get()   (≈120 lines)
  claude.rs     # impl Harness for Claude   — everything Claude
  cursor.rs     # impl Harness for Cursor   (incl. fake-home + workspace overlay)
  codex.rs      # impl Harness for Codex    (incl. CODEX_HOME auth bridging)
  opencode.rs   # impl Harness for OpenCode
  grok.rs       # impl Harness for Grok
  agy.rs        # impl Harness for Antigravity
```

Existing helper modules (`staging/attribution.rs`, `seed.rs`, `constants.rs`, `mcp.rs`,
`claude_home.rs`, `codex_auth.rs`, `collision.rs`, `dot_agents.rs`, `tree.rs`, `cursor.rs` +
`cursor/{approvals,fake_home,manifests,overlay}.rs`, `agy.rs` + `agy/overlay.rs`, `hooks/*`)
**stay** — they hold the actual mechanics. The trait impls *call into* them. The win is
that the dispatch/fan-out is centralized and the per-harness wiring lives in one file per harness,
not scattered. `paths.rs` keeps its functions (they're pure path builders); the trait's
`staged_root`/`reset_paths` just call the right ones so callers stop hardcoding which.

`StagingPipeline` shrinks to: hold `StageCtx`, run the `harness::all()` loops, and call the shared
cross-harness staging steps (`stage_pack_plugins_all_harnesses`, etc.) that already iterate
`targets_and_roots`.

**What deliberately stays shared (does *not* move onto the trait):** the pack/skill overlay loop,
`stage_guidance_all_harnesses` (site 17), and `resolve_user_claude_bundle_collisions` (site 16).
These are genuinely cross-harness passes that already take "all roots at once" — forcing them
per-impl would be worse. The trait owns *per-harness divergence*; these stay as loops that read each
`h.staged_root()`. `LaunchCtx` lives in `mod.rs` alongside `StageCtx`.

---

## 4. Execution order — capability-by-capability, each slice test-gated

Do **not** do this as one commit. Each step compiles, keeps the **144 passing** tests green (+1
ignored) and `cargo clippy --all-targets` clean, and commits.
Order chosen so the lowest-risk, most-mechanical concerns land first and the launch path (highest
blast radius) is last.

**The trait grows method-by-method — it does not spring fully-formed.** A `todo!()`-free skeleton
that implements the *full* trait while "wiring nothing" is impossible (every method needs a body).
So the trait starts with **only `id()`**, and **each step below adds its method(s) to the trait AND
all six impls AND rewires the old call site, in one commit.** This is what keeps every step green.

- [ ] **Step 0 — scaffold.** Create `src/harness/mod.rs` with `StageCtx`, `LaunchCtx`, the 6 unit
      structs (`pub(super) struct Claude;`…`pub(super) struct Agy;` in their submodules), `all()`,
      `get()`, and a **minimal `trait Harness`
      exposing only `fn id(&self) -> HarnessTarget`**. Add `pub(crate) mod harness;` to `lib.rs`
      (note: `artifacts::HarnessTarget` will reference `crate::harness::get`, and `harness/*` impls
      reference `crate::artifacts::HarnessTarget` — an intra-crate module cycle, which Rust permits).
      Add the `HarnessTarget::harness(self) -> &'static dyn Harness { harness::get(self) }` shim, and
      move **only** `HarnessTarget::as_str()` from `staging/mod.rs:38` to `artifacts/harness.rs`
      (`all()` and `raw_plugin_subdirs()` are already there). Add a registry consistency unit test
      (set equality, see §2a.7). Compiles, changes no behavior. *Test:* suite stays at 144 passing.

- [ ] **Step 1 — artifact tables (lowest risk).** Move the five `match self` tables from
      `artifacts/harness.rs` onto the trait impls. `HarnessTarget` keeps a thin
      `fn harness(self) -> &'static dyn Harness { harness::get(self) }` shim so existing
      `artifacts/render.rs` call sites change minimally. Delete the `impl HarnessTarget` tables.
      Use trait defaults for common behavior (`raw_plugin_subdirs = []`, common allowed
      frontmatter keys, command disables model invocation, etc.) and override only the divergent
      harnesses. *Test:* `artifacts::tests` already cover render output per target.

- [ ] **Step 2 — verify.** Move the 6 `verify_*` methods (already split) onto the trait. `verify()`
      becomes `for h in all() { h.verify(&ctx)? }`. *Test:* integration `sync_*` tests assert staging
      layout; add nothing.

- [ ] **Step 3 — paths + reset.** Add `staged_root`/`reset_paths`; rewrite `reset_all` as a loop.
      Explicit reset overrides: Claude→`plugins_dir`, Cursor→`cursor_bundle_root` +
      `cursor_home`, Grok→`grok_home` + `grok_dir`, Agy→`agy_dir`. OpenCode/Codex can use the
      default. *Test:* existing; reset is covered indirectly by every sync test (rebuild calls
      reset first).

- [ ] **Step 4 — prepare (mkdir + seed + attribution).** Add `prepare`; rewrite `prepare_all` as a
      loop. Each impl calls the existing `seed_*` / `force_*_attribution_off` helpers. *Test:*
      `sync_disables_attribution_in_all_supported_harnesses_by_default` is the guard here — it must
      stay green.

- [ ] **Step 5 — MCP.** `stage_merged_mcp` (`mcp.rs:184`) currently *both* merges and writes all
      harnesses. First **factor the merge out** into a shared `collect_merged_mcp(...) ->
      MergedEntries` (the §2a.1 "runs once" helper); then add `write_mcp(&self, &merged, ctx)` and
      have the staging loop call `collect_merged_mcp` once and `for h in all() { h.write_mcp(...) }`.
      The real merged type is `type StagedMcpEntries = MergedEntries = BTreeMap<String,
      (McpServerEntry, McpSource)>` (`mcp.rs:172/205`) — re-export it `pub(crate)`. The five native
      writers (`write_claude_mcp_servers_json`, `write_mcp_servers_json` for Cursor,
      `merge_into_opencode_config`, `merge_into_toml_mcp_config` for **both Codex and Grok**,
      `write_agy_mcp_config_json`) must be reused, not reimplemented (Codex's and Grok's `write_mcp`
      both call the shared TOML writer). Per §2a.3, `Cursor::write_mcp` writes only
      `cursor_pack/mcp.json`; the user-`~/.cursor/mcp.json` re-merge stays in
      `finalize_cursor_staging_common`. *Test:* the 11 `staging::mcp::tests` are the strongest safety
      net in the repo.

- [ ] **Step 6 — hooks.** Move `support_for` / `base_asset_root` / `to_target_output` onto the trait
      (`hook_support` / `hook_asset_root` / `hook_output`), and `hook_renderer()` returns the
      existing `HookRenderer` boxes. `stage_hooks_all_harnesses` must still collect once, derive the
      Codex seeded `hooks.json` path from `HarnessTarget::Codex.harness().staged_root(...)`, then
      render only harnesses whose `hook_renderer()` is `Some`. *Test:*
      `hooks::runtime::dispatch::tests` + `sync_stages_hooks_for_all_harnesses`.

- [ ] **Step 7 — launch (highest risk, do last).** Add `launch_command(&self, ctx: LaunchCtx)
      -> anyhow::Result<Command>` returning a **fully-configured `Command`** (binary resolved, env set via
      `cmd.env(...)`, default args injected). Replace the 6 `run_*` fns with one
      `launcher::launch(id, ctx)` = `{ let mode = sync_for_launch(id, …)?; let cmd =
      get(id).launch_command(ctx)?; common::exec_inherit(cmd) }`. Collapse the 6 `cli/dispatch.rs`
      arms. Three things the impls must absorb (verified in `launcher/`):
      - **Yolo is not uniform.** Claude/Codex/Cursor/Grok/Agy inject a *flag* (`apply_yolo_*` move
        into each impl or are called by it). **OpenCode has no flag** — it patches the staged
        `opencode.json` (`permission:"allow"`). That patch must run **before** `launch_command`
        builds the `Command` (do it in `OpenCode::launch_command` against the already-staged file,
        or as a pre-exec step inside `launch`). Do **not** model it as an arg.
      - **Cursor is the heaviest impl:** parse/inject `--workspace`, prepend `--trust` only in
        headless (`--print`/`--output-format`), set fake-`HOME` + platform Cursor dirs +
        `CURSOR_CONFIG_DIR`/`CURSOR_DATA_DIR`, and bridge `CARGO_HOME`/`RUSTUP_HOME`/`DOCKER_CONFIG`.
        All of this fits inside `launch_command` since it returns a ready `Command`.
      - **exec terminates uniformly, but the call paths differ today.** Claude and Agy build a
        `Command` and call `common::exec_inherit(cmd)` directly; Codex, Cursor, Grok, and OpenCode
        call `common::exec_with_env(exe, &[(env…)], args)` which sets env then calls `exec_inherit`
        (Unix `exec`, Windows `status`+exit). Once `launch_command` returns a fully-configured
        `Command` with env already on it, `exec_with_env` is redundant — `launch` just calls
        `exec_inherit` for all six. Delete `exec_with_env` (and its now-unused imports) in this step.
      - **`sync_for_launch` runs first, inside `launch`, not inside `launch_command`.** Current
        `run_*` take `selected_mode: Option<&str>` and call `sync_for_launch(project_root,
        selected_mode, id, ui) -> EffectiveMode` (`sync/run.rs:161`) themselves. The collapsed
        `launcher::launch(id, project_root, passthrough, selected_mode, yolo, ui)` calls
        `sync_for_launch` once, then builds `LaunchCtx { mode: &effective_mode, … }` and calls
        `get(id).launch_command(ctx)`. So `LaunchCtx.mode` is the *resolved* `EffectiveMode`, while
        the raw `selected_mode: Option<&str>` is consumed by `launch` before the ctx is built.
      *Test:* `launch_sync_state_roundtrip_under_isolated_home` + `launcher::common::tests`.
      **Manually smoke-test** `agentpack codex` + `claude` + `agent` (cursor) against real binaries
      before committing — the suite cannot exec the real CLIs.

- [ ] **Step 8 — cleanup & the remaining fan-outs.** Derive the verify-time `harness_roots` array in
      `verify_staging` (`staging/mod.rs` ~`:117`) and the `resolve_user_claude_bundle_collisions(6
      paths)` call (~`:95`) from `harness::all()`. **Don't forget collision.rs's own internal
      lists:** `resolve_user_claude_bundle_collisions_with_home` (`collision.rs:116`) hard-codes a
      6-path `harness_roots` (`:150`) **and** a separate 5-path `md_roots` (`:181`, Codex excluded
      because it has no `commands/`/`agents/` trees). If these are reworked to take a slice derived
      from the registry, preserve the Codex-excluded `md_roots` semantics (gate on a per-harness
      capability like "has command/agent trees", e.g. `raw_plugin_subdirs().contains("commands")`,
      rather than re-hardcoding the 5). Keep collision shadowing as a named shared pass because it
      mutates staged trees and returns the removed skill set used by skill verification. Delete
      `PackHarnessRoots`/`HookHarnessRoots` only if the replacement still preserves the single-walk
      pack/hook staging behavior; otherwise keep them as thin derived views. `stage_guidance_all_harnesses`
      (site 17) is already a single shared fn — leave it, just note it in the trait docs as
      "shared, not per-impl." Collapse the three `keep_attribution()` copies (§2a.6). Remove now-dead
      re-exports. Confirm `grep -rc "HarnessTarget::" src/` dropped sharply and **no `match` over all
      six harnesses remains** outside the trait impls + `id()`/`as_str()`/tests.

**Acceptance for the whole phase:** adding a hypothetical 7th harness touches exactly
`src/harness/foo.rs` (new) + one line in `all()` + one `HarnessTarget` variant + its `cli` subcommand.
Demonstrate by sketching it in the PR description, and include the registry consistency test as the
compiler-backed guard that every target has an impl.

---

## 5. Risks & mitigations

- **Launch regressions can't be unit-tested** (no real CLI in CI). → Step 7 last, isolated commit,
  manual smoke test of at least `claude` + `codex` + `agent` (cursor) before pushing.
- **`&'static dyn Harness` vs. needing per-call data.** The impls are zero-field unit structs; all
  per-invocation data flows through `StageCtx`/`LaunchCtx`. No lifetimes on the trait objects → a
  `static` registry is fine. (Trait bound `Harness: Sync` is required for the shared `&'static`
  refs and is in the §2 sketch.)
- **Visibility-promotion is the real mechanical lift, not the trait itself.** Almost every helper
  the impls call is `pub(super)` inside a private `staging`/`hooks` submodule today. Each slice must
  promote *only* the orchestration helpers it needs to `pub(crate)` (or add narrow `pub(crate)`
  facades), keeping low-level internals private. Budget most of each step's diff for this, and avoid
  a big-bang "make everything pub(crate)" commit.
- **Mixed error types on the trait are intentional.** Staging methods return `crate::error::Result`;
  `launch_command` returns `anyhow::Result<Command>` (matching today's launcher code). Rust allows
  per-method return types on one trait — keep this split rather than forcing a single error type.
- **Over-abstraction.** Six harnesses, six concerns, confirmed open-ended (agy/grok were added
  recently — the README's "Pre-release, breaking changes" note signals more churn). Rule-of-three is
  satisfied many times over; this is not premature.
- **Don't force-fit `paths.rs`.** Those are pure builders shared beyond staging (fingerprint, cli).
  Leave them; the trait *calls* them. Only the *selection* of which path moves onto the trait.
- **Big diff.** Mitigated entirely by the 9-step slicing — no step is bigger than one already-done
  Phase-4 split, and each is independently revertable.

---

## 6. Out of scope (tracked elsewhere)

- Newtypes `CacheKey`/`CommitSha`, `ModuleId` in `LockPackage` (deferred Phase 5 — high churn, low
  payoff).
- Source-carrying error variants (`Cache(String)` → `#[from]`).
- These are independent of the trait and can happen before or after.
