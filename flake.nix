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

          shellHook = ''
            export PS1="(stellar-raw-ledger-origin) $PS1"
          '';
        };
      });

      # `nix build` -> the nebu-style origin binary.
      # `nix build .#raw-ledger-source` -> the flowctl source service.
      packages = forAllSystems (pkgs:
        let
          version = "0.1.0";
        in
        rec {
          raw-ledger-origin = pkgs.buildGoModule {
            pname = "raw-ledger-origin";
            inherit version;
            src = ./.;
            subPackages = [ "processors/raw-ledger-origin/cmd/raw-ledger-origin" ];
            vendorHash = "sha256-AqX75cU/I8ECirG2Sw6IIuw79r6mCTk14iHPIgBfV4U=";
          };

          raw-ledger-source = pkgs.buildGoModule {
            pname = "raw-ledger-source";
            inherit version;
            src = ./.;
            modRoot = "source";
            subPackages = [ "cmd/raw-ledger-source" ];
            vendorHash = "sha256-B8mA8Gqf/vqGW6k7mKLh1iO+yQnR8AKvk0ECGkAkCwM=";
          };

          default = raw-ledger-origin;
        });
    };
}
