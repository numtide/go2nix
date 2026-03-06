# shellcheck shell=bash
# Atomic hook: set up Go build environment.
setupGoEnv() {
  export HOME="$NIX_BUILD_TOP/home"
  mkdir -p "$HOME"
  export GOPROXY=off
  export GOSUMDB=off
  export GONOSUMCHECK='*'
}

preConfigureHooks+=(setupGoEnv)
