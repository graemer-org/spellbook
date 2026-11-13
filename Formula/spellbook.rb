# Written by scripts/release.sh. Do not edit.
class Spellbook < Formula
  desc "Share AI skills, instructions, agents and MCP servers from git registries"
  homepage "https://github.com/graemer-org/spellbook"
  version "0.2.1"
  license "Apache-2.0"

  on_macos do
    on_arm do
      url "https://github.com/graemer-org/spellbook/releases/download/v0.2.1/spellbook_0.2.1_darwin_arm64.tar.gz"
      sha256 "7d569b39c58db31110fa62285a73fb4b52e163170bf883c69a2644440be30e6d"
    end
    on_intel do
      url "https://github.com/graemer-org/spellbook/releases/download/v0.2.1/spellbook_0.2.1_darwin_amd64.tar.gz"
      sha256 "c32e347146daae57603c917e83d177c2d2692472bd1bc18c2ae4538902db942c"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/graemer-org/spellbook/releases/download/v0.2.1/spellbook_0.2.1_linux_arm64.tar.gz"
      sha256 "58cf83cc164fb856e9ef15665cc8a761bdf5a2621195489199f49dc1c051277e"
    end
    on_intel do
      url "https://github.com/graemer-org/spellbook/releases/download/v0.2.1/spellbook_0.2.1_linux_amd64.tar.gz"
      sha256 "089c111a7fed167efce1058362544995d4b07205d1c107c7baa1b02aec8f13e7"
    end
  end

  def install
    bin.install "spellbook"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/spellbook --version")
  end
end
