# Releasing mobilebridge

This is the v0.1 release checklist for the public Android bridge.

## Pre-release checks

1. Confirm `main` is clean:

   ```bash
   git status --short --branch
   ```

2. Run the full verification set:

   ```bash
   go build ./...
   go vet ./...
   go test ./...
   go test ./... -race
   ```

3. Review [README.md](README.md) and [CHANGELOG.md](CHANGELOG.md) for
   accuracy against the current CLI and package surface.

4. Confirm no accidental workflow or local junk is staged:

   - `.github/`
   - local device logs
   - temporary recordings

## Tagging

Create the release tag from `main`:

```bash
git tag v0.1.0
git push origin v0.1.0
```

For patch releases:

```bash
git tag v0.1.1
git push origin v0.1.1
```

## Release notes template

Use the current changelog entry and include:

- Android-only scope
- embeddable package + CLI positioning
- key gesture and reconnect capabilities
- known limits:
  - single downstream client per page
  - no iOS support in this repo

## Post-tag sanity check

Verify:

- the tag resolves to the expected commit on GitHub
- `go install github.com/VulpineOS/mobilebridge/cmd/mobilebridge@vX.Y.Z` works
- README examples still match the released CLI flags
