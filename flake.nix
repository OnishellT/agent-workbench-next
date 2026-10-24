{
  description = "Workbench development shell (Go + bun pinned to the versions in go.mod, web/package.json and Dockerfile)";
  # Same nixpkgs revision as ~/nix-config: provides go 1.26.8 and bun 1.4.2.
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/8edc0c72e3a38faf5434e40d1f19431125fe4b30";
  outputs = { self, nixpkgs }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
    in {
      devShells.${system}.default = pkgs.mkShellNoCC {
        packages = with pkgs; [ go bun python3 gnumake git ];
        # Makefile/Dockerfile build static nogui binaries; keep the same defaults here.
        CGO_ENABLED = "0";
        GOTOOLCHAIN = "local";
      };
    };
}
