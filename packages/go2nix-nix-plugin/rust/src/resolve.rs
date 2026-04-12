//! Core implementation of Go package resolution.
//!
//! Runs `go list -json -deps -e`, parses the output into a `PackageGraph`,
//! and serializes it to JSON for the C++ nix shim.

use anyhow::{anyhow, bail, Context, Result};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::collections::{BTreeMap, BTreeSet, VecDeque};
use std::path::{Component, Path, PathBuf};
use std::process::Command;

/// Baked-in default Go binary path, set at compile time via GO2NIX_DEFAULT_GO.
/// `option_env!` (not `env!`) so the plugin still compiles when the var is unset;
/// an explicit `go` field in the input always takes precedence.
pub(crate) const DEFAULT_GO: Option<&str> = option_env!("GO2NIX_DEFAULT_GO");

// ---------------------------------------------------------------------------
// Go list JSON types
// ---------------------------------------------------------------------------

#[derive(Deserialize, Default)]
#[serde(default)]
struct GoModule {
    #[serde(rename = "Path")]
    path: String,
    #[serde(rename = "Version")]
    version: String,
    #[serde(rename = "Main")]
    main: bool,
    #[serde(rename = "GoVersion")]
    go_version: String,
    #[serde(rename = "Replace")]
    replace: Option<Box<GoModule>>,
}

#[derive(Deserialize, Default)]
#[serde(default)]
struct GoPackage {
    #[serde(rename = "ImportPath")]
    import_path: String,
    #[serde(rename = "Dir")]
    dir: String,
    #[serde(rename = "Module")]
    module: Option<GoModule>,
    #[serde(rename = "Imports")]
    imports: Vec<String>,
    #[serde(rename = "GoFiles")]
    go_files: Vec<String>,
    #[serde(rename = "CgoFiles")]
    cgo_files: Vec<String>,
    #[serde(rename = "SFiles")]
    s_files: Vec<String>,
    #[serde(rename = "CFiles")]
    c_files: Vec<String>,
    #[serde(rename = "CXXFiles")]
    cxx_files: Vec<String>,
    #[serde(rename = "MFiles")]
    m_files: Vec<String>,
    #[serde(rename = "FFiles")]
    f_files: Vec<String>,
    #[serde(rename = "HFiles")]
    h_files: Vec<String>,
    #[serde(rename = "SysoFiles")]
    syso_files: Vec<String>,
    #[serde(rename = "SwigFiles")]
    swig_files: Vec<String>,
    #[serde(rename = "SwigCXXFiles")]
    swig_cxx_files: Vec<String>,
    #[serde(rename = "EmbedPatterns")]
    embed_patterns: Vec<String>,
    #[serde(rename = "EmbedFiles")]
    embed_files: Vec<String>,
    #[serde(rename = "TestGoFiles")]
    test_go_files: Vec<String>,
    #[serde(rename = "XTestGoFiles")]
    x_test_go_files: Vec<String>,
    #[serde(rename = "TestEmbedFiles")]
    test_embed_files: Vec<String>,
    #[serde(rename = "XTestEmbedFiles")]
    x_test_embed_files: Vec<String>,
    #[serde(rename = "CgoPkgConfig")]
    cgo_pkg_config: Vec<String>,
    #[serde(rename = "CgoCFLAGS")]
    cgo_cflags: Vec<String>,
    #[serde(rename = "CgoLDFLAGS")]
    cgo_ldflags: Vec<String>,
    #[serde(rename = "Error")]
    error: Option<GoPackageError>,
}

#[derive(Deserialize, Default)]
#[serde(default)]
struct GoPackageError {
    #[serde(rename = "Err")]
    err: String,
}

// ---------------------------------------------------------------------------
// Processed package data
// ---------------------------------------------------------------------------

pub(crate) struct PkgData {
    import_path: String,
    mod_path: String,
    mod_version: String,
    replace_version: String,
    imports: Vec<String>,
    cgo_pkg_config: Vec<String>,
    cgo_cflags: Vec<String>,
    cgo_ldflags: Vec<String>,
    is_cgo: bool,
    files: PkgFiles,
}

struct LocalPkgData {
    import_path: String,
    dir: String,
    local_imports: Vec<String>,
    third_party_imports: Vec<String>,
    cgo_pkg_config: Vec<String>,
    cgo_cflags: Vec<String>,
    cgo_ldflags: Vec<String>,
    is_cgo: bool,
    files: PkgFiles,
    /// Dir-relative paths the precise mainSrc filter must include for this
    /// package: every compiled source kind plus resolved //go:embed targets
    /// (build, test, and xtest). The test-embed entries are merged in from
    /// the `-test` pass since `go list` only resolves them under `-test`.
    main_src_files: Vec<String>,
}

/// Raw local package data collected during a parse pass; imports are
/// classified into local vs third-party after the full set is known.
struct RawLocalPkg {
    import_path: String,
    dir: String,
    imports: Vec<String>,
    cgo_pkg_config: Vec<String>,
    cgo_cflags: Vec<String>,
    cgo_ldflags: Vec<String>,
    is_cgo: bool,
    files: PkgFiles,
    main_src_files: Vec<String>,
}

/// Per-package source file lists as reported by `go list`, threaded through
/// to the compile manifest so build-time can skip its own discovery pass.
#[derive(Clone, Debug, Default, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct PkgFiles {
    #[serde(skip_serializing_if = "Vec::is_empty")]
    go_files: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    cgo_files: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    s_files: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    c_files: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    cxx_files: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    m_files: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    f_files: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    h_files: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    syso_files: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    swig_files: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    swig_cxx_files: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    embed_patterns: Vec<String>,
}

impl PkgFiles {
    fn is_empty(&self) -> bool {
        self.go_files.is_empty()
            && self.cgo_files.is_empty()
            && self.s_files.is_empty()
            && self.c_files.is_empty()
            && self.cxx_files.is_empty()
            && self.m_files.is_empty()
            && self.f_files.is_empty()
            && self.h_files.is_empty()
            && self.syso_files.is_empty()
            && self.swig_files.is_empty()
            && self.swig_cxx_files.is_empty()
            && self.embed_patterns.is_empty()
    }
}

fn pkg_files_from(p: &GoPackage) -> PkgFiles {
    PkgFiles {
        go_files: p.go_files.clone(),
        cgo_files: p.cgo_files.clone(),
        s_files: p.s_files.clone(),
        c_files: p.c_files.clone(),
        cxx_files: p.cxx_files.clone(),
        m_files: p.m_files.clone(),
        f_files: p.f_files.clone(),
        h_files: p.h_files.clone(),
        syso_files: p.syso_files.clone(),
        swig_files: p.swig_files.clone(),
        swig_cxx_files: p.swig_cxx_files.clone(),
        embed_patterns: p.embed_patterns.clone(),
    }
}

/// Dir-relative paths the precise mainSrc filter must include for this
/// package. Covers every source kind `go/build.ImportDir` reads (so the
/// testrunner walk sees exactly what `go list` did) plus resolved
/// //go:embed targets. {Test,XTest}EmbedFiles are only populated under
/// `go list -test`; the build pass leaves them empty and the test pass
/// merges them in for build-graph locals.
fn main_src_files_from(p: &GoPackage) -> Vec<String> {
    let mut out = BTreeSet::new();
    for v in [
        &p.go_files,
        &p.cgo_files,
        &p.s_files,
        &p.c_files,
        &p.cxx_files,
        &p.m_files,
        &p.f_files,
        &p.h_files,
        &p.syso_files,
        &p.swig_files,
        &p.swig_cxx_files,
        &p.embed_files,
        &p.test_go_files,
        &p.x_test_go_files,
        &p.test_embed_files,
        &p.x_test_embed_files,
    ] {
        out.extend(v.iter().cloned());
    }
    out.into_iter().collect()
}

/// Cap on the sanitized component so the full derivation name (prefix +
/// sanitized + version suffix) stays under Nix's 211-char store-name limit.
/// Mirrored in go/go2nix/pkg/nixdrv/sanitize.go and nix/helpers.nix — keep
/// all three in sync.
const MAX_SANITIZED_LEN: usize = 160;

fn sanitize_name(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    for c in s.chars() {
        match c {
            'a'..='z' | 'A'..='Z' | '0'..='9' | '+' | '-' | '.' | '_' | '?' | '=' => {
                out.push(c);
            }
            '/' => out.push('-'),
            '@' => out.push_str("_at_"),
            _ => out.push('_'),
        }
    }
    if out.len() <= MAX_SANITIZED_LEN {
        return out;
    }
    // Sanitized output is ASCII-only, so byte truncation is char-safe.
    let d = Sha256::digest(s.as_bytes());
    let h = format!("{:02x}{:02x}{:02x}{:02x}", d[0], d[1], d[2], d[3]);
    out.truncate(MAX_SANITIZED_LEN - 9);
    out.push('-');
    out.push_str(&h);
    out
}

/// Strip the patch component from a Go version string to match the
/// `-lang` flag format expected by `go tool compile` (e.g. "1.21.3" → "1.21").
/// Mirrors `internal/gover.Lang` in cmd/go.
fn lang_version(v: &str) -> String {
    let mut it = v.splitn(3, '.');
    match (it.next(), it.next()) {
        (Some(major), Some(minor)) => format!("{major}.{minor}"),
        _ => v.to_owned(),
    }
}

fn inherit_env(keys: &[&str]) -> Vec<(String, String)> {
    keys.iter()
        .filter_map(|k| std::env::var(k).ok().map(|v| (k.to_string(), v)))
        .collect()
}

/// Extract replace path and version from a GoModule in one destructure.
fn extract_replace(module: &GoModule) -> (String, String) {
    match &module.replace {
        Some(r) => (r.path.clone(), r.version.clone()),
        None => (String::new(), String::new()),
    }
}

// ---------------------------------------------------------------------------
// go list execution
// ---------------------------------------------------------------------------

struct GoListOpts<'a> {
    tags: &'a [String],
    mod_root: &'a str,
    goos: &'a str,
    goarch: &'a str,
    go_proxy: Option<&'a str>,
    cgo_enabled: &'a str,
}

/// Configure common Go environment on a Command.
///
/// Sets up GOMODCACHE, GOPROXY, cross-compilation vars, and conditional
/// network vars. Shared between build and test go list invocations.
fn configure_go_env(cmd: &mut Command, src_dir: &str, opts: &GoListOpts) {
    let work_dir = if opts.mod_root == "." {
        src_dir.to_owned()
    } else {
        format!("{src_dir}/{}", opts.mod_root)
    };
    cmd.current_dir(&work_dir);

    // Minimal environment — GOROOT is self-detected from the binary location,
    // -buildvcs=false avoids VCS tool lookups. GOPROXY/NETRC are inherited so
    // private/caching proxies configured in the host environment are respected;
    // an explicit goProxy arg still wins below.
    cmd.env_clear();
    for (k, v) in inherit_env(&["GOMODCACHE", "GOPATH", "HOME", "GOPROXY", "NETRC"]) {
        cmd.env(&k, &v);
    }
    if let Some(proxy) = opts.go_proxy {
        cmd.env("GOPROXY", proxy);
    }
    cmd.env("GONOSUMCHECK", "*");
    cmd.env("GOFLAGS", "-mod=readonly");
    cmd.env("GOENV", "off");
    cmd.env("GOWORK", "off");

    // When GOPROXY is not "off", the go toolchain needs network access.
    let proxy_off = opts.go_proxy.map_or(false, |p| p == "off");
    if !proxy_off {
        for (k, v) in inherit_env(&[
            "PATH",
            "TMPDIR",
            "SSL_CERT_FILE",
            "SSL_CERT_DIR",
            "NIX_SSL_CERT_FILE",
        ]) {
            cmd.env(&k, &v);
        }
        cmd.env("GIT_TERMINAL_PROMPT", "0");
    }

    if !opts.goos.is_empty() {
        cmd.env("GOOS", opts.goos);
    }
    if !opts.goarch.is_empty() {
        cmd.env("GOARCH", opts.goarch);
    }
    if !opts.cgo_enabled.is_empty() {
        cmd.env("CGO_ENABLED", opts.cgo_enabled);
    }
}

fn run_go_list(
    go_bin: &str,
    src_dir: &str,
    sub_packages: &[String],
    opts: &GoListOpts,
) -> Result<Vec<u8>> {
    let mut cmd = Command::new(go_bin);
    cmd.arg("list");
    cmd.arg("-json=ImportPath,Dir,Module,Imports,GoFiles,CgoFiles,SFiles,CFiles,CXXFiles,MFiles,FFiles,HFiles,SysoFiles,SwigFiles,SwigCXXFiles,EmbedPatterns,EmbedFiles,TestGoFiles,XTestGoFiles,TestEmbedFiles,XTestEmbedFiles,CgoPkgConfig,CgoCFLAGS,CgoLDFLAGS,Error");
    cmd.arg("-deps");
    cmd.arg("-e");
    cmd.arg("-buildvcs=false");
    // -pgo=off short-circuits cmd/go's per-main-package default.pgo walk
    // (load/pkg.go setPGOProfilePath); -mod=readonly is the module-mode
    // default but pinning it makes the env-independence explicit.
    cmd.arg("-pgo=off");
    cmd.arg("-mod=readonly");

    if !opts.tags.is_empty() {
        cmd.arg("-tags");
        cmd.arg(opts.tags.join(","));
    }
    for pkg in sub_packages {
        cmd.arg(pkg);
    }

    configure_go_env(&mut cmd, src_dir, opts);

    let output = cmd
        .output()
        .with_context(|| format!("resolveGoPackages: failed to execute '{go_bin}'"))?;

    if !output.status.success() {
        let stderr = String::from_utf8_lossy(&output.stderr);
        bail!(
            "resolveGoPackages: 'go list' failed (exit {}).\n{stderr}\n\
             Hint: ensure all modules are in your local cache ('go mod download').",
            output.status.code().unwrap_or(-1)
        );
    }

    Ok(output.stdout)
}

fn run_go_list_test(
    go_bin: &str,
    src_dir: &str,
    local_import_paths: &[String],
    opts: &GoListOpts,
) -> Result<Vec<u8>> {
    let mut cmd = Command::new(go_bin);
    cmd.arg("list");
    cmd.arg("-json=ImportPath,Dir,Module,Imports,GoFiles,CgoFiles,SFiles,CFiles,CXXFiles,MFiles,FFiles,HFiles,SysoFiles,SwigFiles,SwigCXXFiles,EmbedPatterns,EmbedFiles,TestGoFiles,XTestGoFiles,TestEmbedFiles,XTestEmbedFiles,CgoPkgConfig,CgoCFLAGS,CgoLDFLAGS,Error");
    cmd.arg("-deps");
    cmd.arg("-test");
    cmd.arg("-e");
    cmd.arg("-buildvcs=false");
    cmd.arg("-pgo=off");
    cmd.arg("-mod=readonly");

    if !opts.tags.is_empty() {
        cmd.arg("-tags");
        cmd.arg(opts.tags.join(","));
    }
    for ip in local_import_paths {
        cmd.arg(ip);
    }

    configure_go_env(&mut cmd, src_dir, opts);

    let output = cmd
        .output()
        .with_context(|| format!("resolveGoPackages: failed to execute '{go_bin}'"))?;

    if !output.status.success() {
        let stderr = String::from_utf8_lossy(&output.stderr);
        bail!(
            "resolveGoPackages: 'go list -test' failed (exit {}).\n{stderr}\n\
             Hint: check the error output above, and ensure all test \
             dependencies are in your local cache by running 'go mod download'.",
            output.status.code().unwrap_or(-1)
        );
    }

    Ok(output.stdout)
}

struct TestPassResult {
    test_packages: Vec<PkgData>,
    test_local_packages: Vec<LocalPkgData>,
    /// importPath → {Test,XTest}EmbedFiles for build-graph locals. `go list`
    /// only resolves test embed files under `-test`, so the build pass left
    /// these empty; merged into local_packages[].main_src_files by the caller.
    local_test_embed_files: BTreeMap<String, Vec<String>>,
}

fn parse_test_packages(
    stdout: &[u8],
    third_party_paths: &BTreeSet<String>,
    local_paths: &BTreeSet<String>,
    replacements: &mut BTreeMap<String, (String, String)>,
) -> Result<TestPassResult> {
    let mut test_packages = Vec::new();
    let mut test_only_paths = BTreeSet::new();
    let mut test_local_paths = BTreeSet::new();
    let mut raw_test_locals: Vec<RawLocalPkg> = Vec::new();
    let mut local_test_embed_files: BTreeMap<String, Vec<String>> = BTreeMap::new();
    let mut pkg_errors = Vec::new();

    for result in serde_json::Deserializer::from_slice(stdout).into_iter::<GoPackage>() {
        let jpkg = result.context("resolveGoPackages: failed to parse test go list JSON")?;

        if let Some(ref err) = jpkg.error {
            if !err.err.is_empty() {
                pkg_errors.push(format!("{}: {}", jpkg.import_path, err.err));
                continue;
            }
        }

        // Skip synthetic test packages: test mains (foo.test) and
        // recompiled variants (foo [foo.test]).
        if jpkg.import_path.contains('[') || jpkg.import_path.ends_with(".test") {
            continue;
        }

        let files = pkg_files_from(&jpkg);
        let main_src_files = main_src_files_from(&jpkg);

        // Skip stdlib (no module).
        let Some(module) = jpkg.module else {
            continue;
        };

        let (replace_path, replace_version) = extract_replace(&module);

        // Local = main module or filesystem-path replace.
        let is_local = module.main || (!replace_path.is_empty() && replace_version.is_empty());
        if is_local {
            // For build-graph locals, harvest the {Test,XTest}EmbedFiles that
            // only resolve under -test; the rest are test-only locals
            // (e.g. an internal/testutil only imported from *_test.go files).
            if local_paths.contains(&jpkg.import_path) {
                if !jpkg.test_embed_files.is_empty() || !jpkg.x_test_embed_files.is_empty() {
                    let mut v = jpkg.test_embed_files.clone();
                    v.extend(jpkg.x_test_embed_files.clone());
                    local_test_embed_files.insert(jpkg.import_path.clone(), v);
                }
                continue;
            }
            if !test_local_paths.insert(jpkg.import_path.clone()) {
                continue;
            }
            raw_test_locals.push(RawLocalPkg {
                import_path: jpkg.import_path,
                dir: jpkg.dir,
                imports: jpkg.imports,
                cgo_pkg_config: jpkg.cgo_pkg_config,
                cgo_cflags: jpkg.cgo_cflags,
                cgo_ldflags: jpkg.cgo_ldflags,
                is_cgo: !jpkg.cgo_files.is_empty(),
                files,
                main_src_files,
            });
            continue;
        }

        // Skip packages already in the build graph.
        if third_party_paths.contains(&jpkg.import_path) {
            continue;
        }

        // Deduplicate within the test pass.
        if !test_only_paths.insert(jpkg.import_path.clone()) {
            continue;
        }

        // Collect replacements from test-only deps.
        if !replace_path.is_empty() {
            let effective_version = if replace_version.is_empty() {
                &module.version
            } else {
                &replace_version
            };
            let mod_key = format!("{}@{effective_version}", module.path);
            replacements
                .entry(mod_key)
                .or_insert_with(|| (replace_path.clone(), replace_version.clone()));
        }

        test_packages.push(PkgData {
            import_path: jpkg.import_path,
            mod_path: module.path,
            mod_version: module.version,
            replace_version,
            imports: jpkg.imports,
            cgo_pkg_config: jpkg.cgo_pkg_config,
            cgo_cflags: jpkg.cgo_cflags,
            cgo_ldflags: jpkg.cgo_ldflags,
            is_cgo: !jpkg.cgo_files.is_empty(),
            files,
        });
    }

    if !pkg_errors.is_empty() {
        let errs = pkg_errors
            .iter()
            .map(|e| format!("  - {e}"))
            .collect::<Vec<_>>()
            .join("\n");
        bail!(
            "resolveGoPackages: test dependency errors:\n{errs}\n\
             Hint: your GOMODCACHE may be stale. Run 'go mod download' to populate it."
        );
    }

    // Classify test-only-local imports against the union of build-graph and
    // test-pass package sets, mirroring parse_go_packages.
    let test_local_packages: Vec<LocalPkgData> = raw_test_locals
        .into_iter()
        .map(|raw| {
            let mut local_imports = Vec::new();
            let mut third_party_imports = Vec::new();
            for imp in &raw.imports {
                if local_paths.contains(imp) || test_local_paths.contains(imp) {
                    local_imports.push(imp.clone());
                } else if third_party_paths.contains(imp) || test_only_paths.contains(imp) {
                    third_party_imports.push(imp.clone());
                }
            }
            LocalPkgData {
                import_path: raw.import_path,
                dir: raw.dir,
                local_imports,
                third_party_imports,
                cgo_pkg_config: raw.cgo_pkg_config,
                cgo_cflags: raw.cgo_cflags,
                cgo_ldflags: raw.cgo_ldflags,
                is_cgo: raw.is_cgo,
                files: raw.files,
                main_src_files: raw.main_src_files,
            }
        })
        .collect();

    Ok(TestPassResult {
        test_packages,
        test_local_packages,
        local_test_embed_files,
    })
}

// ---------------------------------------------------------------------------
// JSON → package graph
// ---------------------------------------------------------------------------

pub(crate) struct PackageGraph {
    packages: Vec<PkgData>,
    local_packages: Vec<LocalPkgData>,
    third_party_paths: BTreeSet<String>,
    local_paths: BTreeSet<String>,
    replacements: BTreeMap<String, (String, String)>,
    module_path: String,
    /// Main module's `go` directive (major.minor), threaded to local
    /// per-package compiles as `-lang` so non-root subpackages — whose
    /// filtered srcDir lacks `go.mod` — match `go build` semantics.
    go_version: String,
    test_packages: Vec<PkgData>,
    test_local_packages: Vec<LocalPkgData>,
    test_only_paths: BTreeSet<String>,
}

impl PackageGraph {
    /// Set of "path@version" module keys actually referenced by the build
    /// graph (third-party + test packages, plus replacement targets). Used
    /// to filter go.sum before NAR-hashing — go.sum is a superset of the
    /// build list (it includes every module MVS considered).
    pub(crate) fn required_module_keys(&self) -> BTreeSet<String> {
        let mut keys = BTreeSet::new();
        for p in self.packages.iter().chain(self.test_packages.iter()) {
            let effective = if p.replace_version.is_empty() {
                &p.mod_version
            } else {
                &p.replace_version
            };
            keys.insert(format!("{}@{}", p.mod_path, effective));
        }
        // Replacement targets are what go.sum actually keys on when a
        // module is replaced; include them so the hash is available.
        for (path, version) in self.replacements.values() {
            if !version.is_empty() {
                keys.insert(format!("{path}@{version}"));
            }
        }
        keys
    }
}

pub(crate) fn parse_go_packages(stdout: &[u8]) -> Result<PackageGraph> {
    let mut packages = Vec::new();
    let mut pkg_errors = Vec::new();
    let mut third_party_paths = BTreeSet::new();
    let mut local_paths = BTreeSet::new();
    let mut replacements: BTreeMap<String, (String, String)> = BTreeMap::new();
    let mut module_path = String::new();
    let mut go_version = String::new();
    let mut raw_local_pkgs: Vec<RawLocalPkg> = Vec::new();

    for result in serde_json::Deserializer::from_slice(stdout).into_iter::<GoPackage>() {
        let jpkg = result.context("resolveGoPackages: failed to parse go list JSON")?;

        if let Some(ref err) = jpkg.error {
            if !err.err.is_empty() {
                pkg_errors.push(format!("{}: {}", jpkg.import_path, err.err));
                continue;
            }
        }

        let files = pkg_files_from(&jpkg);
        let main_src_files = main_src_files_from(&jpkg);

        // stdlib packages have no module
        let Some(module) = jpkg.module else {
            continue;
        };

        let (replace_path, replace_version) = extract_replace(&module);

        // Local = main module, or a replace with empty version (filesystem path)
        let is_local = module.main || (!replace_path.is_empty() && replace_version.is_empty());

        if is_local {
            // Capture main module path from first main-module package.
            if module.main && module_path.is_empty() {
                module_path = module.path.clone();
            }
            // Capture the main module's go directive (major.minor) for -lang.
            if module.main && go_version.is_empty() && !module.go_version.is_empty() {
                go_version = lang_version(&module.go_version);
            }

            local_paths.insert(jpkg.import_path.clone());

            raw_local_pkgs.push(RawLocalPkg {
                import_path: jpkg.import_path,
                dir: jpkg.dir,
                imports: jpkg.imports,
                cgo_pkg_config: jpkg.cgo_pkg_config,
                cgo_cflags: jpkg.cgo_cflags,
                cgo_ldflags: jpkg.cgo_ldflags,
                is_cgo: !jpkg.cgo_files.is_empty(),
                files,
                main_src_files,
            });

            continue;
        }

        if !replace_path.is_empty() {
            let effective_version = if replace_version.is_empty() {
                &module.version
            } else {
                &replace_version
            };
            let mod_key = format!("{}@{effective_version}", module.path);
            replacements
                .entry(mod_key)
                .or_insert_with(|| (replace_path.clone(), replace_version.clone()));
        }

        third_party_paths.insert(jpkg.import_path.clone());

        packages.push(PkgData {
            import_path: jpkg.import_path,
            mod_path: module.path,
            mod_version: module.version,
            replace_version,
            imports: jpkg.imports,
            cgo_pkg_config: jpkg.cgo_pkg_config,
            cgo_cflags: jpkg.cgo_cflags,
            cgo_ldflags: jpkg.cgo_ldflags,
            is_cgo: !jpkg.cgo_files.is_empty(),
            files,
        });
    }

    if !pkg_errors.is_empty() {
        let errs = pkg_errors
            .iter()
            .map(|e| format!("  - {e}"))
            .collect::<Vec<_>>()
            .join("\n");
        bail!(
            "resolveGoPackages: package errors:\n{errs}\n\
             Hint: your GOMODCACHE may be stale. Run 'go mod download' to populate it."
        );
    }

    // Classify each local package's imports into local vs third-party.
    let local_packages: Vec<LocalPkgData> = raw_local_pkgs
        .into_iter()
        .map(|raw| {
            let mut local_imports = Vec::new();
            let mut third_party_imports = Vec::new();
            for imp in &raw.imports {
                if local_paths.contains(imp) {
                    local_imports.push(imp.clone());
                } else if third_party_paths.contains(imp) {
                    third_party_imports.push(imp.clone());
                }
            }
            LocalPkgData {
                import_path: raw.import_path,
                dir: raw.dir,
                local_imports,
                third_party_imports,
                cgo_pkg_config: raw.cgo_pkg_config,
                cgo_cflags: raw.cgo_cflags,
                cgo_ldflags: raw.cgo_ldflags,
                is_cgo: raw.is_cgo,
                files: raw.files,
                main_src_files: raw.main_src_files,
            }
        })
        .collect();

    Ok(PackageGraph {
        packages,
        local_packages,
        third_party_paths,
        local_paths,
        replacements,
        module_path,
        go_version,
        test_packages: Vec::new(),
        test_local_packages: Vec::new(),
        test_only_paths: BTreeSet::new(),
    })
}

// ---------------------------------------------------------------------------
// JSON FFI types
// ---------------------------------------------------------------------------

/// Deserializable input matching the `builtins.resolveGoPackages` attrset.
#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct JsonInput {
    #[serde(default)]
    pub(crate) go: Option<String>,
    pub(crate) src: String,
    #[serde(default)]
    do_check: bool,
    #[serde(default)]
    tags: Vec<String>,
    #[serde(default = "default_sub_packages")]
    sub_packages: Vec<String>,
    #[serde(default = "default_dot")]
    pub(crate) mod_root: String,
    #[serde(default)]
    goos: String,
    #[serde(default)]
    goarch: String,
    #[serde(default)]
    go_proxy: Option<String>,
    #[serde(default)]
    cgo_enabled: String,
    /// When true, resolve NAR hashes for all modules from go.sum + GOMODCACHE.
    /// Enables lockfile-free builds.
    #[serde(default)]
    pub(crate) resolve_hashes: bool,
}

fn default_sub_packages() -> Vec<String> {
    vec!["./...".to_owned()]
}
fn default_dot() -> String {
    ".".to_owned()
}

/// Query `go env GOMODCACHE` to find the module cache directory.
pub(crate) fn find_gomodcache(go_bin: &str) -> Result<std::path::PathBuf> {
    // Check environment first (same var Go checks).
    if let Ok(val) = std::env::var("GOMODCACHE") {
        if !val.is_empty() {
            return Ok(std::path::PathBuf::from(val));
        }
    }

    let output = std::process::Command::new(go_bin)
        .args(["env", "GOMODCACHE"])
        .output()
        .with_context(|| format!("running '{go_bin} env GOMODCACHE'"))?;

    if !output.status.success() {
        bail!(
            "'go env GOMODCACHE' failed: {}",
            String::from_utf8_lossy(&output.stderr)
        );
    }

    let path = String::from_utf8(output.stdout)
        .context("GOMODCACHE is not valid UTF-8")?
        .trim()
        .to_owned();

    if path.is_empty() {
        bail!("GOMODCACHE is empty");
    }

    Ok(std::path::PathBuf::from(path))
}

/// Run both go list passes and return the complete package graph.
///
/// The first pass (`go list -deps`) discovers build-time packages.
/// When `do_check` is set and local packages exist, a second pass
/// (`go list -deps -test`) discovers test-only third-party dependencies.
pub(crate) fn resolve_packages(input: &JsonInput) -> Result<PackageGraph> {
    let go_bin = input
        .go
        .as_deref()
        .or(DEFAULT_GO)
        .ok_or_else(|| anyhow!("resolveGoPackages: 'go' not provided and GO2NIX_DEFAULT_GO was unset at plugin build time"))?;

    let opts = GoListOpts {
        tags: &input.tags,
        mod_root: &input.mod_root,
        goos: &input.goos,
        goarch: &input.goarch,
        go_proxy: input.go_proxy.as_deref(),
        cgo_enabled: &input.cgo_enabled,
    };

    let stdout = run_go_list(go_bin, &input.src, &input.sub_packages, &opts)?;
    let mut graph = parse_go_packages(&stdout)?;

    if input.do_check && !graph.local_packages.is_empty() {
        let local_ips: Vec<String> = graph
            .local_packages
            .iter()
            .map(|p| p.import_path.clone())
            .collect();

        let test_stdout = run_go_list_test(go_bin, &input.src, &local_ips, &opts)?;

        let tp = parse_test_packages(
            &test_stdout,
            &graph.third_party_paths,
            &graph.local_paths,
            &mut graph.replacements,
        )?;
        graph.test_only_paths = tp
            .test_packages
            .iter()
            .map(|p| p.import_path.clone())
            .collect();
        graph.test_packages = tp.test_packages;
        graph.test_local_packages = tp.test_local_packages;
        for lp in &mut graph.local_packages {
            if let Some(extra) = tp.local_test_embed_files.get(&lp.import_path) {
                lp.main_src_files.extend(extra.iter().cloned());
                lp.main_src_files.sort();
                lp.main_src_files.dedup();
            }
        }
    }

    Ok(graph)
}

// ---------------------------------------------------------------------------
// Serializable output types
// ---------------------------------------------------------------------------

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct JsonLocalPkg {
    dir: String,
    local_imports: Vec<String>,
    third_party_imports: Vec<String>,
    #[serde(skip_serializing_if = "std::ops::Not::not")]
    is_cgo: bool,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    cgo_pkg_config: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    cgo_cflags: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    cgo_ldflags: Vec<String>,
    #[serde(skip_serializing_if = "PkgFiles::is_empty")]
    files: PkgFiles,
    /// Src-relative paths the precise mainSrc filter must include for this
    /// package: every compiled source kind, *_test.go, and resolved
    /// //go:embed targets (build + test + xtest). Dir-prefixed here so the
    /// Nix-side filter can union these into one attrset across all packages.
    main_src_files: Vec<String>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct JsonOutput {
    packages: BTreeMap<String, JsonPkg>,
    local_packages: BTreeMap<String, JsonLocalPkg>,
    module_path: String,
    go_version: String,
    replacements: BTreeMap<String, JsonReplacement>,
    test_packages: BTreeMap<String, JsonPkg>,
    /// Local packages reachable only from `*_test.go` imports. Same shape as
    /// `local_packages`; the dag builder unions them in when `doCheck` so the
    /// testrunner has `.a` archives for testutil-style helpers. Omitted when
    /// empty so callers without test-only locals see an unchanged JSON shape.
    #[serde(skip_serializing_if = "BTreeMap::is_empty")]
    test_local_packages: BTreeMap<String, JsonLocalPkg>,
    /// NAR hashes for modules, keyed by "path@version".
    /// Only populated when resolveHashes is true.
    #[serde(skip_serializing_if = "BTreeMap::is_empty")]
    module_hashes: BTreeMap<String, String>,
    /// Precomputed per-subPackage import closure (modKeys + cxx).
    /// Replaces the Nix-side `genericClosure` walk.
    sub_package_closures: BTreeMap<String, JsonClosure>,
    /// Transitive local-replace target dirs (src-relative, normalized).
    /// Replaces the Nix-side go.mod readFile + regex walk.
    local_replace_dirs: Vec<String>,
    /// All src-relative directories containing a go.mod. Replaces per-filter
    /// `pathExists (path + "/go.mod")` syscalls.
    nested_module_roots: Vec<String>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct JsonPkg {
    drv_name: String,
    imports: Vec<String>,
    mod_key: String,
    subdir: String,
    #[serde(skip_serializing_if = "std::ops::Not::not")]
    is_cgo: bool,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    cgo_pkg_config: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    cgo_cflags: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    cgo_ldflags: Vec<String>,
    #[serde(skip_serializing_if = "PkgFiles::is_empty")]
    files: PkgFiles,
}

#[derive(Serialize)]
struct JsonReplacement {
    path: String,
    version: String,
}

/// Per-subPackage transitive closure summary so `nix/dag` can skip its
/// `genericClosure` walk: the modKey set (for modinfo `dep` lines) and the
/// CXX flag (for `-extld`).
#[derive(Serialize, Debug, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
struct JsonClosure {
    mod_keys: Vec<String>,
    cxx: bool,
}

/// Convert a `PkgData` to a `JsonPkg`, filtering imports to only include
/// packages present in `allowed_imports`.
fn pkg_data_to_json_pkg(p: &PkgData, allowed_imports: &dyn Fn(&str) -> bool) -> JsonPkg {
    let effective_version = if p.replace_version.is_empty() {
        &p.mod_version
    } else {
        &p.replace_version
    };
    let mod_key = format!("{}@{effective_version}", p.mod_path);
    let subdir = if p.import_path != p.mod_path {
        let prefix = format!("{}/", p.mod_path);
        p.import_path.strip_prefix(&prefix).unwrap_or("").to_owned()
    } else {
        String::new()
    };
    let filtered_imports: Vec<String> = p
        .imports
        .iter()
        .filter(|imp| allowed_imports(imp))
        .cloned()
        .collect();
    let drv_name = format!("gopkg-{}-{}", sanitize_name(&p.import_path), p.mod_version);

    JsonPkg {
        drv_name,
        imports: filtered_imports,
        mod_key,
        subdir,
        is_cgo: p.is_cgo,
        cgo_pkg_config: p.cgo_pkg_config.clone(),
        cgo_cflags: p.cgo_cflags.clone(),
        cgo_ldflags: p.cgo_ldflags.clone(),
        files: p.files.clone(),
    }
}

/// Map `["./cmd/foo", "."]` → import paths under `module_path`, matching
/// dag.nix's `spImportPath`.
fn sub_package_import_path(module_path: &str, sp: &str) -> String {
    let clean = sp.strip_prefix("./").unwrap_or(sp);
    if sp == "." || clean.is_empty() {
        module_path.to_owned()
    } else {
        format!("{module_path}/{clean}")
    }
}

/// BFS the import graph from each subPackage's import path, collecting the
/// set of third-party modKeys and whether any reached package has C++/SWIG-C++
/// sources. Mirrors dag.nix's `closureOf` + `modKeysOf` + `hasCxx`.
fn compute_sub_package_closures(
    graph: &PackageGraph,
    sub_packages: &[String],
) -> BTreeMap<String, JsonClosure> {
    // Index third-party packages by import path → (modKey, hasCxx, &imports).
    let mod_key_of = |p: &PkgData| -> String {
        let v = if p.replace_version.is_empty() {
            &p.mod_version
        } else {
            &p.replace_version
        };
        format!("{}@{v}", p.mod_path)
    };
    let has_cxx = |f: &PkgFiles| !f.cxx_files.is_empty() || !f.swig_cxx_files.is_empty();

    let tp_index: BTreeMap<&str, (String, bool, &[String])> = graph
        .packages
        .iter()
        .map(|p| {
            (
                p.import_path.as_str(),
                (mod_key_of(p), has_cxx(&p.files), p.imports.as_slice()),
            )
        })
        .collect();
    let local_index: BTreeMap<&str, (&LocalPkgData, bool)> = graph
        .local_packages
        .iter()
        .chain(graph.test_local_packages.iter())
        .map(|p| (p.import_path.as_str(), (p, has_cxx(&p.files))))
        .collect();

    let mut out = BTreeMap::new();
    for sp in sub_packages {
        let root = sub_package_import_path(&graph.module_path, sp);
        let mut visited: BTreeSet<&str> = BTreeSet::new();
        let mut queue: VecDeque<&str> = VecDeque::new();
        let mut mod_keys: BTreeSet<String> = BTreeSet::new();
        let mut cxx = false;

        queue.push_back(root.as_str());
        while let Some(ip) = queue.pop_front() {
            if !visited.insert(ip) {
                continue;
            }
            if let Some((lp, lcxx)) = local_index.get(ip) {
                cxx |= lcxx;
                for imp in lp.local_imports.iter().chain(lp.third_party_imports.iter()) {
                    queue.push_back(imp.as_str());
                }
            } else if let Some((mk, tcxx, imports)) = tp_index.get(ip) {
                cxx |= tcxx;
                mod_keys.insert(mk.clone());
                for imp in imports.iter() {
                    queue.push_back(imp.as_str());
                }
            }
        }

        out.insert(
            root,
            JsonClosure {
                mod_keys: mod_keys.into_iter().collect(),
                cxx,
            },
        );
    }
    out
}

/// Normalize `a/b/../c` → `a/c` in pure string space (mirrors dag.nix's
/// `normalizeRelPath`).
fn normalize_rel(p: &str) -> String {
    let mut out: Vec<&str> = Vec::new();
    for seg in p.split('/') {
        match seg {
            "" | "." => {}
            ".." => match out.last() {
                Some(&last) if last != ".." => {
                    out.pop();
                }
                _ => out.push(".."),
            },
            s => out.push(s),
        }
    }
    if out.is_empty() {
        ".".to_owned()
    } else {
        out.join("/")
    }
}

/// Transitively walk local-replace directives (`=> ./X` / `=> ../X`) starting
/// from `mod_root`, returning normalized src-relative target dirs (excluding
/// `mod_root` itself, `"."`, and any that escape `src` via `..`).
/// Mirrors dag.nix's `replaceDirsOf` + the post-filter.
fn walk_local_replace_dirs(src: &Path, mod_root: &str) -> Vec<String> {
    fn parse_local_replaces(text: &str) -> Vec<String> {
        // `=>` only appears in `replace` directives; a local target is one
        // starting with `./` or `../`. Same condition as helpers.nix.
        text.lines()
            .filter_map(|l| {
                let i = l.find("=>")?;
                let tgt = l[i + 2..].split_whitespace().next()?;
                if tgt.starts_with("./") || tgt.starts_with("../") {
                    Some(tgt.to_owned())
                } else {
                    None
                }
            })
            .collect()
    }

    let clean_mod_root = normalize_rel(mod_root);
    let mut visited: BTreeSet<String> = BTreeSet::new();
    let mut queue: VecDeque<String> = VecDeque::new();
    queue.push_back(clean_mod_root.clone());
    let mut out: Vec<String> = Vec::new();

    while let Some(dir) = queue.pop_front() {
        if !visited.insert(dir.clone()) {
            continue;
        }
        if dir.starts_with("..") {
            continue;
        }
        let go_mod = if dir == "." {
            src.join("go.mod")
        } else {
            src.join(&dir).join("go.mod")
        };
        let Ok(text) = std::fs::read_to_string(&go_mod) else {
            continue;
        };
        for r in parse_local_replaces(&text) {
            let next = normalize_rel(&format!("{dir}/{r}"));
            if visited.contains(&next) {
                continue;
            }
            if next != clean_mod_root && next != "." && !next.starts_with("..") {
                out.push(next.clone());
            }
            queue.push_back(next);
        }
    }
    // Dedupe + sort for determinism.
    out.sort();
    out.dedup();
    out
}

/// Walk under each `start` directory (src-relative) for go.mod-bearing
/// subdirectories; return all of them src-relative. Seeds are modRoot plus
/// the local-replace targets — the only subtrees the dag-side `mainSrc` /
/// `pkgSrc` filters ever descend into — so a monorepo `src` with
/// `modRoot != "."` does no I/O outside the relevant module roots.
///
/// Descent stops at the first go.mod *strictly below* a seed (matches the
/// `builtins.path` filter, which rejects a nested-module dir and so never
/// recurses into it). Seeds themselves always have a go.mod and are
/// included; the dag-side mainSrc filter exempts allowedDirs explicitly.
fn find_nested_module_roots(src: &Path, starts: &[String]) -> Vec<String> {
    fn rel_of(src: &Path, p: &Path) -> String {
        let r = p.strip_prefix(src).unwrap_or(p);
        let mut s = String::new();
        for c in r.components() {
            if let Component::Normal(seg) = c {
                if !s.is_empty() {
                    s.push('/');
                }
                s.push_str(&seg.to_string_lossy());
            }
        }
        if s.is_empty() {
            ".".to_owned()
        } else {
            s
        }
    }

    let seed_set: BTreeSet<PathBuf> = starts
        .iter()
        .map(|s| if s == "." { src.to_path_buf() } else { src.join(s) })
        .collect();

    let mut out: BTreeSet<String> = BTreeSet::new();
    let mut stack: Vec<PathBuf> = seed_set.iter().cloned().collect();
    while let Some(dir) = stack.pop() {
        let has_gomod = dir.join("go.mod").is_file();
        if has_gomod {
            out.insert(rel_of(src, &dir));
            // Nested boundary below a seed: stop here, matching the
            // builtins.path filter's no-descend-on-reject behaviour.
            if !seed_set.contains(&dir) {
                continue;
            }
        }
        let Ok(rd) = std::fs::read_dir(&dir) else {
            continue;
        };
        for entry in rd.flatten() {
            let Ok(ft) = entry.file_type() else { continue };
            if !ft.is_dir() {
                continue;
            }
            let name = entry.file_name();
            let name = name.to_string_lossy();
            // Skip hidden + Go-ignored testdata/vendor.
            if name.starts_with('.') || name == "testdata" || name == "vendor" {
                continue;
            }
            stack.push(entry.path());
        }
    }
    out.into_iter().collect()
}

/// Convert a `PackageGraph` to a JSON string.
pub(crate) fn package_graph_to_json(
    graph: &PackageGraph,
    input: &JsonInput,
    module_hashes: BTreeMap<String, String>,
) -> Result<String> {
    let src_dir = input.src.as_str();
    let canon_src = std::fs::canonicalize(src_dir)
        .with_context(|| format!("failed to canonicalize src dir: {src_dir}"))?;

    // Build packages map.
    let packages: BTreeMap<String, JsonPkg> = graph
        .packages
        .iter()
        .map(|p| {
            let json_pkg = pkg_data_to_json_pkg(p, &|imp| graph.third_party_paths.contains(imp));
            (p.import_path.clone(), json_pkg)
        })
        .collect();

    let to_json_local = |lp: &LocalPkgData| -> Result<(String, JsonLocalPkg)> {
        let canon_dir = std::fs::canonicalize(&lp.dir)
            .with_context(|| format!("failed to canonicalize local package dir: {}", lp.dir))?;
        let rel = canon_dir.strip_prefix(&canon_src).with_context(|| {
            format!(
                "local package dir {} escapes source tree {}",
                canon_dir.display(),
                canon_src.display()
            )
        })?;
        let rel_str = rel.to_string_lossy();
        let dir = if rel_str.is_empty() {
            ".".to_owned()
        } else {
            rel_str.into_owned()
        };
        let main_src_files = lp
            .main_src_files
            .iter()
            .map(|f| if dir == "." { f.clone() } else { format!("{dir}/{f}") })
            .collect();
        Ok((
            lp.import_path.clone(),
            JsonLocalPkg {
                dir,
                local_imports: lp.local_imports.clone(),
                third_party_imports: lp.third_party_imports.clone(),
                is_cgo: lp.is_cgo,
                cgo_pkg_config: lp.cgo_pkg_config.clone(),
                cgo_cflags: lp.cgo_cflags.clone(),
                cgo_ldflags: lp.cgo_ldflags.clone(),
                files: lp.files.clone(),
                main_src_files,
            },
        ))
    };

    let local_packages: BTreeMap<String, JsonLocalPkg> = graph
        .local_packages
        .iter()
        .map(to_json_local)
        .collect::<Result<_>>()?;
    let test_local_packages: BTreeMap<String, JsonLocalPkg> = graph
        .test_local_packages
        .iter()
        .map(to_json_local)
        .collect::<Result<_>>()?;

    let replacements = graph
        .replacements
        .iter()
        .map(|(k, (path, version))| {
            (
                k.clone(),
                JsonReplacement {
                    path: path.clone(),
                    version: version.clone(),
                },
            )
        })
        .collect();

    // Re-key module_hashes by modKey (origPath@effectiveVersion). go.sum lists
    // modules under their *fetch* path, so for a fork replace `foo => fork v2`
    // it contains `fork@v2` while the per-package modKey is `foo@v2`. Insert an
    // alias so nix/dag's `moduleInfo.${pkg.modKey}` lookup hits. The original
    // entry is left in place — Nix evaluates lazily, and same-path replaces
    // (modKey == fetch key) make this a no-op.
    let mut module_hashes = module_hashes;
    for (mod_key, (repl_path, repl_ver)) in &graph.replacements {
        if repl_ver.is_empty() {
            continue; // local replace — no fetch, no go.sum entry
        }
        let fetch_key = format!("{repl_path}@{repl_ver}");
        if fetch_key == *mod_key {
            continue;
        }
        if let Some(h) = module_hashes.get(&fetch_key).cloned() {
            module_hashes.insert(mod_key.clone(), h);
        }
    }

    // Build test_packages map.
    let test_packages: BTreeMap<String, JsonPkg> = graph
        .test_packages
        .iter()
        .map(|p| {
            let json_pkg = pkg_data_to_json_pkg(p, &|imp| {
                graph.third_party_paths.contains(imp) || graph.test_only_paths.contains(imp)
            });
            (p.import_path.clone(), json_pkg)
        })
        .collect();

    let sub_package_closures = compute_sub_package_closures(graph, &input.sub_packages);
    let local_replace_dirs = walk_local_replace_dirs(&canon_src, &input.mod_root);
    let nested_starts: Vec<String> = std::iter::once(normalize_rel(&input.mod_root))
        .chain(local_replace_dirs.iter().cloned())
        .collect();
    let nested_module_roots = find_nested_module_roots(&canon_src, &nested_starts);

    let output = JsonOutput {
        packages,
        local_packages,
        module_path: graph.module_path.clone(),
        go_version: graph.go_version.clone(),
        replacements,
        test_packages,
        test_local_packages,
        module_hashes,
        sub_package_closures,
        local_replace_dirs,
        nested_module_roots,
    };

    serde_json::to_string(&output).context("failed to serialize output JSON")
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_main_src_files_from() {
        let p = GoPackage {
            go_files: vec!["a.go".into()],
            cgo_files: vec!["c.go".into()],
            h_files: vec!["c.h".into()],
            embed_files: vec!["templates/index.html".into()],
            test_go_files: vec!["a_test.go".into()],
            x_test_go_files: vec!["x_test.go".into()],
            test_embed_files: vec!["fixture.bin".into()],
            // patterns are NOT included — only resolved files matter for mainSrc
            embed_patterns: vec!["templates/*".into()],
            ..Default::default()
        };
        let got = main_src_files_from(&p);
        assert_eq!(
            got,
            vec![
                "a.go",
                "a_test.go",
                "c.go",
                "c.h",
                "fixture.bin",
                "templates/index.html",
                "x_test.go",
            ]
        );
    }

    /// Minimal go list JSON for a third-party package.
    fn third_party_json(import_path: &str, mod_path: &str, version: &str) -> String {
        format!(
            r#"{{"ImportPath":"{import_path}","Module":{{"Path":"{mod_path}","Version":"{version}"}},"Imports":[]}}"#
        )
    }

    /// go list JSON for a main-module (local) package with Dir and Imports.
    fn local_pkg_json(import_path: &str, mod_path: &str, dir: &str, imports: &[&str]) -> String {
        let imp_json: Vec<String> = imports.iter().map(|i| format!("\"{i}\"")).collect();
        format!(
            r#"{{"ImportPath":"{import_path}","Dir":"{dir}","Module":{{"Path":"{mod_path}","Main":true}},"Imports":[{}]}}"#,
            imp_json.join(",")
        )
    }

    /// go list JSON for a stdlib package (no Module).
    fn stdlib_json(import_path: &str) -> String {
        format!(r#"{{"ImportPath":"{import_path}","Imports":[]}}"#)
    }

    /// go list JSON for a package with an error.
    fn error_json(import_path: &str, err: &str) -> String {
        format!(r#"{{"ImportPath":"{import_path}","Error":{{"Err":"{err}"}}}}"#)
    }

    /// go list JSON for a replaced module.
    fn replaced_json(
        import_path: &str,
        mod_path: &str,
        mod_version: &str,
        replace_path: &str,
        replace_version: &str,
    ) -> String {
        format!(
            r#"{{"ImportPath":"{import_path}","Module":{{"Path":"{mod_path}","Version":"{mod_version}","Replace":{{"Path":"{replace_path}","Version":"{replace_version}"}}}},"Imports":[]}}"#
        )
    }

    // --- parse_go_packages ---

    #[test]
    fn parse_skips_stdlib() {
        let input = stdlib_json("fmt");
        let graph = parse_go_packages(input.as_bytes()).unwrap();
        assert!(graph.packages.is_empty());
        assert!(graph.local_packages.is_empty());
    }

    #[test]
    fn parse_collects_third_party() {
        let input = third_party_json("github.com/foo/bar", "github.com/foo/bar", "v1.0.0");
        let graph = parse_go_packages(input.as_bytes()).unwrap();
        assert_eq!(graph.packages.len(), 1);
        assert_eq!(graph.packages[0].import_path, "github.com/foo/bar");
        assert_eq!(graph.packages[0].mod_version, "v1.0.0");
        assert!(graph.third_party_paths.contains("github.com/foo/bar"));
    }

    #[test]
    fn parse_collects_local_with_dir() {
        let input = local_pkg_json(
            "example.com/mymod/internal/db",
            "example.com/mymod",
            "/src/internal/db",
            &[],
        );
        let graph = parse_go_packages(input.as_bytes()).unwrap();
        assert!(graph.packages.is_empty());
        assert_eq!(graph.local_packages.len(), 1);
        assert_eq!(
            graph.local_packages[0].import_path,
            "example.com/mymod/internal/db"
        );
        assert_eq!(graph.local_packages[0].dir, "/src/internal/db");
        assert_eq!(graph.module_path, "example.com/mymod");
    }

    #[test]
    fn parse_classifies_local_imports() {
        // Two local packages + one third-party. The second local imports both.
        let input = [
            local_pkg_json("example.com/m/a", "example.com/m", "/src/a", &[]),
            third_party_json("github.com/dep/x", "github.com/dep/x", "v2.0.0"),
            local_pkg_json(
                "example.com/m/b",
                "example.com/m",
                "/src/b",
                &["example.com/m/a", "github.com/dep/x", "fmt"],
            ),
        ]
        .join("\n");

        let graph = parse_go_packages(input.as_bytes()).unwrap();
        assert_eq!(graph.local_packages.len(), 2);

        let pkg_b = &graph.local_packages[1];
        assert_eq!(pkg_b.local_imports, vec!["example.com/m/a"]);
        assert_eq!(pkg_b.third_party_imports, vec!["github.com/dep/x"]);
    }

    #[test]
    fn parse_captures_module_path_from_first_main() {
        let input = [
            local_pkg_json("example.com/mymod", "example.com/mymod", "/src", &[]),
            local_pkg_json(
                "example.com/mymod/sub",
                "example.com/mymod",
                "/src/sub",
                &[],
            ),
        ]
        .join("\n");

        let graph = parse_go_packages(input.as_bytes()).unwrap();
        assert_eq!(graph.module_path, "example.com/mymod");
    }

    #[test]
    fn parse_collects_replacements() {
        let input = replaced_json(
            "github.com/old/pkg",
            "github.com/old/pkg",
            "v1.0.0",
            "github.com/new/pkg",
            "v2.0.0",
        );
        let graph = parse_go_packages(input.as_bytes()).unwrap();
        assert_eq!(graph.packages.len(), 1);
        let (path, version) = &graph.replacements["github.com/old/pkg@v2.0.0"];
        assert_eq!(path, "github.com/new/pkg");
        assert_eq!(version, "v2.0.0");
    }

    #[test]
    fn module_hashes_rekeyed_by_mod_key_for_fork_replace() {
        // Fork replace: old/pkg => new/pkg v2.0.0. go.sum (and thus the
        // incoming module_hashes map) keys this as new/pkg@v2.0.0, but the
        // per-package modKey is old/pkg@v2.0.0. The JSON output must alias
        // the hash under the modKey so nix/dag's moduleInfo lookup hits.
        let input = replaced_json(
            "github.com/old/pkg",
            "github.com/old/pkg",
            "v1.0.0",
            "github.com/new/pkg",
            "v2.0.0",
        );
        let graph = parse_go_packages(input.as_bytes()).unwrap();

        let mut hashes = BTreeMap::new();
        hashes.insert(
            "github.com/new/pkg@v2.0.0".to_owned(),
            "sha256-fork".to_owned(),
        );

        let src = tempfile::tempdir().unwrap();
        let input = JsonInput {
            go: None,
            src: src.path().to_string_lossy().into_owned(),
            do_check: false,
            tags: vec![],
            sub_packages: vec![".".into()],
            mod_root: ".".into(),
            goos: String::new(),
            goarch: String::new(),
            go_proxy: None,
            cgo_enabled: String::new(),
            resolve_hashes: false,
        };
        let json = package_graph_to_json(&graph, &input, hashes).unwrap();
        let v: serde_json::Value = serde_json::from_str(&json).unwrap();

        let mod_hashes = &v["moduleHashes"];
        assert_eq!(
            mod_hashes["github.com/old/pkg@v2.0.0"], "sha256-fork",
            "hash must be aliased under origPath@effectiveVersion"
        );
        // Original go.sum key is still present (harmless under lazy eval).
        assert_eq!(mod_hashes["github.com/new/pkg@v2.0.0"], "sha256-fork");
        // And the package's modKey matches the alias.
        assert_eq!(
            v["packages"]["github.com/old/pkg"]["modKey"],
            "github.com/old/pkg@v2.0.0"
        );
    }

    #[test]
    fn parse_errors_are_aggregated() {
        let input = [
            error_json("github.com/bad/a", "missing module"),
            error_json("github.com/bad/b", "version mismatch"),
        ]
        .join("\n");

        let result = parse_go_packages(input.as_bytes());
        let err = result.err().expect("should have errored");
        let msg = format!("{err}");
        assert!(msg.contains("github.com/bad/a: missing module"));
        assert!(msg.contains("github.com/bad/b: version mismatch"));
    }

    #[test]
    fn parse_cgo_fields() {
        let input = r#"{"ImportPath":"github.com/cgo/pkg","Module":{"Path":"github.com/cgo/pkg","Version":"v1.0.0"},"Imports":[],"CgoFiles":["bridge.go"],"CgoPkgConfig":["libfoo"],"CgoCFLAGS":["-I/usr/include"],"CgoLDFLAGS":["-lfoo"]}"#;
        let graph = parse_go_packages(input.as_bytes()).unwrap();
        assert!(graph.packages[0].is_cgo);
        assert_eq!(graph.packages[0].cgo_pkg_config, vec!["libfoo"]);
        assert_eq!(graph.packages[0].cgo_cflags, vec!["-I/usr/include"]);
        assert_eq!(graph.packages[0].cgo_ldflags, vec!["-lfoo"]);
        assert_eq!(graph.packages[0].files.cgo_files, vec!["bridge.go"]);
    }

    #[test]
    fn parse_file_lists_round_trip_to_json() {
        let input = r#"{"ImportPath":"github.com/f/p","Module":{"Path":"github.com/f/p","Version":"v1.0.0"},"Imports":[],"GoFiles":["a.go","b.go"],"CgoFiles":["c.go"],"SFiles":["asm.s"],"CFiles":["x.c"],"CXXFiles":["x.cc"],"MFiles":["x.m"],"FFiles":["x.f90"],"HFiles":["x.h"],"SysoFiles":["x.syso"],"SwigFiles":["x.swig"],"SwigCXXFiles":["x.swigcxx"],"EmbedPatterns":["data/*"]}"#;
        let graph = parse_go_packages(input.as_bytes()).unwrap();
        let f = &graph.packages[0].files;
        assert_eq!(f.go_files, vec!["a.go", "b.go"]);
        assert_eq!(f.cgo_files, vec!["c.go"]);
        assert_eq!(f.s_files, vec!["asm.s"]);
        assert_eq!(f.c_files, vec!["x.c"]);
        assert_eq!(f.cxx_files, vec!["x.cc"]);
        assert_eq!(f.m_files, vec!["x.m"]);
        assert_eq!(f.f_files, vec!["x.f90"]);
        assert_eq!(f.h_files, vec!["x.h"]);
        assert_eq!(f.syso_files, vec!["x.syso"]);
        assert_eq!(f.swig_files, vec!["x.swig"]);
        assert_eq!(f.swig_cxx_files, vec!["x.swigcxx"]);
        assert_eq!(f.embed_patterns, vec!["data/*"]);

        let jp = pkg_data_to_json_pkg(&graph.packages[0], &|_| true);
        let json = serde_json::to_value(&jp).unwrap();
        let files = &json["files"];
        assert_eq!(files["goFiles"], serde_json::json!(["a.go", "b.go"]));
        assert_eq!(files["cgoFiles"], serde_json::json!(["c.go"]));
        assert_eq!(files["sFiles"], serde_json::json!(["asm.s"]));
        assert_eq!(files["cFiles"], serde_json::json!(["x.c"]));
        assert_eq!(files["cxxFiles"], serde_json::json!(["x.cc"]));
        assert_eq!(files["mFiles"], serde_json::json!(["x.m"]));
        assert_eq!(files["fFiles"], serde_json::json!(["x.f90"]));
        assert_eq!(files["hFiles"], serde_json::json!(["x.h"]));
        assert_eq!(files["sysoFiles"], serde_json::json!(["x.syso"]));
        assert_eq!(files["swigFiles"], serde_json::json!(["x.swig"]));
        assert_eq!(files["swigCxxFiles"], serde_json::json!(["x.swigcxx"]));
        assert_eq!(files["embedPatterns"], serde_json::json!(["data/*"]));
    }

    #[test]
    fn json_pkg_omits_empty_files() {
        let p = PkgData {
            import_path: "github.com/foo/bar".into(),
            mod_path: "github.com/foo/bar".into(),
            mod_version: "v1.0.0".into(),
            replace_version: String::new(),
            imports: vec![],
            cgo_pkg_config: vec![],
            cgo_cflags: vec![],
            cgo_ldflags: vec![],
            is_cgo: false,
            files: PkgFiles::default(),
        };
        let jp = pkg_data_to_json_pkg(&p, &|_| true);
        let json = serde_json::to_value(&jp).unwrap();
        assert!(
            json.get("files").is_none(),
            "empty files should be omitted, got {json}"
        );
    }

    #[test]
    fn parse_local_file_lists() {
        let input = r#"{"ImportPath":"example.com/m","Dir":"/src","Module":{"Path":"example.com/m","Main":true},"Imports":[],"GoFiles":["main.go"],"SFiles":["asm_amd64.s"]}"#;
        let graph = parse_go_packages(input.as_bytes()).unwrap();
        assert_eq!(graph.local_packages.len(), 1);
        let f = &graph.local_packages[0].files;
        assert_eq!(f.go_files, vec!["main.go"]);
        assert_eq!(f.s_files, vec!["asm_amd64.s"]);
    }

    // --- parse_test_packages ---

    fn ptp(
        input: &str,
        third_party: &BTreeSet<String>,
        locals: &BTreeSet<String>,
    ) -> TestPassResult {
        let mut replacements = BTreeMap::new();
        parse_test_packages(input.as_bytes(), third_party, locals, &mut replacements).unwrap()
    }

    #[test]
    fn test_parse_skips_build_graph_packages() {
        let mut third_party = BTreeSet::new();
        third_party.insert("github.com/already/known".to_owned());

        let input = third_party_json(
            "github.com/already/known",
            "github.com/already/known",
            "v1.0.0",
        );
        let r = ptp(&input, &third_party, &BTreeSet::new());
        assert!(r.test_packages.is_empty());
        assert!(r.test_local_packages.is_empty());
    }

    #[test]
    fn test_parse_collects_test_only() {
        let input = third_party_json("github.com/testify", "github.com/testify", "v1.9.0");
        let r = ptp(&input, &BTreeSet::new(), &BTreeSet::new());
        assert_eq!(r.test_packages.len(), 1);
        assert_eq!(r.test_packages[0].import_path, "github.com/testify");
    }

    #[test]
    fn test_parse_skips_synthetic_packages() {
        let input = [
            // Synthetic test main
            third_party_json("example.com/pkg.test", "example.com/pkg", ""),
            // Recompiled variant
            format!(r#"{{"ImportPath":"example.com/pkg [example.com/pkg.test]","Module":{{"Path":"example.com/pkg","Main":true}},"Imports":[]}}"#),
            // Real test-only dep
            third_party_json("github.com/testify", "github.com/testify", "v1.9.0"),
        ]
        .join("\n");

        let r = ptp(&input, &BTreeSet::new(), &BTreeSet::new());
        assert_eq!(r.test_packages.len(), 1);
        assert_eq!(r.test_packages[0].import_path, "github.com/testify");
        assert!(r.test_local_packages.is_empty());
    }

    #[test]
    fn test_parse_collects_test_only_local() {
        let mut locals = BTreeSet::new();
        locals.insert("example.com/m/internal/app".to_owned());
        let input = [
            // Already in build graph: skipped.
            local_pkg_json(
                "example.com/m/internal/app",
                "example.com/m",
                "/src/app",
                &[],
            ),
            // Test-only local importing build-graph local + test-only third-party.
            local_pkg_json(
                "example.com/m/internal/testutil",
                "example.com/m",
                "/src/testutil",
                &["example.com/m/internal/app", "github.com/testify", "fmt"],
            ),
            // Test-only third-party.
            third_party_json("github.com/testify", "github.com/testify", "v1.9.0"),
        ]
        .join("\n");

        let r = ptp(&input, &BTreeSet::new(), &locals);
        assert_eq!(r.test_packages.len(), 1);
        assert_eq!(r.test_local_packages.len(), 1);
        let lp = &r.test_local_packages[0];
        assert_eq!(lp.import_path, "example.com/m/internal/testutil");
        assert_eq!(lp.dir, "/src/testutil");
        assert_eq!(lp.local_imports, vec!["example.com/m/internal/app"]);
        assert_eq!(lp.third_party_imports, vec!["github.com/testify"]);
    }

    #[test]
    fn test_parse_deduplicates() {
        let input = [
            third_party_json("github.com/dup", "github.com/dup", "v1.0.0"),
            third_party_json("github.com/dup", "github.com/dup", "v1.0.0"),
            local_pkg_json("example.com/m/x", "example.com/m", "/src/x", &[]),
            local_pkg_json("example.com/m/x", "example.com/m", "/src/x", &[]),
        ]
        .join("\n");
        let r = ptp(&input, &BTreeSet::new(), &BTreeSet::new());
        assert_eq!(r.test_packages.len(), 1);
        assert_eq!(r.test_local_packages.len(), 1);
    }

    #[test]
    fn test_parse_collects_test_replacements() {
        let input = replaced_json(
            "github.com/test/dep",
            "github.com/test/dep",
            "v1.0.0",
            "github.com/fork/dep",
            "v1.1.0",
        );
        let mut replacements = BTreeMap::new();
        let r = parse_test_packages(
            input.as_bytes(),
            &BTreeSet::new(),
            &BTreeSet::new(),
            &mut replacements,
        )
        .unwrap();
        assert_eq!(r.test_packages.len(), 1);
        let (path, _) = &replacements["github.com/test/dep@v1.1.0"];
        assert_eq!(path, "github.com/fork/dep");
    }

    // --- extract_replace ---

    #[test]
    fn extract_replace_none() {
        let m = GoModule {
            path: "foo".into(),
            version: "v1".into(),
            ..Default::default()
        };
        let (p, v) = extract_replace(&m);
        assert!(p.is_empty());
        assert!(v.is_empty());
    }

    #[test]
    fn extract_replace_some() {
        let m = GoModule {
            path: "foo".into(),
            version: "v1".into(),
            replace: Some(Box::new(GoModule {
                path: "bar".into(),
                version: "v2".into(),
                ..Default::default()
            })),
            ..Default::default()
        };
        let (p, v) = extract_replace(&m);
        assert_eq!(p, "bar");
        assert_eq!(v, "v2");
    }

    // --- lang_version / goVersion threading ---

    #[test]
    fn lang_version_strips_patch() {
        assert_eq!(lang_version("1.21.3"), "1.21");
        assert_eq!(lang_version("1.21"), "1.21");
        assert_eq!(lang_version("1.22rc1"), "1.22rc1");
        assert_eq!(lang_version(""), "");
    }

    #[test]
    fn parse_captures_main_module_go_version() {
        let input = r#"{"ImportPath":"example.com/m","Dir":"/src","Module":{"Path":"example.com/m","Main":true,"GoVersion":"1.21.3"},"Imports":[],"GoFiles":["main.go"]}"#;
        let graph = parse_go_packages(input.as_bytes()).unwrap();
        assert_eq!(graph.go_version, "1.21");
    }

    // --- sanitize_name ---

    #[test]
    fn sanitize_name_whitelist() {
        assert_eq!(
            sanitize_name("github.com/foo/bar+baz"),
            "github.com-foo-bar+baz"
        );
        assert_eq!(
            sanitize_name("git.sr.ht/~geb/dotool"),
            "git.sr.ht-_geb-dotool"
        );
        assert_eq!(
            sanitize_name("example.com/@scope/pkg"),
            "example.com-_at_scope-pkg"
        );
    }

    #[test]
    fn sanitize_name_length_cap() {
        let long = format!("example.com/{}", "a".repeat(288));
        assert_eq!(long.len(), 300);
        let got = sanitize_name(&long);
        assert_eq!(got.len(), MAX_SANITIZED_LEN);
        assert_eq!(got, sanitize_name(&long), "deterministic");
        // Cross-implementation parity: Go (pkg/nixdrv) and Nix (helpers.nix)
        // must produce this exact string for the same input.
        assert_eq!(got, format!("example.com-{}-2d904ea3", "a".repeat(139)));
        let other = format!("example.com/{}", "b".repeat(288));
        assert_ne!(sanitize_name(&other), got, "distinct inputs must differ");
    }

    // --- pkg_data_to_json_pkg ---

    #[test]
    fn json_pkg_computes_subdir() {
        let p = PkgData {
            import_path: "github.com/foo/bar/sub/pkg".into(),
            mod_path: "github.com/foo/bar".into(),
            mod_version: "v1.2.3".into(),
            replace_version: String::new(),
            imports: vec![],
            cgo_pkg_config: vec![],
            cgo_cflags: vec![],
            cgo_ldflags: vec![],
            is_cgo: false,
            files: PkgFiles::default(),
        };
        let jp = pkg_data_to_json_pkg(&p, &|_| true);
        assert_eq!(jp.subdir, "sub/pkg");
        assert_eq!(jp.mod_key, "github.com/foo/bar@v1.2.3");
        assert_eq!(jp.drv_name, "gopkg-github.com-foo-bar-sub-pkg-v1.2.3");
    }

    #[test]
    fn json_pkg_uses_replace_version_in_mod_key() {
        let p = PkgData {
            import_path: "github.com/foo/bar".into(),
            mod_path: "github.com/foo/bar".into(),
            mod_version: "v1.0.0".into(),
            replace_version: "v2.0.0".into(),
            imports: vec![],
            cgo_pkg_config: vec![],
            cgo_cflags: vec![],
            cgo_ldflags: vec![],
            is_cgo: false,
            files: PkgFiles::default(),
        };
        let jp = pkg_data_to_json_pkg(&p, &|_| true);
        assert_eq!(jp.mod_key, "github.com/foo/bar@v2.0.0");
    }

    // --- go field resolution ---

    #[test]
    fn json_input_go_absent_deserializes_to_none() {
        let input: JsonInput =
            serde_json::from_str(r##"{"src":"/src","subPackages":["./..."]}"##).unwrap();
        assert!(input.go.is_none());
    }

    #[test]
    fn json_input_go_explicit_deserializes_to_some() {
        let input: JsonInput = serde_json::from_str(
            r##"{"go":"/nix/store/xxx-go/bin/go","src":"/src","subPackages":["./..."]}"##,
        )
        .unwrap();
        assert_eq!(input.go.as_deref(), Some("/nix/store/xxx-go/bin/go"));
    }

    /// When go is absent and DEFAULT_GO is unset at compile time (the normal CI
    /// case), resolve_packages must return the descriptive error immediately,
    /// before attempting to spawn any subprocess.
    #[test]
    fn resolve_packages_errors_without_go_field() {
        // DEFAULT_GO is None in test builds (GO2NIX_DEFAULT_GO not set in CI).
        if DEFAULT_GO.is_some() {
            return; // compiled with default baked in — skip
        }
        let input: JsonInput =
            serde_json::from_str(r##"{"src":"/nonexistent","subPackages":["./..."]}"##).unwrap();
        let err = match resolve_packages(&input) {
            Err(e) => e,
            Ok(_) => panic!("expected error when go is absent"),
        };
        assert!(
            err.to_string().contains("GO2NIX_DEFAULT_GO"),
            "unexpected error: {err}"
        );
    }

    #[test]
    fn json_pkg_filters_imports() {
        let p = PkgData {
            import_path: "github.com/foo/bar".into(),
            mod_path: "github.com/foo/bar".into(),
            mod_version: "v1.0.0".into(),
            replace_version: String::new(),
            imports: vec!["github.com/keep".into(), "github.com/drop".into()],
            cgo_pkg_config: vec![],
            cgo_cflags: vec![],
            cgo_ldflags: vec![],
            is_cgo: false,
            files: PkgFiles::default(),
        };
        let jp = pkg_data_to_json_pkg(&p, &|imp| imp == "github.com/keep");
        assert_eq!(jp.imports, vec!["github.com/keep"]);
    }

    fn test_opts(go_proxy: Option<&str>) -> GoListOpts<'_> {
        GoListOpts {
            tags: &[],
            mod_root: ".",
            goos: "",
            goarch: "",
            go_proxy,
            cgo_enabled: "",
        }
    }

    fn cmd_env(cmd: &std::process::Command, key: &str) -> Option<String> {
        cmd.get_envs()
            .find(|(k, _)| *k == key)
            .and_then(|(_, v)| v.map(|v| v.to_string_lossy().into_owned()))
    }

    // One test (not two) because std::env mutation is process-global and the
    // default cargo test harness runs tests in parallel.
    #[test]
    fn configure_go_env_goproxy_netrc_inherit_and_override() {
        std::env::set_var("GOPROXY", "https://proxy.example/");
        std::env::set_var("NETRC", "/tmp/netrc-test");

        // No explicit goProxy: inherit from env.
        let mut cmd = std::process::Command::new("true");
        configure_go_env(&mut cmd, "/tmp", &test_opts(None));
        assert_eq!(
            cmd_env(&cmd, "GOPROXY").as_deref(),
            Some("https://proxy.example/")
        );
        assert_eq!(cmd_env(&cmd, "NETRC").as_deref(), Some("/tmp/netrc-test"));

        // Explicit goProxy: overrides inherited value.
        let mut cmd = std::process::Command::new("true");
        configure_go_env(
            &mut cmd,
            "/tmp",
            &test_opts(Some("https://explicit.example/")),
        );
        assert_eq!(
            cmd_env(&cmd, "GOPROXY").as_deref(),
            Some("https://explicit.example/")
        );

        std::env::remove_var("GOPROXY");
        std::env::remove_var("NETRC");
    }

    /// Regression: the -json= field filter must include MFiles/SwigFiles/
    /// SwigCXXFiles or go list silently omits them and the compile-time
    /// "not yet supported" check never fires.
    #[test]
    fn run_go_list_surfaces_mfiles_and_swig() {
        let Some(go) = DEFAULT_GO else {
            eprintln!("skip: GO2NIX_DEFAULT_GO unset at build time");
            return;
        };
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(
            dir.path().join("go.mod"),
            "module example.com/mswig\n\ngo 1.23\n",
        )
        .unwrap();
        // CgoFiles is required for go/build to classify .m/.swig as package sources.
        std::fs::write(
            dir.path().join("a.go"),
            "package mswig\n// #cgo LDFLAGS: -lm\nimport \"C\"\n",
        )
        .unwrap();
        std::fs::write(dir.path().join("x.m"), "// objc\n").unwrap();
        std::fs::write(dir.path().join("x.swig"), "%module mswig\n").unwrap();
        std::fs::write(dir.path().join("x.swigcxx"), "%module mswigxx\n").unwrap();

        let stdout = run_go_list(
            go,
            dir.path().to_str().unwrap(),
            &["./...".to_string()],
            &test_opts(Some("off")),
        )
        .unwrap();
        let graph = parse_go_packages(&stdout).unwrap();
        let f = &graph.local_packages[0].files;
        assert_eq!(f.m_files, vec!["x.m"], "MFiles missing from go list output");
        assert_eq!(f.swig_files, vec!["x.swig"], "SwigFiles missing");
        assert_eq!(f.swig_cxx_files, vec!["x.swigcxx"], "SwigCXXFiles missing");
    }

    // --- Tier-3 offload: closures ---

    fn tp_with_imports(ip: &str, mp: &str, ver: &str, imports: &[&str], cxx: bool) -> String {
        let imp: Vec<String> = imports.iter().map(|i| format!("\"{i}\"")).collect();
        let cxx_field = if cxx { r#","CXXFiles":["a.cc"]"# } else { "" };
        format!(
            r#"{{"ImportPath":"{ip}","Module":{{"Path":"{mp}","Version":"{ver}"}},"Imports":[{}]{cxx_field}}}"#,
            imp.join(",")
        )
    }

    #[test]
    fn test_sub_package_closures() {
        // Graph: m/cmd/a → m/internal/x → tp/a/pkg, tp/b/pkg
        //        tp/a/pkg → tp/c/pkg (transitive)
        //        tp/b/pkg has CXXFiles
        let stdout = format!(
            "{}\n{}\n{}\n{}\n{}\n",
            local_pkg_json("m/cmd/a", "m", "/src/cmd/a", &["m/internal/x"]),
            local_pkg_json(
                "m/internal/x",
                "m",
                "/src/internal/x",
                &["tp/a/pkg", "tp/b/pkg"]
            ),
            tp_with_imports("tp/a/pkg", "tp/a", "v1.0.0", &["tp/c/pkg"], false),
            tp_with_imports("tp/b/pkg", "tp/b", "v2.0.0", &[], true),
            tp_with_imports("tp/c/pkg", "tp/c", "v3.0.0", &[], false),
        );
        let graph = parse_go_packages(stdout.as_bytes()).unwrap();
        let closures = compute_sub_package_closures(&graph, &["./cmd/a".into()]);
        let c = &closures["m/cmd/a"];
        assert_eq!(
            c.mod_keys,
            vec!["tp/a@v1.0.0", "tp/b@v2.0.0", "tp/c@v3.0.0"]
        );
        assert!(c.cxx, "transitive cxx must be true (tp/b has CXXFiles)");

        // Second subPackage with no third-party deps and no cxx.
        let closures2 = compute_sub_package_closures(&graph, &[".".into()]);
        // root "." → "m"; m has no local pkg in graph → empty closure
        assert_eq!(closures2["m"].mod_keys, Vec::<String>::new());
        assert!(!closures2["m"].cxx);
    }

    #[test]
    fn test_normalize_rel() {
        assert_eq!(normalize_rel("a/b/../c"), "a/c");
        assert_eq!(normalize_rel("./a/./b"), "a/b");
        assert_eq!(normalize_rel("a/b/../.."), ".");
        assert_eq!(normalize_rel("../a"), "../a");
        assert_eq!(normalize_rel("a/../../b"), "../b");
    }

    #[test]
    fn test_walk_local_replace_dirs() {
        let src = tempfile::tempdir().unwrap();
        let app = src.path().join("app");
        let sib = src.path().join("sib");
        let nested = src.path().join("sib/nested");
        std::fs::create_dir_all(&app).unwrap();
        std::fs::create_dir_all(&nested).unwrap();
        std::fs::write(
            app.join("go.mod"),
            "module app\nreplace x => ../sib\nreplace y => github.com/y v1.0.0\n",
        )
        .unwrap();
        std::fs::write(
            sib.join("go.mod"),
            "module sib\nreplace z => ./nested\nreplace esc => ../../escaped\n",
        )
        .unwrap();
        std::fs::write(nested.join("go.mod"), "module nested\n").unwrap();

        let dirs = walk_local_replace_dirs(src.path(), "app");
        assert_eq!(dirs, vec!["sib", "sib/nested"]);
    }

    #[test]
    fn test_nested_module_roots() {
        // Monorepo layout: src/{app,sib,unrelated}/ each has go.mod;
        // app/nested/ has go.mod; nested/under/ has go.mod (must NOT
        // be reported — descent stops at app/nested).
        let src = tempfile::tempdir().unwrap();
        for d in ["app", "app/nested", "app/nested/under", "sib", "unrelated"] {
            std::fs::create_dir_all(src.path().join(d)).unwrap();
            std::fs::write(src.path().join(d).join("go.mod"), "module x\n").unwrap();
        }
        // testdata under a seed is skipped.
        let td = src.path().join("app/testdata/x");
        std::fs::create_dir_all(&td).unwrap();
        std::fs::write(td.join("go.mod"), "module td\n").unwrap();

        // Seeds = modRoot + replaceDirs. unrelated/ is outside both → no I/O.
        let roots =
            find_nested_module_roots(src.path(), &["app".into(), "sib".into()]);
        assert_eq!(roots, vec!["app", "app/nested", "sib"]);

        // modRoot = "." walks the whole src.
        let roots_all = find_nested_module_roots(src.path(), &[".".into()]);
        assert_eq!(roots_all, vec!["app", "sib", "unrelated"]);
    }
}
