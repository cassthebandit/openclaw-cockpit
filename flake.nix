{
  description = "OpenClaw operator wall for tmux-backed agent work";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs = { self, nixpkgs }:
    let
      supportedSystems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
    in
    {
      packages = forAllSystems (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
        in
        {
          "openclaw-cockpit" = pkgs.buildGoModule {
            pname = "openclaw-cockpit";
            version = "0.9.5";

            src = ./.;

            vendorHash = "sha256-qozRmgemVX+5ye9h0udTi3zBMHMw05RjvFpxyUgqwzI=";

            subPackages = [ "cmd/openclaw-cockpit" ];

            excludedPackages = [ "tools" ];

            meta = with pkgs.lib; {
              description = "OpenClaw operator wall for tmux-backed agent work";
              homepage = "https://github.com/cassthebandit/openclaw-cockpit";
              license = licenses.mit;
              maintainers = [ ];
              mainProgram = "openclaw-cockpit";
            };
          };

          default = self.packages.${system}."openclaw-cockpit";
        }
      );

      apps = forAllSystems (system: {
        "openclaw-cockpit" = {
          type = "app";
          program = "${self.packages.${system}."openclaw-cockpit"}/bin/openclaw-cockpit";
        };

        default = self.apps.${system}."openclaw-cockpit";
      });

      devShells = forAllSystems (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
        in
        {
          default = pkgs.mkShell {
            buildInputs = with pkgs; [
              go
              gopls
              golangci-lint
              gofumpt
              tmux
            ];
          };
        }
      );
    };
}
