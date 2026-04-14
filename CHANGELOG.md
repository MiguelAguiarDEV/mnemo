# Changelog

## [0.1.2](https://github.com/MiguelAguiarDEV/mnemo/compare/v0.1.1...v0.1.2) (2026-04-14)


### Bug Fixes

* **ci:** trigger GoReleaser inline from release-please job ([#3](https://github.com/MiguelAguiarDEV/mnemo/issues/3)) ([4f271be](https://github.com/MiguelAguiarDEV/mnemo/commit/4f271be17f3602f979aaba1fbc36fced617d8fb4))

## [0.1.1](https://github.com/MiguelAguiarDEV/mnemo/compare/v0.1.0...v0.1.1) (2026-04-14)


### Features

* async execution — quick answers + background work for complex tasks ([1a0ff60](https://github.com/MiguelAguiarDEV/mnemo/commit/1a0ff60a1836806d3239394fb3808c4e0b076d25))
* **athena:** filesystem, shell, search, and web tools — JARVIS can now act on the system ([bc62f9f](https://github.com/MiguelAguiarDEV/mnemo/commit/bc62f9f69c92d532f3dfb932546a17ebe34ced82))
* **athena:** Gateway pattern Phase 1 — Channel interface, router, WebChannel, format converters ([b4f9fb9](https://github.com/MiguelAguiarDEV/mnemo/commit/b4f9fb9ef8d4769336c4ba1071285fa9c689beb8))
* **athena:** Gateway Phase 2 — wire to HTTP routes, Discord channel stub ([df325b8](https://github.com/MiguelAguiarDEV/mnemo/commit/df325b8ee78283bb97d6c0341ea6f9b2fc44d05b))
* **athena:** Gateway Phase 3 — real Discord channel via discordgo ([0f2c82f](https://github.com/MiguelAguiarDEV/mnemo/commit/0f2c82f6d4e8e9052c6e11d58c25fbdfcb588e2c))
* **athena:** trace all tool executions — fire-and-forget to /traces/tool-call ([217c78a](https://github.com/MiguelAguiarDEV/mnemo/commit/217c78a7f8bbe71a41ba6c1d299fbbfc02a1d2a1))
* **athena:** web_search tool — DuckDuckGo HTML search, no API key needed ([a46d3d4](https://github.com/MiguelAguiarDEV/mnemo/commit/a46d3d49bba814e9b3b4556edfc1289b265159e0))
* cost dashboard — budget endpoint, projections, logging, tests ([202b4ac](https://github.com/MiguelAguiarDEV/mnemo/commit/202b4ace2facb60307d00e12c2c61c4a841464c2))
* Discord progressive message editing — live tool status + streaming ([98e8f62](https://github.com/MiguelAguiarDEV/mnemo/commit/98e8f621e2af5eaebb2f690b74fc684774547c55))
* **discord:** 9 slash commands — fast-path actions without LLM ([681fdcd](https://github.com/MiguelAguiarDEV/mnemo/commit/681fdcd1dedb535a9e3e6ed4cb7d76cad0bfc374))
* **discord:** intent router — natural language → programmatic handlers ([1794e57](https://github.com/MiguelAguiarDEV/mnemo/commit/1794e57611bc9fae532639716ca8f9b1df53b27e))
* **intent-router:** match /command typed as text + more natural variants ([38553ac](https://github.com/MiguelAguiarDEV/mnemo/commit/38553ace2cc9c30d34e36ba518b56927627dc467))
* JARVIS monorepo — initial structure ([abf9c13](https://github.com/MiguelAguiarDEV/mnemo/commit/abf9c13c703d3ba4ebde11d5100aa0cf175f014c))
* JARVIS tool execution — orchestrator parses [TOOL:name] from Claude response and dispatches locally ([ec8d031](https://github.com/MiguelAguiarDEV/mnemo/commit/ec8d031c73d1297a81ecbebef74053d383e3ea5a))
* SSE streaming bridge — live tool events to Discord ProgressReporter ([638bc9b](https://github.com/MiguelAguiarDEV/mnemo/commit/638bc9b4d5b154fe46b74530da9dc32dd8c36d9f))
* **usage:** Claude Code usage + rate limit tracking ([dccb560](https://github.com/MiguelAguiarDEV/mnemo/commit/dccb560c3f7008e308e9891a917dcead79b58533))
* **usage:** rich /usage with window accumulation + per-model breakdown ([b4186a9](https://github.com/MiguelAguiarDEV/mnemo/commit/b4186a9862801a1a0e012b4ca0fbac61b07913c4))


### Bug Fixes

* ChatQuick maxTurns 1→5 — query() counts initial message as a turn ([42868a3](https://github.com/MiguelAguiarDEV/mnemo/commit/42868a36d8731b9eb1a413882c676e53fdaf3d5e))
* chatV2 missing MaxTurns — was defaulting to 1 in bridge, causing all chat to fail ([97bd48f](https://github.com/MiguelAguiarDEV/mnemo/commit/97bd48f1a3d9cc77f605360b4ed1ba4147670db4))
* Discord formatting + delete thinking message on finalize ([0dbd23f](https://github.com/MiguelAguiarDEV/mnemo/commit/0dbd23f694e28ee35ea9349e3cc9bd7910f37f5c))
* Discord progress — new message on finalize (notifies user) + better thinking text ([e6a216c](https://github.com/MiguelAguiarDEV/mnemo/commit/e6a216c5588056fbc0c7db5bb3e941e5545888f6))
* don't send tool definitions to PROMETHEUS bridge — tools managed by orchestrator ([ce7ec2e](https://github.com/MiguelAguiarDEV/mnemo/commit/ce7ec2ed9e6a494b206e7c4f3c7da3bd6687991c))
* exclude athena/jarvis binary from repo ([d30a1de](https://github.com/MiguelAguiarDEV/mnemo/commit/d30a1de3e2220c013408051fd909f57bfd8cf37c))
* HTTP client timeout 5min→15min — autonomous tasks need more time ([d76c875](https://github.com/MiguelAguiarDEV/mnemo/commit/d76c875444eb79d7e74a7b2aad08bfe54f2ae75a))
* maxTurns 20→50 — complex tasks (debugging, multi-file edits) need more turns ([6b11fc4](https://github.com/MiguelAguiarDEV/mnemo/commit/6b11fc4a9fe784fda88a9e14025af9604022932d))
* MaxTurns 5→20 everywhere — Claude needs turns for destructive ops ([c971014](https://github.com/MiguelAguiarDEV/mnemo/commit/c9710145ce6b221790519243a8ec241e02397142))
* maxTurns→100 ([f64a3f9](https://github.com/MiguelAguiarDEV/mnemo/commit/f64a3f95c2a7d2ec5211a3817799fe187edf1ebb))
* nil pointer panic — pass no-op callback to Chat() from Gateway handler ([db0dfea](https://github.com/MiguelAguiarDEV/mnemo/commit/db0dfea3191755cb0285ec307d935214ffd5b4a5))
* remove unused combined var ([2f85f46](https://github.com/MiguelAguiarDEV/mnemo/commit/2f85f467ceeb5640c4a7176e3b220c4c802ef656))


### Performance

* **discord:** compact /usage with ANSI colors and dashboard layout ([d0d740f](https://github.com/MiguelAguiarDEV/mnemo/commit/d0d740f7afd1d0e9d1f4b6c12d2d5435670a88fd))
* **discord:** slash commands respond instantly (no defer) ([dc6eebd](https://github.com/MiguelAguiarDEV/mnemo/commit/dc6eebdcd8fa7b75ecad4c000e7c63adb61306e5))


### Refactor

* complete rename Gentleman-Programming/engram → MiguelAguiarDEV/mnemo ([e471616](https://github.com/MiguelAguiarDEV/mnemo/commit/e47161655fe5408c03375c30d8cdc5a0c489cf63))
* remove HERMES — ATHENA Gateway handles Discord directly ([266a7f6](https://github.com/MiguelAguiarDEV/mnemo/commit/266a7f66f8fb8fd10bf225979db1e41b2354b06f))
* rename engram binary to mnemo ([8a5c135](https://github.com/MiguelAguiarDEV/mnemo/commit/8a5c135615493ba8675873111942efc1b8a4ff40))
* rename engram-&gt;mnemo across plugin source ([2beb8b7](https://github.com/MiguelAguiarDEV/mnemo/commit/2beb8b73b0e7042f47e23dc51eef44baedfa43cc))
* strip mnemo to memory-only system ([300acd1](https://github.com/MiguelAguiarDEV/mnemo/commit/300acd15bfa9ce9def713bfb8f6675ceaa1c95a9))


### Documentation

* **athena:** add GoDoc comments to all undocumented exported symbols ([bec1e0b](https://github.com/MiguelAguiarDEV/mnemo/commit/bec1e0b48722eab6429e34b0caeedfd5eeab40cb))
* OpenAPI 3.0 spec + Swagger UI for all JARVIS API endpoints ([37b47f0](https://github.com/MiguelAguiarDEV/mnemo/commit/37b47f0fddc37f9c6508364675ff220358acd431))
