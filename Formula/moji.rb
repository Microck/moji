class Moji < Formula
  desc "Cozy terminal font finder with safe downloads"
  homepage "https://github.com/Microck/moji"
  url "https://registry.npmjs.org/@microck/moji/-/moji-0.8.1.tgz"
  sha256 "cfee5c54106b2fa0332f3dd5ddb556a1a010dd5f6e402ea1f60a56d56e40ff44"
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
