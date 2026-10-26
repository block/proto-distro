# Overview

This directory contains various packagegroups for the miner firmware. Each packagegroup is designed to include a specific set of packages required for different functionalities.

# Packagegroup Descriptions

## Feature packages

### packagegroup-proto-base
Base packages, installed on all images to enable basic functionality (serial console, ssh). When this package is included, it enables basic system communication.

### packagegroup-proto-essential
Essential packages, installed on all images (release, mfg). When this package is included, it enables all essential system setup and configuration recipes (i.e. miner-firstboot, network time sync, hostname configuration, etc.). All images should include this.

### packagegroup-proto-memfault
Packages to enable memfault features/functionality.

## Miner-Image specific packages

### packagegroup-proto-mfg
Packages used in the manufacturing image (miner-image-mfg). The unused legacy
`miner-health` utility is no longer included. Application health remains owned
by application packages; OTA update-confirmation and rollback policy are unchanged.

### packagegroup-proto-release
Packages used in the release/customer facing image (miner-image-release)
