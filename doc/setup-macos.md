# macOS-Specific Setup

## Apple Silicon (optional)

### Enable Rosetta for x86/amd64 emulation

- Upgrade macOS to `Ventura` or later (Docker's Rosetta 2 virtualization is unavailable on Monterey).
- Upgrade Docker Desktop to 4.16.2 or later.
- [Install Rosetta 2](https://osxdaily.com/2020/12/04/how-install-rosetta-2-apple-silicon-mac/).
- Enable **Use Rosetta for x86/amd64 emulation on Apple Silicon** in Docker Desktop.
    - [How to run x86 containers with Rosetta 2](https://levelup.gitconnected.com/docker-on-apple-silicon-mac-how-to-run-x86-containers-with-rosetta-2-4a679913a0d5)
    - [Docker Mac settings](https://docs.docker.com/desktop/settings/mac/)

## Docker Volume

### Create the `proto-distro-build` volume

The Proto Distro build uses a Docker volume to persist build artifacts and the Yocto sstate cache across builds. The volume is created automatically on startup if it does not already exist. To create it manually, run:

```bash
docker volume create proto-distro-build
```

The volume's contents can be browsed in Docker Desktop under **Volumes → proto-distro-build → Data**.

To use a different volume name, change the `build_volume` field in [distro.yaml](../distro.yaml).
