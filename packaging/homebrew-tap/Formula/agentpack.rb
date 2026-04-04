class Agentpack < Formula
  desc "Pin GitHub-hosted skills and plugin directories for agent harnesses"
  homepage "https://github.com/OlegHQ/agentpack"
  url "https://github.com/OlegHQ/agentpack/archive/refs/tags/v0.1.0.tar.gz"
  sha256 "0000000000000000000000000000000000000000000000000000000000000000"
  license "MIT"
  head "https://github.com/OlegHQ/agentpack.git", branch: "dev"

  depends_on "rust" => :build

  def install
    system "cargo", "install", *std_cargo_args
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/agentpack --version")
  end
end
