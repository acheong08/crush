{
  description = "Crush development environment";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = {
    self,
    nixpkgs,
    flake-utils,
  }:
    flake-utils.lib.eachDefaultSystem (
      system: let
        # Crush is FSL-licensed, which nixpkgs classifies as unfree, so the
        # package could not otherwise be evaluated by this flake's own
        # nixpkgs. Consumers of the flake do not need allowUnfree.
        pkgs = import nixpkgs {
          inherit system;
          config.allowUnfreePredicate = pkg: (pkg.meta.license.spdxId or "") == "FSL-1.1-MIT";
        };

        crush = pkgs.buildGo127Module (finalAttrs: {
          pname = "crush";
          version = "0.98.1+tailcat";

          src = self;

          vendorHash = "sha256-hUsxDxrnrIkuwsFr2oLJ68INoHjQGTOGsxPHoSxt8S8=";

          subPackages = [ "." ];

          ldflags = [
            "-s"
            "-X=github.com/charmbracelet/crush/internal/version.Version=${finalAttrs.version}"
          ];

          # The Go test suite wants network access and a writable HOME that
          # the build sandbox does not provide; run it in CI instead.
          doCheck = false;

          nativeBuildInputs = [ pkgs.installShellFiles ];

          postInstall = pkgs.lib.optionalString (pkgs.stdenv.buildPlatform.canExecute pkgs.stdenv.hostPlatform) ''
            installShellCompletion --cmd crush \
              --bash <($out/bin/crush completion bash) \
              --fish <($out/bin/crush completion fish) \
              --zsh <($out/bin/crush completion zsh)
          '';

          meta = {
            description = "Glamourous AI coding agent for your favourite terminal (tailcat fork)";
            homepage = "https://github.com/acheong08/crush";
            license = pkgs.lib.licenses.fsl11Mit;
            mainProgram = "crush";
          };
        });
      in {
        packages.default = crush;
        packages.crush = crush;

        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            # Go toolchain
            go_1_27

            # Development tools
            gopls # Go language server
            golangci-lint # Linter
            gofumpt # Formatter (stricter than gofmt)
            go-task # Task runner
            delve # Go debugger

            # Additional tools
            git # Version control
            gh # GitHub CLI
            svu # Semantic version utility
            sqlc # SQL code generator
          ];

          shellHook = ''
            # Set Go environment variables
            export CGO_ENABLED=0
          '';
        };
      }
    );
}
