---
name: github-actions
description: >
  GitHub Actions workflow design and security best practices for Legion. Apply when writing
  or reviewing .github/workflows/*.yml files. Covers: Go build/test workflows, Docker
  image build and push, secret handling, action pinning, caching, and least-privilege
  permissions. Activate for: creating CI workflows, reviewing workflow files, debugging
  failing Actions runs, or adding new workflow triggers.
license: MIT
metadata:
  version: "1.0"
  project: legion
---

## Legion CI Targets

Three binaries to build and test, one Docker image to build per vessel:

| Job | Command | Trigger |
|---|---|---|
| Build + vet | `go build ./...` + `go vet ./...` | push, PR |
| Test | `go test ./...` | push, PR |
| Build archon image | `docker build -f cmd/archon/Dockerfile` | push to main |
| Build vessel-copilot | `docker build -f Dockerfile.vessel-copilot` | push to main |

## Starter Workflow: CI

```yaml
name: ci
on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

permissions:
  contents: read          # minimum required; add only what jobs need

jobs:
  build-test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4          # pin to SHA in production

      - uses: actions/setup-go@v5
        with:
          go-version: "1.21"
          cache: true                      # caches module download

      - name: Build all binaries
        run: go build ./...

      - name: Vet
        run: go vet ./...

      - name: Test
        run: go test -race -timeout 120s ./...
```

## Secret Handling

```yaml
# GOOD — from encrypted secrets, injected at runtime
env:
  GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}    # auto-provided by Actions
  DOLT_DSN: ${{ secrets.DOLT_DSN }}

# NEVER — hardcoded in workflow YAML
env:
  GITHUB_TOKEN: "ghp_abc123..."               # ← exposed in repo history
```

Rules:
- Store secrets in **repository secrets** (Settings → Secrets → Actions)
- Use **environment secrets** + manual approval for production deployments
- Never `echo $SECRET` — GitHub redacts known secret values, but not derived ones
- Rotate secrets every 90 days; remove obsolete ones immediately

## Action Pinning

```yaml
# GOOD — pinned to full commit SHA (immune to tag mutation attacks)
- uses: actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683  # v4.2.2

# ACCEPTABLE for trusted GitHub-maintained actions
- uses: actions/setup-go@v5

# AVOID for third-party actions
- uses: some-org/some-action@latest             # ← tag can be repointed
```

## Caching

```yaml
# Go module + build cache (handled by setup-go cache: true)
- uses: actions/setup-go@v5
  with:
    go-version: "1.21"
    cache: true

# Docker layer cache with buildx
- uses: docker/build-push-action@v6
  with:
    cache-from: type=gha
    cache-to: type=gha,mode=max
```

## Docker Build + Push Workflow

```yaml
  build-image:
    runs-on: ubuntu-latest
    needs: build-test
    if: github.ref == 'refs/heads/main'
    permissions:
      contents: read
      packages: write               # needed for GHCR push
    steps:
      - uses: actions/checkout@v4

      - uses: docker/setup-buildx-action@v3

      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - uses: docker/build-push-action@v6
        with:
          context: .
          file: cmd/archon/Dockerfile
          push: true
          tags: ghcr.io/${{ github.repository }}/archon:latest
          cache-from: type=gha
          cache-to: type=gha,mode=max
```

## Permissions: Least Privilege

```yaml
# Set at workflow level — applied to all jobs unless overridden
permissions:
  contents: read

# Override at job level for elevated needs
jobs:
  publish:
    permissions:
      contents: read
      packages: write
```

Never use `permissions: write-all`. Grant only what each job needs.

## Security Rules

- [ ] `pull_request_target` trigger avoided — exposes secrets to fork code
- [ ] Secrets never echoed to logs
- [ ] Third-party actions pinned to commit SHA
- [ ] `permissions: contents: read` at workflow level minimum
- [ ] Dependabot enabled for `github-actions` ecosystem

See [references/WORKFLOW-PATTERNS.md](references/WORKFLOW-PATTERNS.md) for complete workflow templates.
