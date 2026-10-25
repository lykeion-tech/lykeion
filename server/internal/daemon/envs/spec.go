package envs

type download struct{ url, sha256 string }

const micromambaVersion = "2.9.0-0"

var micromamba = map[string]download{
	"osx-arm64":     {"https://github.com/mamba-org/micromamba-releases/releases/download/2.9.0-0/micromamba-osx-arm64", "ec2a072f028e1a7cf20f3e2e74d5a8127cf5a5f27636375b5359811565f4e5be"},
	"osx-64":        {"https://github.com/mamba-org/micromamba-releases/releases/download/2.9.0-0/micromamba-osx-64", "1e71054bb3ac9a076e21f7ec48acfef536f9b3f1408f371a942784bf5ef83d8a"},
	"linux-64":      {"https://github.com/mamba-org/micromamba-releases/releases/download/2.9.0-0/micromamba-linux-64", "366cd9cd8be14df1ab8ed50352a82111082a36686b2d389fdb79a92c3fafb3e3"},
	"linux-aarch64": {"https://github.com/mamba-org/micromamba-releases/releases/download/2.9.0-0/micromamba-linux-aarch64", "9f93b974adcb4d166996af969b6cd371287d1a3e52733704727884d9b74cb7a7"},
}

const juliaVersion = "1.12.6"

var julia = map[string]download{
	"osx-arm64":     {"https://julialang-s3.julialang.org/bin/mac/aarch64/1.12/julia-1.12.6-macaarch64.tar.gz", "277d82fbd2eda99d0963b3e41f3dc979d7486f181399f8430fb637318ccd6a31"},
	"osx-64":        {"https://julialang-s3.julialang.org/bin/mac/x64/1.12/julia-1.12.6-mac64.tar.gz", "1a70b7c606d6bac38a246e722369e5b30914dccf9378499d2712fb3bd282642c"},
	"linux-64":      {"https://julialang-s3.julialang.org/bin/linux/x64/1.12/julia-1.12.6-linux-x86_64.tar.gz", "bbabf3bef19421a9dbd24a767d807606ab85e444323b5a1c73ffe293fa3d079a"},
	"linux-aarch64": {"https://julialang-s3.julialang.org/bin/linux/aarch64/1.12/julia-1.12.6-linux-aarch64.tar.gz", "029b93b857bd0ffd627f9a8580d3bbaa63daf008d7b7aed02fbceb8fd57c4899"},
}

// Exact versions from conda-forge, each solved for every platform above.
var pythonPins = []string{
	"python==3.13.16", "numpy==2.5.4", "pandas==3.0.6", "scipy==1.18.1", "matplotlib==3.11.2",
	"statsmodels==0.15.0", "ipykernel==7.4.0", "jupyter_client==8.10.0",
}

var rPins = []string{"r-base==4.5.3", "r-tidyverse==2.0.0", "r-ggplot2==4.0.3", "r-jsonlite==2.0.0", "r-irkernel==1.3.2"}

// Exact versions from Julia's General registry.
var juliaPins = [][2]string{{"IJulia", "1.34.4"}, {"DataFrames", "1.8.2"}, {"CSV", "1.1.0"}}

const elanVersion = "4.2.4"

var elan = map[string]download{
	"osx-arm64":     {"https://github.com/leanprover/elan/releases/download/v4.2.4/elan-aarch64-apple-darwin.tar.gz", "7ad829861392c718dfebde3a83b5c8508df47be02af68894b094b0b3952616e5"},
	"linux-aarch64": {"https://github.com/leanprover/elan/releases/download/v4.2.4/elan-aarch64-unknown-linux-gnu.tar.gz", "05febd124d84ebf994b2e7479922a5650b1e950c17ae3bd1ddd776b65bb72bf9"},
	"osx-64":        {"https://github.com/leanprover/elan/releases/download/v4.2.4/elan-x86_64-apple-darwin.tar.gz", "8a340b309d8ed2e96f930761fa223b3af57a38f5d253b53ac90293c9516f8cd4"},
	"linux-64":      {"https://github.com/leanprover/elan/releases/download/v4.2.4/elan-x86_64-unknown-linux-gnu.tar.gz", "42b94d4244e8353142c456ec0e4ca6528fd898a6c604d4059f494e706e431f63"},
}
