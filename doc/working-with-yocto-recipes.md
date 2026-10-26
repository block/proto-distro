# Working with Yocto Recipes

All commands below run inside the distro builder container. Enter it with:

```bash
just shell
```

## Inspect a recipe

```bash
# Show resolved variables (SRC_URI, S, WORKDIR, PV, etc.)
bitbake -e <recipe> | less

# List all tasks for a recipe
bitbake -c listtasks <recipe>

# Show which layers/files contribute to a recipe
bitbake-layers show-recipes <recipe>
bitbake-layers show-appends
```

## Edit recipe sources with devtool

`devtool` extracts the exact source used for a target into a workspace git repo so you can iterate on patches.

```bash
# Pull source into build/workspace/sources/<recipe>/
devtool modify linux-stm32mp

# Rebuild after edits
devtool build linux-stm32mp

# Generate patches back into the original recipe layer
devtool update-recipe linux-stm32mp

# Drop the workspace copy
devtool reset linux-stm32mp
```

See the [devtool reference](https://docs.yoctoproject.org/ref-manual/devtool-reference.html).

## Iterate on a single recipe

```bash
# Build one recipe
bitbake <recipe>

# Force re-run from a specific task (e.g. configure)
bitbake -c configure -f <recipe>
bitbake <recipe>

# Drop into a shell with the recipe's build env (cd $S, run make, etc.)
bitbake -c devshell <recipe>

# Clean recipe outputs (sstate kept)
bitbake -c clean <recipe>

# Nuke sstate cache for a recipe too
bitbake -c cleansstate <recipe>
```

`clean` vs `cleansstate`: `clean` wipes the work dir; `cleansstate` also invalidates the shared-state cache so the recipe rebuilds from scratch on next run.

## Find what went wrong

Logs live under the recipe's work dir:

```
build/tmp-glibc/work/<arch>/<recipe>/<version>/temp/log.do_<task>
build/tmp-glibc/work/<arch>/<recipe>/<version>/temp/run.do_<task>   # the actual script run
```

Quick jump:

```bash
bitbake -e <recipe> | grep ^WORKDIR=
```

## Layers and dependencies

```bash
# List configured layers
bitbake-layers show-layers

# Who depends on this recipe / what does it depend on
bitbake -g <recipe> && cat pn-depends.dot

# Which package provides a file
oe-pkgdata-util find-path /usr/bin/foo

# Which recipe ships a given package
oe-pkgdata-util lookup-recipe <pkg>
```

## Image debugging

```bash
# What ended up in the rootfs
ls build/tmp-glibc/deploy/images/<machine>/
cat build/tmp-glibc/work/<arch>/<image>/*/rootfs.manifest

# Rebuild the image without re-running every recipe
bitbake -c rootfs -f <image>
bitbake <image>
```

## See also

- Yocto Mega Manual: https://docs.yoctoproject.org/singleindex.html
- devtool reference: https://docs.yoctoproject.org/ref-manual/devtool-reference.html
