homepage = "https://github.com/crate-ci/typos"
description = "Source code spell checker"
binaries = ["typos"]
test = "typos --version"
source = "https://github.com/crate-ci/typos/releases/download/v${version}/typos-v${version}-${arch_}-${os_}.tar.gz"

platform "arm64" {
  vars = {
    "arch_": "aarch64",
  }
}

platform "amd64" {
  vars = {
    "arch_": "x86_64",
  }
}

platform "darwin" {
  vars = {
    "os_": "apple-darwin",
  }
}

platform "linux" {
  vars = {
    "os_": "unknown-linux-musl",
  }
}

version "1.34.0" {
  auto-version {
    github-release = "crate-ci/typos"
  }
}

sha256sums = {
  "https://github.com/crate-ci/typos/releases/download/v1.34.0/typos-v1.34.0-x86_64-apple-darwin.tar.gz": "551516c54f0606ff94b31735628eef4d42102c882bcd20c126d5506e66e24a67",
  "https://github.com/crate-ci/typos/releases/download/v1.34.0/typos-v1.34.0-aarch64-apple-darwin.tar.gz": "7df419f677dc5f3bbfb434e39632c468cdabee599b120e74205ad9a40217a75a",
  "https://github.com/crate-ci/typos/releases/download/v1.34.0/typos-v1.34.0-aarch64-unknown-linux-musl.tar.gz": "499b24962a49dc5dc1c2be71132a7f26eb2061ebb1c91de5cfb6dc417ad52a13",
  "https://github.com/crate-ci/typos/releases/download/v1.34.0/typos-v1.34.0-x86_64-unknown-linux-musl.tar.gz": "883051642fb941bb6b3769fed2971ac24df7ace6eab584d74394fa6444691013",
}
