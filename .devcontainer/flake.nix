{
  description = "hop.top/kit — Go/TS/Python monorepo dev environment";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
        # The locked nixpkgs carries go 1.27.2; mise.toml pins 1.27.3 (a
        # security release). Same derivation, newer source; drop this
        # override once the lock moves to a nixpkgs with go_1_27 >= 1.27.3.
        go_1_27 = pkgs.go_1_27.overrideAttrs (_: rec {
          version = "1.27.3";
          src = pkgs.fetchurl {
            url = "https://go.dev/dl/go${version}.src.tar.gz";
            hash = "sha256-bndJA1zzYPBrJXXRYup535/ajvZxiJ5cN1+OUHd/Jrs=";
          };
        });
      in
      {
        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            # Go
            go_1_27 # the override above, not pkgs.go_1_27
            golangci-lint

            # Node / TypeScript
            nodejs_24
            pnpm_12

            # Python
            python314
            python314Packages.pip
            python314Packages.virtualenv
            ruff

            # Linting
            eslint

            # Task runner
            go-task

            # VCS & GitHub
            git
            gh

            # DX utilities
            jq
            curl
          ];

          shellHook = ''
            echo "hop.top/kit dev shell active"
            echo "  go:     $(go version | cut -d' ' -f3)"
            echo "  node:   $(node --version)"
            echo "  python: $(python3 --version)"
          '';
        };
      }
    );
}
