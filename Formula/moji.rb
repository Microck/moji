class Moji < Formula
  desc "Cozy terminal font finder with safe downloads"
  homepage "https://github.com/Microck/moji"
  url "https://registry.npmjs.org/@microck/moji/-/moji-0.7.0.tgz"
  sha256 "2430859658fc26fe4da37bff6b27b0900dc28b9f6c92c16ebf26f083a2939afb"
  license "MIT"

  def install
    platform = OS.mac? ? "darwin" : "linux"
    architecture = if Hardware::CPU.arm64?
      "arm64"
    elsif Hardware::CPU.intel?
      "x64"
    else
      odie "Unsupported CPU architecture: #{Hardware::CPU.arch}"
    end
    bin.install "binaries/#{platform}-#{architecture}/moji"
  end

  test do
    assert_equal version.to_s, shell_output("#{bin}/moji --version").strip
  end
end
