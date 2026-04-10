// Nix plugin shim for builtins.resolveGoPackages.
//
// Serializes the input attrset to JSON via printValueAsJSON, calls the
// Rust resolve_go_packages_json(), and parses the result back via
// parseJSON. Handles chroot store path remapping for src/go attributes.

#include <nix/expr/eval.hh>
#include <nix/expr/primops.hh>
#include <nix/expr/value.hh>
#include <nix/expr/json-to-value.hh>
#include <nix/expr/value-to-json.hh>
#include <nix/store/local-fs-store.hh>
#include <nix/util/util.hh>
#include <nlohmann/json.hpp>

extern "C" {
    int resolve_go_packages_json(
        const char *input_json,
        char **out,
        char **err_out
    );
    void go2nix_free_string(char *s);
}

using namespace nix;

/**
 * Remap a logical store path to the real filesystem path for chroot
 * stores (--store /tmp/foo) so that go list can read sources during eval.
 */
static std::string remapStorePath(Store &store, const std::string &path) {
    auto *localFS = dynamic_cast<LocalFSStore *>(&store);
    if (!localFS)
        return path;

    auto realStoreDir = localFS->getRealStoreDir();
    auto logicalStoreDir = store.storeDir;

    if (realStoreDir == logicalStoreDir)
        return path;

    if (path.substr(0, logicalStoreDir.size()) != logicalStoreDir)
        return path;

    return realStoreDir + path.substr(logicalStoreDir.size());
}

static void prim_resolveGoPackages(EvalState &state, const PosIdx pos,
                                    Value **args, Value &v) {
    state.forceAttrs(*args[0], pos,
        "while evaluating the argument to builtins.resolveGoPackages");

    NixStringContext context;
    auto inputJson = printValueAsJSON(state, true, *args[0], pos, context, false);

    // The default-mode call site (nix/dag/default.nix) passes only source
    // paths, so this loop sees Opaque context only and evaluation stays
    // IFD-free. When a caller passes a derivation-backed value
    // (e.g. src = pkgs.fetchFromGitHub {...}), warn and fall through to
    // realiseContext so it still works — that is opt-in IFD by construction
    // and remains gated by allow-import-from-derivation.
    bool needRealise = false;
    for (const auto &c : context) {
        std::visit(overloaded {
            [&](const NixStringContextElem::Opaque &o) {
                state.store->ensurePath(o.path);
            },
            [&](const NixStringContextElem::Built &) {
                warn("resolveGoPackages: realising derivation '%s' at eval time "
                     "(IFD). For IFD-free evaluation, use builtins.fetchTarball/"
                     "fetchGit for 'src' instead of pkgs.fetchFromGitHub. "
                     "See go2nix docs: builder-api.md#src.",
                     c.display(*state.store));
                needRealise = true;
            },
            [&](const NixStringContextElem::DrvDeep &) {
                warn("resolveGoPackages: realising derivation closure '%s' at "
                     "eval time (IFD).",
                     c.display(*state.store));
                needRealise = true;
            },
        }, c.raw);
    }
    if (needRealise) {
        try {
            auto _ = state.realiseContext(context);
        } catch (InvalidPathError &e) {
            state.error<EvalError>(
                "resolveGoPackages: cannot realise context for '%s': %s",
                e.path.to_string(), e.what())
                .atPos(pos).debugThrow();
        }
    }

    for (const auto &key : {"src", "go"}) {
        if (inputJson.contains(key) && inputJson[key].is_string()) {
            inputJson[key] = remapStorePath(*state.store, inputJson[key].get<std::string>());
        }
    }

    auto inputStr = inputJson.dump();

    char *resultJson = nullptr;
    char *errorMsg = nullptr;

    int rc = resolve_go_packages_json(inputStr.c_str(), &resultJson, &errorMsg);

    if (rc != 0) {
        std::string err = errorMsg ? errorMsg : "unknown error";
        if (errorMsg) go2nix_free_string(errorMsg);
        state.error<EvalError>("resolveGoPackages: %s", err).atPos(pos).debugThrow();
    }

    std::string result(resultJson);
    go2nix_free_string(resultJson);

    parseJSON(state, result, v);
}

// Nix >=2.34 renamed PrimOp::fun to PrimOp::impl (see CMakeLists.txt).
static RegisterPrimOp rp(PrimOp {
    .name = "resolveGoPackages",
    .args = {"attrs"},
    .arity = 1,
    .doc = R"(
  Discover the Go package graph at eval time by running `go list`.

  Accepts an attrset with:
  - `go` (optional): Path to the Go binary. Defaults to the toolchain
    baked into the plugin at build time; omit it to keep evaluation
    IFD-free.
  - `src`: Path to the Go source directory. Source paths (./. or
    builtins.fetchTarball/fetchGit) keep evaluation IFD-free; a
    derivation-backed value (pkgs.fetchFromGitHub) is realised at eval
    time with a warning.
  - `tags` (optional): List of build tags
  - `subPackages` (optional): List of package patterns (default: ["./..."])
  - `modRoot` (optional): Subdirectory containing go.mod (default: ".")
  - `goos` / `goarch` (optional): Cross-compilation targets
  - `goProxy` (optional): GOPROXY override; when unset, inherits the environment
  - `cgoEnabled` (optional): CGO_ENABLED value
  - `doCheck` (optional): When true, runs a second `go list -deps -test`
    pass to discover test-only third-party dependencies (default: false)
  - `resolveHashes` (optional): When true, resolves NAR hashes for all
    modules from go.sum + GOMODCACHE, enabling lockfile-free builds.
    Returns `moduleHashes` in the output (default: false)

  Returns: { packages, localPackages, modulePath, goVersion, replacements,
    testPackages, moduleHashes (when resolveHashes=true) }
)",
#ifdef NIX_PRIMOP_HAS_IMPL
    .impl = prim_resolveGoPackages,
#else
    .fun = prim_resolveGoPackages,
#endif
});
