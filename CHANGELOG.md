# Changelog

## v1.0.0

Initial release.

### Features

- **`/steer` skill**: Generate Claude Code hooks from natural language rules with automatic self-testing
- **Rule syntax**: `detect | suggest` — left side defines what to block, right side provides developer guidance
- **Hard + soft steering**: Hook-level enforcement (hard) paired with `CLAUDE.md` rule persistence (soft)
- **Steer CLI**: Full hook lifecycle management — `register`, `remove`, `list`, `test`, `verify`
- **Factory preset pack**: 12 security hooks shipped as built-in presets (`steer preset enable-bundle security`)
- **Self-protection hook**: Factory hooks guard their own persistence — agent can't disable or overwrite them
- **Git enforcement**: `steer verify --staged` for pre-commit checks, `steer git-hooks install` for automation
- **CI support**: `steer verify --changed --base <ref> --head <ref>` for pull request gates
- **Upsert-by-default registration**: `steer register` always succeeds — no interactive prompts, safe for agent workflows
- **Cross-platform**: Prebuilt binaries for macOS (amd64/arm64), Linux (amd64/arm64), Windows (amd64)
- **Standalone installer**: `install-prebuilt.sh` for CLI use outside the plugin system
