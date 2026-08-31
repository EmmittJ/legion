// docker-bake.hcl: builds Legion's vessel/archon images in one buildx
// session so cross-image `FROM` references (vessel-copilot builds FROM
// vessel-base) resolve to the sibling target's output instead of trying
// to pull from a registry. See images/*/Dockerfile for details on each
// image; this file only wires the shared builder session together.

group "default" {
  targets = ["vessel-base", "vessel-copilot", "archon"]
}

target "vessel-base" {
  context    = "."
  dockerfile = "images/vessel-base/Dockerfile"
  tags       = ["legion/vessel-base:latest"]
  cache-from = ["type=gha,scope=vessel-base"]
  cache-to   = ["type=gha,mode=max,scope=vessel-base"]
}

target "vessel-copilot" {
  context    = "."
  dockerfile = "images/vessel-copilot/Dockerfile"
  tags       = ["legion/vessel-copilot:latest"]
  # Wire the vessel-base target's build output in as the base image so
  # `FROM legion/vessel-base:latest` resolves within this builder session
  # instead of falling through to a registry pull.
  contexts = {
    "legion/vessel-base:latest" = "target:vessel-base"
  }
  cache-from = ["type=gha,scope=vessel-copilot"]
  cache-to   = ["type=gha,mode=max,scope=vessel-copilot"]
}

target "archon" {
  context    = "."
  dockerfile = "images/archon/Dockerfile"
  tags       = ["legion/archon:latest"]
  cache-from = ["type=gha,scope=archon"]
  cache-to   = ["type=gha,mode=max,scope=archon"]
}
