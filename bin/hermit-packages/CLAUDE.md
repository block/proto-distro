# AGENTS.md

This file provides guidance to AI coding assistants when working with Hermit packages in this repository.

## Hermit Package Management Overview

This directory contains custom Hermit package definitions for tools not available in the official Hermit registry. Hermit is a tool manager that provides consistent, reproducible tool versions across development environments.

## Creating New Hermit Packages

Follow these steps to create a new Hermit package for tools that publish binaries to GitHub releases:

### 1. Create the Package HCL File

Create a new `.hcl` file named after the tool (e.g., `mytool.hcl`) with this structure:

```hcl
homepage = "https://github.com/owner/repo"
description = "Brief description of the tool"
binaries = ["binary-name"]  # List of binaries in the archive
test = "binary-name --version"  # Command to test installation
strip = 1  # Number of directory levels to strip from archive (optional)

# Define platform-specific download URLs
platform "darwin" "amd64" {
  source = "https://github.com/owner/repo/releases/download/v${version}/tool-macos-x64.tar.gz"
}

platform "darwin" "arm64" {
  source = "https://github.com/owner/repo/releases/download/v${version}/tool-macos-arm64.tar.gz"
}

platform "linux" "amd64" {
  source = "https://github.com/owner/repo/releases/download/v${version}/tool-linux-x64.tar.gz"
}

platform "linux" "arm64" {
  source = "https://github.com/owner/repo/releases/download/v${version}/tool-linux-arm64.tar.gz"
}

# Version with auto-update from GitHub releases
version "X.Y.Z" {
  auto-version {
    github-release = "owner/repo"
  }
}

# IMPORTANT: Leave sha256sums empty - they will be added automatically
sha256sums = {}
```

### 2. Important Notes on sha256sums

**CRITICAL**: When initially creating the package file:
- The `sha256sums` field must be present but **empty**: `sha256sums = {}`
- Do **NOT** add comments inside the braces
- Do **NOT** manually add any checksums
- The checksums will be added automatically in the next step

### 3. Add SHA256 Checksums

Run this command to automatically download the binaries and add their checksums:

```bash
hermit manifest add-digests bin/hermit-packages/mytool.hcl
```

This will:
- Download each platform-specific binary
- Calculate SHA256 checksums
- Update the HCL file with the correct checksums

### 4. Validate the Package

From the **project root** directory, validate the manifest:

```bash
hermit manifest validate
```

This ensures:
- Package syntax is correct
- All required fields are present
- URLs are properly formatted
- Checksums match the expected format

### 5. Test Installation

Install the package to verify it works:

```bash
# From project root
source bin/activate-hermit
hermit install mytool@X.Y.Z

# Test the tool
mytool --version
```

## Hermit Built-in Variables

Hermit provides several built-in variables that automatically map to platform values:

### Architecture Variables

| Variable | darwin/amd64 | darwin/arm64 | linux/amd64 | linux/arm64 |
|----------|-------------|--------------|-------------|-------------|
| `${arch}` | `amd64` | `arm64` | `amd64` | `arm64` |
| `${xarch}` | `x86_64` | `aarch64` | `x86_64` | `aarch64` |

### Operating System Variables

| Variable | darwin | linux |
|----------|--------|-------|
| `${os}` | `darwin` | `linux` |
| `${xos}` | `darwin` | `linux` |

### Usage Examples

```hcl
# Using built-in xarch (extended architecture names)
source = "https://github.com/owner/repo/releases/download/v${version}/tool-${xarch}-${os}.tar.gz"
# Results in: tool-x86_64-darwin.tar.gz, tool-aarch64-linux.tar.gz, etc.

# Using built-in arch (standard names)
source = "https://github.com/owner/repo/releases/download/v${version}/tool-${os}-${arch}.tar.gz"
# Results in: tool-darwin-amd64.tar.gz, tool-linux-arm64.tar.gz, etc.
```

## Common Patterns for GitHub Releases

### Pattern 1: Simple platform-specific archives with built-ins

```hcl
# When release uses x86_64/aarch64 naming
source = "https://github.com/owner/repo/releases/download/v${version}/tool-${xarch}-${os}.tar.gz"
# No custom vars needed!
```

### Pattern 2: Custom variables for non-standard naming

```hcl
# When release naming doesn't match Hermit's built-ins
platform "arm64" {
  vars = { "arch_": "aarch64" }
}
platform "amd64" {
  vars = { "arch_": "x86_64" }
}
platform "darwin" {
  vars = { "os_": "apple-darwin" }
}
platform "linux" {
  vars = { "os_": "unknown-linux-musl" }
}

source = "https://github.com/owner/repo/releases/download/v${version}/tool-v${version}-${arch_}-${os_}.tar.gz"
```

### Pattern 3: Overriding for specific platforms

```hcl
# Use built-ins as default
source = "https://github.com/owner/repo/releases/download/v${version}/tool-${xarch}-${os}.tar.gz"

# Override for specific platform with different naming
platform "darwin" {
  source = "https://github.com/owner/repo/releases/download/v${version}/tool-macos-universal.tar.gz"
}
```

### Pattern 4: Mixed built-ins and custom vars

```hcl
# Define custom var for OS naming, use built-in for arch
platform "darwin" {
  vars = { "os_name": "macos" }
}
platform "linux" {
  vars = { "os_name": "linux" }
}

source = "https://github.com/owner/repo/releases/download/v${version}/tool-${os_name}-${xarch}.tar.gz"
# Results in: tool-macos-x86_64.tar.gz, tool-linux-aarch64.tar.gz, etc.
```

## Package Fields Reference

### Required Fields
- `homepage`: Project homepage URL
- `description`: Brief description of the tool
- `binaries`: Array of binary names in the archive
- `source` or platform-specific `source`: Download URL template
- `version`: Version block with version number
- `sha256sums`: Checksum mapping (empty initially, filled by add-digests)

### Optional Fields
- `test`: Command to verify installation (recommended)
- `strip`: Number of directory levels to strip from archive
- `env`: Environment variables to set
- `auto-version`: Auto-update configuration for GitHub releases
- `vars`: Platform-specific variable substitutions

## Troubleshooting

### Common Issues

1. **"Missing checksums" error**
   - Ensure you ran `hermit manifest add-digests`
   - Check that all platform URLs are accessible

2. **"Binary not found" after installation**
   - Verify the `binaries` array matches actual binary names
   - Check if `strip` level is correct for the archive structure

3. **"404 Not Found" during add-digests**
   - Verify the GitHub release exists
   - Check URL pattern matches actual release assets
   - Ensure version number is correct

4. **Platform detection issues**
   - Use `hermit info` to see detected platform
   - Ensure platform blocks match: "darwin"/"linux" and "amd64"/"arm64"

## Best Practices

1. Always use `${version}` variable in source URLs for maintainability
2. Include a `test` command to verify successful installation
3. Use `auto-version` with `github-release` for automatic updates
4. Keep descriptions concise but informative
5. Test on all supported platforms when possible
6. Use meaningful variable names when using `vars`

## Examples in This Repository

See these existing packages for reference:
- `probe-rs.hcl`: Multi-binary tool with platform-specific naming
- `typos.hcl`: Simple tool with variable-based platform detection
- `clang.hcl`: Complex package with development headers
- `uv.hcl`: Package with many versions and auto-update

## Integration with CI

Hermit packages are automatically validated in CI through GitHub Actions. Ensure your package passes local validation before pushing.
