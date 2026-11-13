# Written by scripts/release.sh. Do not edit.
class Spellbook < Formula
  desc "Share AI skills, instructions, agents and MCP servers from git registries"
  homepage "https://github.com/graemer-org/spellbook"
  version "0.4.0"
  license "Apache-2.0"

  on_macos do
    on_arm do
      url "https://github.com/graemer-org/spellbook/releases/download/v0.4.0/spellbook_0.4.0_darwin_arm64.tar.gz"
      sha256 "abc8b1e14bf2459b5c692d3cec062d070f608291247494807760d0cfe3ea7ff9"
    end
    on_intel do
      url "https://github.com/graemer-org/spellbook/releases/download/v0.4.0/spellbook_0.4.0_darwin_amd64.tar.gz"
      sha256 "f16bc68d6be574bb728e3e8c5233c17153cb47042f59693b39797800e0da534c"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/graemer-org/spellbook/releases/download/v0.4.0/spellbook_0.4.0_linux_arm64.tar.gz"
      sha256 "4653d7b5ac6a3332fa9327cf4da5c7471bc198658c8017e06b479d43fa870378"
    end
    on_intel do
      url "https://github.com/graemer-org/spellbook/releases/download/v0.4.0/spellbook_0.4.0_linux_amd64.tar.gz"
      sha256 "f88ae9ff861bfa559d0bd994f6156f97c0ae062bbbec414a48f391ff805453f7"
    end
  end

  def install
    bin.install "spellbook"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/spellbook --version")
  end
end
