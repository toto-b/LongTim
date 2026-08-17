{
  description = "Longwave Online — DevShell mit Go-, Container- und Kubernetes-Toolchain";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs {
          inherit system;
          config.allowUnfree = true;
        };
      in
      {
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            claude-code

            # Backend
            go
            gopls
            go-tools

            # Container & Cluster
            docker-client
            kind
            kubectl
            kubernetes-helm
            kustomize

            # Hilfsmittel
            gnumake
            jq
            curl
          ];

          # Nur im interaktiven Terminal begruessen, damit `nix develop -c ...`
          # in Skripten und CI keine Fremdausgabe erzeugt.
          shellHook = ''
            if [ -t 1 ]; then
            echo "🌊 Longwave Online — dev environment"
            echo "   go        $(go version | cut -d' ' -f3)"
            echo "   kind      $(kind version | cut -d' ' -f2)"
            echo "   kubectl   $(kubectl version --client -o json 2>/dev/null | jq -r .clientVersion.gitVersion)"
            echo "   helm      $(helm version --short)"
            echo ""
            echo "   make help   für die verfügbaren Targets"
            fi
          '';
        };
      });
}
