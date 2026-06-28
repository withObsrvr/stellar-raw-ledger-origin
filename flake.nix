{
  description = "Shared raw Stellar ledger core with nebu (origin) and flowctl (source) adapters";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      # `nix develop` -> Go toolchain plus the protobuf tooling needed to
      # regenerate gen/ from proto/ (see `make proto`).
      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = [
            pkgs.go
            pkgs.gopls
            pkgs.protobuf
            pkgs.protoc-gen-go
            pkgs.gnumake
          ];
        };
      });

      # `nix build` -> the nebu-style origin binary.
      packages = forAllSystems (pkgs: {
        default = pkgs.buildGoModule {
          pname = "raw-ledger-origin";
          version = "0.1.0";
          src = ./.;
          subPackages = [ "processors/raw-ledger-origin/cmd/raw-ledger-origin" ];
          vendorHash = "sha256-AqX75cU/I8ECirG2Sw6IIuw79r6mCTk14iHPIgBfV4U=";
        };
      });
    };
}
