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
