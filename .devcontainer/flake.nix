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

        tools = with pkgs; [
          # Go
          go_1_27 # the override above, not pkgs.go_1_27
          golangci-lint

          # Node, pnpm and Python come from their upstream releases
          # (Dockerfile) or mise, at the exact mise.toml pins: nixpkgs lags
          # security releases. eslint runs from each package's lockfile
          # (`pnpm eslint`), on that Node.
          ruff

          # Task runner
          go-task

          # VCS & GitHub
          gitMinimal # full git pulls python and perl into the closure
          gh

          # DX utilities
          jq
          curl
        ];
      in
      {
        # One profile of every tool: the Dockerfile installs it with its
        # whole closure, so the binaries keep their Nix store interpreter.
        packages.default = pkgs.buildEnv {
          name = "kit-dev-tools";
          paths = tools;
        };

        devShells.default = pkgs.mkShell {
          buildInputs = tools;

          shellHook = ''
            echo "hop.top/kit dev shell active"
            echo "  go:     $(go version | cut -d' ' -f3)"
          '';
        };
      }
    );
}
