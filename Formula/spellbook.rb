# Written by scripts/release.sh. Do not edit.
class Spellbook < Formula
  desc "Share AI skills, instructions, agents and MCP servers from git registries"
  homepage "https://github.com/graemer-org/spellbook"
  version "0.3.0"
  license "Apache-2.0"

  on_macos do
    on_arm do
      url "https://github.com/graemer-org/spellbook/releases/download/v0.3.0/spellbook_0.3.0_darwin_arm64.tar.gz"
      sha256 "bce9e590d71426474d2290e0057a8dcd6298ce52f30e62985304c65f57f6f4bf"
    end
    on_intel do
      url "https://github.com/graemer-org/spellbook/releases/download/v0.3.0/spellbook_0.3.0_darwin_amd64.tar.gz"
      sha256 "6687634b6b08cb527c4dc95f3ac03291f2c8ea86721c184279ec57634b65fd4e"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/graemer-org/spellbook/releases/download/v0.3.0/spellbook_0.3.0_linux_arm64.tar.gz"
      sha256 "4699f697bea42046f094c725aa5b644314509b2e4b01960b45c2f0b23981ca92"
    end
    on_intel do
      url "https://github.com/graemer-org/spellbook/releases/download/v0.3.0/spellbook_0.3.0_linux_amd64.tar.gz"
      sha256 "3b389d76e89fed022866dc41436d39308359e0720635e597e2d49aeefbbf210c"
    end
  end

  def install
    bin.install "spellbook"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/spellbook --version")
  end
end
