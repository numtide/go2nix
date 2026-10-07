module example.com/toolchain-line

go 1.23

toolchain go1.99.0

require example.com/dep v0.1.0

replace example.com/dep => ./dep
