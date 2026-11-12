# syntax=docker/dockerfile:1
FROM ubuntu:22.04

LABEL org.opencontainers.image.authors="btcm-sw-team@squareup.com"
LABEL org.opencontainers.image.description="Image for building BTCM Linux distro using Yocto project"
LABEL org.opencontainers.image.licenses=Apache-2.0

ARG DEBIAN_FRONTEND=noninteractive

ARG UID=1001
ARG GID=1000

ENV BB_ENV_PASSTHROUGH_ADDITIONS="DISTRO_VERSION GIT_HASH BUILD_DATETIME MEMFAULT_PROJECT_KEY TARGET"

SHELL ["/bin/bash", "-c"]

# Install package dependencies
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
    bsdmainutils \
    build-essential \
    chrpath \
    coreutils \
    cpio \
    curl \
    debianutils \
    device-tree-compiler \
    diffstat \
    file \
    fluxbox \
    gawk \
    gcc \
    git \
    iproute2 \
    iputils-ping \
    libegl1-mesa \
    libgmp-dev \
    liblz4-tool \
    libmpc-dev \
    libsdl1.2-dev \
    libssl-dev \
    locales \
    mesa-common-dev \
    nano \
    openssl \
    parted \
    quilt \
    rsync \
    screen \
    socat \
    sudo \
    texinfo \
    tmux \
    tree \
    unzip \
    zip \
    vim \
    wget \
    xterm \
    xz-utils \
    zstd && \
    /usr/sbin/locale-gen en_US.UTF-8 && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*

# Install ssh server
RUN apt-get update && \
    apt-get install -y \
    openssh-server \
    openssh-client \
    systemctl

# Install quality of life tools when working in container
RUN apt-get update && \
    apt-get install -y \
    nnn \
    ripgrep \
    fd-find

# Create btcm user
RUN groupadd -g $GID btcm && \
    useradd -g $GID -m -s /bin/bash -u $UID btcm

# Add btcm to sudoers list, with no password
RUN ls /etc/sudoers.d/ && \
    echo "btcm ALL=(ALL) NOPASSWD: ALL" > /etc/sudoers.d/010_btcm-nopasswd
# Set btcm password
RUN echo 'btcm:password' | chpasswd

EXPOSE 22
CMD ["/usr/sbin/sshd","-D"]

# Install Python 3.12 from deadsnakes PPA
RUN apt-get update && \
    apt-get install -y software-properties-common && \
    add-apt-repository -y ppa:deadsnakes/ppa && \
    apt-get update && \
    apt-get install -y python3.12 python3.12-venv python3.12-dev && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

# Set python3.12 as default python3
RUN update-alternatives --install /usr/bin/python3 python3 /usr/bin/python3.12 1 && \
    update-alternatives --install /usr/bin/python python /usr/bin/python3.12 1

COPY --from=ghcr.io/astral-sh/uv:0.12.17 /uv /uvx /usr/local/bin/

# setuptools is required because Python 3.12 removed the distutils module from
# the standard library. Yocto/BitBake's sanity check imports distutils.sysconfig,
# and setuptools (>=60) provides a vendored copy of distutils.
RUN uv pip install --system --python /usr/bin/python3.12 --no-cache \
    'setuptools>=60'

# Keep installed security tools outside the bind-mounted checkout.
RUN --mount=type=bind,source=python,target=/tmp/security-source <<'SH'
set -eu
python3.12 - <<'PY'
import shutil
shutil.copytree(
    '/tmp/security-source', '/tmp/security-build',
    ignore=shutil.ignore_patterns(
        '.venv', 'build', 'dist', '*.egg-info', '__pycache__',
        '.pytest_cache', '.mypy_cache', '.ruff_cache', '.tox',
        'tests', 'test_artifacts', 'default_ta.pem',
    ),
)
PY
UV_PROJECT_ENVIRONMENT=/opt/proto-distro-security-tools \
    uv sync --project /tmp/security-build --locked --no-dev --no-editable \
    --python /usr/bin/python3.12 --no-python-downloads --no-cache
rm -rf /tmp/security-build
ln -s /opt/proto-distro-security-tools/bin/fsbl_signer /usr/local/bin/fsbl_signer
SH

USER btcm
WORKDIR /home/btcm/proto-distro

ENV PATH="/home/btcm/.local/bin:${PATH}"

# Copy entrypoint script (outside WORKDIR so volume mounts don't shadow it)
COPY --chmod=777 --chown=btcm docker/entrypoint.sh /home/btcm/entrypoint.sh

# Set up git config
RUN git config --global user.name "BTCM SW Team"
RUN git config --global user.email "btcm-sw-team@squareup.com"

ENTRYPOINT ["/home/btcm/entrypoint.sh"]
