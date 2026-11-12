# Written by scripts/release.sh. Do not edit.
class Spellbook < Formula
  desc "Share AI skills, instructions, agents and MCP servers from git registries"
  homepage "https://github.com/graemer-org/spellbook"
  version "0.1.0"

  on_macos do
    on_arm do
      url "https://github.com/graemer-org/spellbook/releases/download/v0.1.0/spellbook_0.1.0_darwin_arm64.tar.gz"
      sha256 "db8ba460eb9810b6fd87c067b0be33711596ea37367d2a4c61ad7bdd9652104c"
    end
    on_intel do
      url "https://github.com/graemer-org/spellbook/releases/download/v0.1.0/spellbook_0.1.0_darwin_amd64.tar.gz"
      sha256 "31438bec947d26aaf5085c4b9712a095a731b7833167501d5327a6f58a42b757"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/graemer-org/spellbook/releases/download/v0.1.0/spellbook_0.1.0_linux_arm64.tar.gz"
      sha256 "36a87c614ad0939d7b05b7ec09ece1c4c3d512bf862970814d967ecad10a4b21"
    end
    on_intel do
      url "https://github.com/graemer-org/spellbook/releases/download/v0.1.0/spellbook_0.1.0_linux_amd64.tar.gz"
      sha256 "646c42072e9d8b7fad19818e0416d53da5df3c9c9ed416daa1dc5cf55b336e0a"
    end
  end

  def install
    bin.install "spellbook"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/spellbook --version")
  end
end
