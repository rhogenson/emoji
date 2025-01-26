{
  # Nixpkgs / NixOS version to use.
  inputs.nixpkgs.url = "nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let

      # to work with older version of flakes
      lastModifiedDate = self.lastModifiedDate or self.lastModified or "19700101";

      # Generate a user-friendly version number.
      version = builtins.substring 0 8 lastModifiedDate;

      # System types to support.
      supportedSystems = [ "x86_64-linux" "x86_64-darwin" "aarch64-linux" "aarch64-darwin" ];

      # Helper function to generate an attrset '{ x86_64-linux = f "x86_64-linux"; ... }'.
      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;

      # Nixpkgs instantiated for supported system types.
      nixpkgsFor = forAllSystems (system: import nixpkgs { inherit system; overlays = [ self.overlay ]; });

    in

    {

      # A Nixpkgs overlay.
      overlay = final: prev: {

        emoji = with final; stdenv.mkDerivation rec {
          name = "emoji-${version}";

          unpackPhase = ":";

          buildInputs = with pkgs; [ unzip ];

          src = ./.;

          cldrVersion = "46";

          emojiList = pkgs.fetchurl {
            url = "https://unicode.org/Public/cldr/${cldrVersion}/core.zip";
            sha256 = "sha256-+86cInWGKtJmaPs0eD/mwznz2S3f61oQoXdftYGBoV0=";
          };

          buildPhase = ''
            unzip ${emojiList} common/annotations/en.xml
            grep -E 'annotation cp="[^"]+">' <common/annotations/en.xml |
              sed -E 's/\s+<annotation cp="//g; s/">/ /g; s|</annotation>||g; s/\s\|\s/, /g' > emoji_list

            cat > emoji_picker << EOF
            #!/bin/sh
            ${bemenu}/bin/bemenu -i -l20 <$out/share/emoji_list | ${coreutils}/bin/cut -d' ' -f1 | ${wl-clipboard}/bin/wl-copy -n
            EOF
            chmod +x emoji_picker
          '';

          installPhase = ''
            mkdir -p $out/{bin,share}
            cp emoji_picker $out/bin/
            cp emoji_list $out/share/
          '';
        };

      };

      # Provide some binary packages for selected system types.
      packages = forAllSystems (system:
        {
          inherit (nixpkgsFor.${system}) emoji;
        });

      # The default package for 'nix build'. This makes sense if the
      # flake provides only one package or there is a clear "main"
      # package.
      defaultPackage = forAllSystems (system: self.packages.${system}.emoji);
    };
}
