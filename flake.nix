{
  description = "A FUSE filesystem (ro or rw) for filtering files based on gitignore patterns";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
  };

  outputs = { self, nixpkgs }:
    let
      supportedSystems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
      pkgsFor = system: nixpkgs.legacyPackages.${system};
    in
    {
      packages = forAllSystems (system: {
        filterfs = (pkgsFor system).buildGoModule {
          pname = "filterfs";
          version = "0.1.0";
          src = ./.;
          vendorHash = "sha256-+x5DiLz/NeinFJuSF5vUciAVMDzwlcLoD8Hwx6ObAvk=";
        };
        default = self.packages.${system}.filterfs;
      });

      devShells = forAllSystems (system: {
        default = (pkgsFor system).mkShell {
          buildInputs = with (pkgsFor system); [
            go
            gopls
          ];
        };
      });
    };
}
