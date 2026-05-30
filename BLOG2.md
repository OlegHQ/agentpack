
# I wanted reproducible AI agent environments, so I built agentpack [AI]

## AI made custom tooling cheap again [AI]

Like almost anyone, I've been using AI agents for building commercial software—it’s incredibly useful for creating quick prototypes and iterating on products for early-stage startups. However, I must say, even before AI, building web apps was already fast thanks to libraries like React, Node.js, and frameworks like Django. For rapid iteration, the challenge was to find and put together libraries and have systems that allow you to quickly troubleshoot your apps once they are deployed to production. With AI agents, you’re not bound by the existing package ecosystem and can generate your custom libraries and development tooling that may not have existed before. Truly liberating. 

## The cost of reversing decisions went down [ AI]

Data analysis, triaging problems, even undoing decisions that recently were unfeasible to undo—like changing the entire technical stack of your project—became essentially cheap. Before writing CLIs/scripts, custom to your needs, required days of work, now they can be built in minutes, and that allows you to focus on problems that are meaningful to you.

### The new bottleneck is the harness

Of course, overly hyped AI tools aren’t perfect. These systems cannot reason and produce high-quality code—even frontier models fail at making custom abstractions and highly maintainable code unless you prompt it correctly. What makes the situation worse is the amount of AI coding harnesses that implement essentially the same agent loop: Claude Code, OpenCode, Codex, Copilot, Cursor, each of them either restricts to its models. To make matters worse, they manage context differently that often times results in different coding performance for the same models. Configuration is still not standardized: skills are more or less standard, but hooks or custom rules are implemented in their own way per harness. CLIs like Claude Code or Cursor also have plugin systems, but they are not fully interchangeable. When working with teams on startups, I often see a repository fully optimized for Claude Code, while using it with Codex requires hacking with symlinks or committing my own harness-specific configs to the team’s repository.

### Why I ended up using several agents

To this day, I’m using Claude Code as my daily driver. I know the flaws of the Opus model—it makes average code, may miss important things, gaslight itself, ultimately make wrong decisions, which makes it infeasible to run on autopilot. Nevertheless, it’s a good enough model for me. For speed, I use Cursor Agent with Composer Model with `agent` CLI. It gets the job done for doing quick refactors, solving merge conflicts, and other light work. At the same time, I would never trust even the latest Composer to build anything serious; it’s simply not there. Anything complex I throw at Codex (GPT 5.5) with Playwright MCP connected, magically it can figure out even the most complex issues. Gemini 3.5 Flash recently came with Antigravity CLI; the harness is very rough and lacking, but the model seems very smart of a small set of non-trivial issues to solve. So I end up with a problem of having 3-4 CLI agents working on the same codebase, and I would like to share a set of skills, slash commands across all of them. Harnesses like Claude Code have their own Marketplace with toggleable plugins, but those are specific to Claude and don’t work well with immutable configs like Nix Home-Manager, so the overall agentic AI tooling feels brittle. 

### Why this problem is worse in the CLI [AI]

I’m not mentioning GUI apps like Codex App or Cursor: while UX for a high-fidelity UI application is way nicer, you trade off the flexibility of quickly running agents on a virtual machine from a Cloud data center or connecting to your session from a phone terminal like Termius or Blink without friction.  Launching GUI apps on macOS with custom environment variables is also not as simple as doing so for TUI apps. 

Some teams implement complex tooling around work trees that allows launching copies of the same apps for full end-to-end testing, but even in May 2026, I still would not trust AI with QA: my typical workflow still ends with testing each PR before the merge. 

## agentpack: a staging layer for agent configs

### The basic idea

[insert ASCII video doing it]

To keep myself sane with complicated agent configs and new agentic harnesses that are constantly coming out, I’ve built a prototype package manager/virtual environment tool that allows me to quickly compose a new configuration from any skill I find on GitHub or quickly create a new plugin that contains a domain-specific set of skills that could be turned on or off on demand. 

I know there are quite a bunch of package managers for agents, but for me they lack 2 things: simplicity and isolation. Some package managers are Claude Code specific and rely on marketplace registries, others are doing symlinking into the project directory. Honestly, I find a folder with markdown files being called a package to be an overstatement. While I’m aware of GitHub’s problem with uptime, I still find loading those skill files from GitHub simple and easy (similar to how we add packages in Go):

[insert example of inserting Go-like package]

### GitHub as the registry

Other times, I would not be bothered with checking README.md but can navigate some directory of skills. So you can just copy a GitHub URL with a skill and the CLI will load it properly into the staging directory. Simple:

[Simple example of loading the config]

### Design constraints

A design decision that was critical for me: portability, reusability of configuration. The `agentpack` idea is to never copy assets to the project directory or the global user configuration in folders like `~/.cursor` or `~/.claude`. 

The solution was to create an ephemeral staging directory and pass it to launch arguments of agentic harness or use environment variables to do so. Unfortunately, this has been very challenging to fully support. I had to use agents to reverse engineer bundled JavaScript code to understand how to get around limitations of certain harnesses. E.g. rules are only supported by cursor, so I had to generate a fallback skill that would load into harnesses like Codex or Claude Code.  Cursor CLI has undocumented bugs that were possible to find out while reversing what it is doing: for some reason, it only loads sub-agents from the project-local `.cursor/agents` directory and ignores the global `$HOME/.cursor/agents` dir (that may be already fixed, hopefully). So in the current state of things, I’ve made a best-possible approximation of the ideal “package-manager” for agent CLIs with the hope that things will get either standardized or at least major CLIs agree on additive loading of additional configs. 

#### A shared manifest file and lockfile

To keep track of the loaded projects, I store two files that are typically committed to the source control:

* agentpack.toml — this lists dependencies, configuration, and modes (I’ll come back to this later)
* pack.lock — writes down commit hashes of all loaded repositories with the appropriate dependencies.

#### Workspaces for client repositories

On my consulting work, of course, I don’t want to commit some obscure manifest files of some unknown tool to teams’ repositories, so I create a parent `<project-name>-workspace/<project-repo>` directory structure, where I init `agentpack` config in the workspace directory. Running `agentpack sync` will reload all the dependencies and fetch the necessary repositories from GitHub. Fetched repositories are changed in the user-wide home directories and pulled from there on repeated reads of `owner/repo/path/commit` to eliminate the need to fetch the same repository multiple times.

### Modes: switching the agent’s working context

Having a fully dynamic configurator allows me to define modes (it’s a list of toggles whether certain skills are available in a certain mode). In a full-stack Python & React.js monorepo, I want to toggle Python-specific skills off for a front-end heavy refactoring job and maybe add additional granular frontend-specific rules and guidance for that. With a dedicated TUI and pre-configured modes in `agentpack.toml` manifest, it becomes easy to do.

#### Why modes matter in real projects

On one project that requires doing heavy math calculations and frontend UX work in the same repository and also database exploration, this was very useful to have. 

### Hooks as an intermediate representation

#### Choosing the canonical hook model

For hooks emulation, I chose to base the configuration on Claude Code’s lifecycle events (PreToolUse, PostToolUse, UserPromptSubmit, Stop, etc.) and tool matches (for Edit and Grep). Other CLIs support fewer events and handler types. 

Claude’s model is treated as the canonical intermediate representation that gets mapped to each agent harness format. Features that are not natively supported by the target CLI get emulated through subcommand `agentpack hook-exec`.

This gives a lot of advantages when running setups where you redirect agents from using standard tools to a custom MCP server (e.g. jCodeMunch MCP for better retrieval). 

[show diagram]

### Compatibility matrix

For this date, I created the following compatibility matrix. Harnesses like Codex, OpenCode, and Claude Code were the most important for me, so I focused more on them. 

[compat matrix table here]

### Try it

```

    agentpack init                      # stub agentpack.toml + v2 pack.lock
    agentpack add github.com/anthropics/skills/skills/canvas-design
    agentpack claude 
```

`agentpack` is a quick, vibe-coded prototype and goes more as a pitch of an idea. I don’t think we need complex package managers with dedicated registries for downloading a couple of Markdown files from public sources. I built it for my needs: it allows me to reach my personally commonly used skills or MCPs without polluting the original repo and keeping agent configurations organized. I’ve been using it for two months, and I thought that it would be a good idea to contribute to the community of anyone who uses agents heavily in their work.


 

