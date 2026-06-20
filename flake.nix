{
  description = "atelier — terminal-centric agentic dev framework";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in {
        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            # runtime deps (pinned via nixpkgs)
            tmux
            fzf
            git
            jq
            yq-go

            # dev toolchain
            go
            gopls
            golangci-lint
            gotools
            delve
            gnumake
            goreleaser
          ];

          shellHook = ''
            echo "atelier dev shell ready"
            echo "  go:   $(go version | cut -d' ' -f3)"
            echo "  tmux: $(tmux -V)"
          '';
        };
      });
}
