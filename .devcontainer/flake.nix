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
      in
      {
        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            # Go
            go_1_26
            golangci-lint

            # Node / TypeScript
            nodejs_22
            nodePackages.pnpm

            # Python
            python313
            python313Packages.pip
            python313Packages.virtualenv
            ruff

            # Linting
            nodePackages.eslint

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
