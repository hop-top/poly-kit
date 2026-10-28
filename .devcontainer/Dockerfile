# Stage 1 — nix builder: build closure with all toolchains
FROM nixos/nix:2.24.14 AS builder

# Image ships without flakes; enable them for this stage.
ENV NIX_CONFIG="experimental-features = nix-command flakes"

# Own dir: a flake at / makes nix copy the whole filesystem into the store.
WORKDIR /build
COPY flake.nix flake.lock ./
# Profile of every flake tool, plus its full store closure: Nix binaries
# load their libraries (and ELF interpreter) from /nix/store, so copying
# bin/ dirs alone leaves them unrunnable.
RUN mkdir -p /out /closure \
    && nix build .#default --out-link /out/tools \
    && nix-store -qR /out/tools > closure.txt \
    && xargs cp -a -t /closure/ < closure.txt

# Stage 2 — runtime: slim debian with copied toolchains
FROM debian:bookworm-slim

ARG USERNAME=dev
ARG USER_UID=1000
ARG USER_GID=${USER_UID}

# Base packages + DX niceties
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    less \
    htop \
    jq \
    fzf \
    ripgrep \
    zsh \
    sudo \
    locales \
    libstdc++6 \
    && rm -rf /var/lib/apt/lists/* \
    && sed -i 's/^# *en_US.UTF-8/en_US.UTF-8/' \
       /etc/locale.gen \
    && locale-gen

ENV LANG=en_US.UTF-8
ENV LANGUAGE=en_US:en
ENV LC_ALL=en_US.UTF-8

# Create non-root user with sudo
RUN groupadd --gid ${USER_GID} ${USERNAME} \
    && useradd --uid ${USER_UID} --gid ${USER_GID} \
       -m -s /usr/bin/zsh ${USERNAME} \
    && echo "${USERNAME} ALL=(ALL) NOPASSWD:ALL" \
       >> /etc/sudoers.d/${USERNAME} \
    && chmod 0440 /etc/sudoers.d/${USERNAME}

# Toolchains from the nix builder: closure at its store path, profile
# symlink at /usr/local/tools
COPY --from=builder /closure /nix/store
COPY --from=builder /out/ /usr/local/

ENV TOOLS_DIR=/usr/local/tools
ENV GOROOT=${TOOLS_DIR}/share/go
ENV GOPATH=/home/${USERNAME}/go
ENV PATH=${TOOLS_DIR}/bin:${GOPATH}/bin:${PATH}

# Node and pnpm at the exact mise.toml pins (check-toolchain-parity holds
# NODE_VERSION / PNPM_VERSION to them), from upstream release archives
# checked against pinned per-arch sha256s. Bumping a version means
# replacing its hashes: Node's come from the release's SHASUMS256.txt
# (verify its .sig with the nodejs/release-keys keyring), pnpm's from the
# GitHub release asset digests.
ARG NODE_VERSION=24.22.1
ARG NODE_SHA256_ARM64=33c37be234ba8bee041961047b19160f4d7d0f164e8e246f78bdd10feff148b3
ARG NODE_SHA256_X64=eb6728e77c576a35162c122c45ccadf224d89ff00abc8dae46cc2897c6002d7c
ARG PNPM_VERSION=12.16.0
ARG PNPM_SHA256_ARM64=be414eee035c3cc4f0a8fe20cbfc44b7fae9e4545aec6f45565a24789aebca93
ARG PNPM_SHA256_X64=4492070f381f5cc42b977b968d8d72b970ffd17bef2523c229170b2cd4ece44a
RUN set -eu; \
    case "$(dpkg --print-architecture)" in \
      arm64) arch=arm64; node_sum=${NODE_SHA256_ARM64}; pnpm_sum=${PNPM_SHA256_ARM64} ;; \
      amd64) arch=x64; node_sum=${NODE_SHA256_X64}; pnpm_sum=${PNPM_SHA256_X64} ;; \
      *) echo "unsupported arch: $(dpkg --print-architecture)" >&2; exit 1 ;; \
    esac; \
    curl -fsSLo /tmp/node.tar.gz \
      "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${arch}.tar.gz"; \
    printf "%s  /tmp/node.tar.gz\n" "${node_sum}" > /tmp/node.sha256; \
    sha256sum -c /tmp/node.sha256; \
    tar -xzf /tmp/node.tar.gz -C /usr/local --strip-components=1 \
      --no-same-owner --exclude=CHANGELOG.md --exclude=README.md --exclude=LICENSE; \
    curl -fsSLo /tmp/pnpm.tar.gz \
      "https://github.com/pnpm/pnpm/releases/download/v${PNPM_VERSION}/pnpm-linux-${arch}.tar.gz"; \
    printf "%s  /tmp/pnpm.tar.gz\n" "${pnpm_sum}" > /tmp/pnpm.sha256; \
    sha256sum -c /tmp/pnpm.sha256; \
    mkdir -p /usr/local/lib/pnpm; \
    tar -xzf /tmp/pnpm.tar.gz -C /usr/local/lib/pnpm --no-same-owner; \
    ln -s /usr/local/lib/pnpm/pnpm /usr/local/bin/pnpm; \
    rm /tmp/node.tar.gz /tmp/node.sha256 /tmp/pnpm.tar.gz /tmp/pnpm.sha256

# Python at the exact mise.toml pin (check-toolchain-parity holds this and
# the uv tag to it): uv's python-build-standalone build, installed for all
# users as the default python3/python in /usr/local/bin. Venvs come from
# `python3 -m venv` with the bundled pip.
ARG PYTHON_VERSION=3.14.10
COPY --from=ghcr.io/astral-sh/uv:0.13.6@sha256:af22a52f5eafef3d78e551599d405c02df61919d18028f0cea8eda93bc9986d0 \
    /uv /uvx /usr/local/bin/
# Install dirs scoped to this RUN: the dev user's own `uv python install`
# keeps uv's per-user defaults.
RUN UV_PYTHON_INSTALL_DIR=/usr/local/share/uv/python \
    UV_PYTHON_BIN_DIR=/usr/local/bin \
    uv python install --preview-features python-install-default \
       --default ${PYTHON_VERSION} \
    && uv cache clean

# Install starship prompt
RUN curl -fsSL \
    https://starship.rs/install.sh \
    | sh -s -- --yes

# Configure zsh + starship for dev user
RUN echo 'eval "$(starship init zsh)"' \
    >> /home/${USERNAME}/.zshrc \
    && chown ${USERNAME}:${USERNAME} \
       /home/${USERNAME}/.zshrc

WORKDIR /workspace

USER ${USERNAME}

CMD ["zsh"]
