# Stage 1 — nix builder: build closure with all toolchains
FROM nixos/nix:2.24.14 AS builder

COPY flake.nix flake.lock ./
RUN nix build ".#devShells.$(nix eval --impure --raw --expr builtins.currentSystem).default" --no-link

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

# Toolchain install paths
ENV TOOLS_DIR=/usr/local/tools
RUN mkdir -p ${TOOLS_DIR}/go \
    ${TOOLS_DIR}/node \
    ${TOOLS_DIR}/python \
    ${TOOLS_DIR}/bin

# Copy Go toolchain + golangci-lint from nix closure
COPY --from=builder /nix/store/*-go-1.26*/bin/* \
    ${TOOLS_DIR}/go/bin/
COPY --from=builder /nix/store/*-go-1.26*/share/go \
    ${TOOLS_DIR}/go/share/go/
COPY --from=builder /nix/store/*-golangci-lint*/bin/* \
    ${TOOLS_DIR}/bin/

# Copy Node + pnpm
COPY --from=builder /nix/store/*-nodejs-22*/bin/* \
    ${TOOLS_DIR}/node/bin/
COPY --from=builder /nix/store/*-nodejs-22*/lib \
    ${TOOLS_DIR}/node/lib/
COPY --from=builder /nix/store/*-pnpm*/bin/* \
    ${TOOLS_DIR}/bin/

# Copy Python + ruff
COPY --from=builder /nix/store/*-python3-3.13*/bin/* \
    ${TOOLS_DIR}/python/bin/
COPY --from=builder /nix/store/*-python3-3.13*/lib \
    ${TOOLS_DIR}/python/lib/
COPY --from=builder /nix/store/*-ruff*/bin/* \
    ${TOOLS_DIR}/bin/

# Copy go-task, git, gh CLI
COPY --from=builder /nix/store/*-go-task*/bin/* \
    ${TOOLS_DIR}/bin/
COPY --from=builder /nix/store/*-git-minimal*/bin/* \
    ${TOOLS_DIR}/bin/
COPY --from=builder /nix/store/*-gh*/bin/* \
    ${TOOLS_DIR}/bin/

# Set up PATH for all toolchains
ENV GOROOT=${TOOLS_DIR}/go
ENV GOPATH=/home/${USERNAME}/go
ENV PATH=${TOOLS_DIR}/go/bin:${TOOLS_DIR}/node/bin:${TOOLS_DIR}/python/bin:${TOOLS_DIR}/bin:${GOPATH}/bin:${PATH}

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
