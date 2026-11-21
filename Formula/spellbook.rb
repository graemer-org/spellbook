# Written by scripts/release.sh. Do not edit.
class Spellbook < Formula
  desc "Share AI skills, instructions, agents and MCP servers from git registries"
  homepage "https://github.com/graemer-org/spellbook"
  version "0.5.0"
  license "Apache-2.0"

  on_macos do
    on_arm do
      url "https://github.com/graemer-org/spellbook/releases/download/v0.5.0/spellbook_0.5.0_darwin_arm64.tar.gz"
      sha256 "b9106239e30f198fa6a44e892d63095034482a287a86c1e5cdc6b9d52198e977"
    end
    on_intel do
      url "https://github.com/graemer-org/spellbook/releases/download/v0.5.0/spellbook_0.5.0_darwin_amd64.tar.gz"
      sha256 "bd132837b82f3965407b52a18957d6c3609c63824e7dfa9b4174932c8922fe5b"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/graemer-org/spellbook/releases/download/v0.5.0/spellbook_0.5.0_linux_arm64.tar.gz"
      sha256 "4670c650e9671945017c19444061b1e7052cce9cd6a8a1a222e51cebb2f28f4d"
    end
    on_intel do
      url "https://github.com/graemer-org/spellbook/releases/download/v0.5.0/spellbook_0.5.0_linux_amd64.tar.gz"
      sha256 "9b7bd0c12e047884bf8d630490854b378803008f3225c6503c8bf44cc9d5c604"
    end
  end

  def install
    bin.install "spellbook"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/spellbook --version")
  end
end
