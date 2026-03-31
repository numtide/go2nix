//! Core implementation of Go package resolution.
//!
//! Runs `go list -json -deps -e`, parses the output into a `PackageGraph`,
//! and serializes it to JSON for the C++ nix shim.

use anyhow::{anyhow, bail, Context, Result};
use serde::{Deserialize, Serialize};
use std::collections::{BTreeMap, BTreeSet};
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
    #[serde(rename = "CgoFiles")]
    cgo_files: Vec<String>,
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
}

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
    out
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
    // -buildvcs=false avoids VCS tool lookups.
    cmd.env_clear();
    for (k, v) in inherit_env(&["GOMODCACHE", "GOPATH", "HOME"]) {
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
    cmd.arg("-json=ImportPath,Dir,Module,Imports,CgoFiles,CgoPkgConfig,CgoCFLAGS,CgoLDFLAGS,Error");
    cmd.arg("-deps");
    cmd.arg("-e");
    cmd.arg("-buildvcs=false");

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
    cmd.arg("-json=ImportPath,Module,Imports,CgoFiles,CgoPkgConfig,CgoCFLAGS,CgoLDFLAGS,Error");
    cmd.arg("-deps");
    cmd.arg("-test");
    cmd.arg("-e");
    cmd.arg("-buildvcs=false");

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

pub(crate) fn parse_test_packages(
    stdout: &[u8],
    third_party_paths: &BTreeSet<String>,
    replacements: &mut BTreeMap<String, (String, String)>,
) -> Result<Vec<PkgData>> {
    let mut test_packages = Vec::new();
    let mut test_only_paths = BTreeSet::new();
    let mut pkg_errors = Vec::new();

    for result in serde_json::Deserializer::from_slice(stdout).into_iter::<GoPackage>() {
        let jpkg = result.context("resolveGoPackages: failed to parse test go list JSON")?;

        if let Some(ref err) = jpkg.error {
            if !err.err.is_empty() {
                pkg_errors.push(format!("{}: {}", jpkg.import_path, err.err));
                continue;
            }
        }

        // Skip stdlib (no module).
        let Some(module) = jpkg.module else {
            continue;
        };

        // Skip synthetic test packages: test mains (foo.test) and
        // recompiled variants (foo [foo.test]).
        if jpkg.import_path.contains('[') || jpkg.import_path.ends_with(".test") {
            continue;
        }

        let (replace_path, replace_version) = extract_replace(&module);

        // Skip local packages (main module or local replaces).
        let is_local = module.main || (!replace_path.is_empty() && replace_version.is_empty());
        if is_local {
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

    Ok(test_packages)
}

// ---------------------------------------------------------------------------
// JSON → package graph
// ---------------------------------------------------------------------------

pub(crate) struct PackageGraph {
    packages: Vec<PkgData>,
    local_packages: Vec<LocalPkgData>,
    third_party_paths: BTreeSet<String>,
    replacements: BTreeMap<String, (String, String)>,
    module_path: String,
    test_packages: Vec<PkgData>,
    test_only_paths: BTreeSet<String>,
}

pub(crate) fn parse_go_packages(stdout: &[u8]) -> Result<PackageGraph> {
    let mut packages = Vec::new();
    let mut pkg_errors = Vec::new();
    let mut third_party_paths = BTreeSet::new();
    let mut local_paths = BTreeSet::new();
    let mut replacements: BTreeMap<String, (String, String)> = BTreeMap::new();
    let mut module_path = String::new();

    // Raw local package data collected during first pass; imports are
    // classified into local vs third-party after the loop.
    struct RawLocalPkg {
        import_path: String,
        dir: String,
        imports: Vec<String>,
        cgo_pkg_config: Vec<String>,
        cgo_cflags: Vec<String>,
        cgo_ldflags: Vec<String>,
        is_cgo: bool,
    }
    let mut raw_local_pkgs: Vec<RawLocalPkg> = Vec::new();

    for result in serde_json::Deserializer::from_slice(stdout).into_iter::<GoPackage>() {
        let jpkg = result.context("resolveGoPackages: failed to parse go list JSON")?;

        if let Some(ref err) = jpkg.error {
            if !err.err.is_empty() {
                pkg_errors.push(format!("{}: {}", jpkg.import_path, err.err));
                continue;
            }
        }

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

            local_paths.insert(jpkg.import_path.clone());

            raw_local_pkgs.push(RawLocalPkg {
                import_path: jpkg.import_path,
                dir: jpkg.dir,
                imports: jpkg.imports,
                cgo_pkg_config: jpkg.cgo_pkg_config,
                cgo_cflags: jpkg.cgo_cflags,
                cgo_ldflags: jpkg.cgo_ldflags,
                is_cgo: !jpkg.cgo_files.is_empty(),
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
            }
        })
        .collect();

    Ok(PackageGraph {
        packages,
        local_packages,
        third_party_paths,
        replacements,
        module_path,
        test_packages: Vec::new(),
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

        let test_stdout =
            run_go_list_test(go_bin, &input.src, &local_ips, &opts)?;

        let test_pkgs = parse_test_packages(
            &test_stdout,
            &graph.third_party_paths,
            &mut graph.replacements,
        )?;
        graph.test_only_paths = test_pkgs.iter().map(|p| p.import_path.clone()).collect();
        graph.test_packages = test_pkgs;
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
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct JsonOutput {
    packages: BTreeMap<String, JsonPkg>,
    local_packages: BTreeMap<String, JsonLocalPkg>,
    module_path: String,
    replacements: BTreeMap<String, JsonReplacement>,
    test_packages: BTreeMap<String, JsonPkg>,
    /// NAR hashes for modules, keyed by "path@version".
    /// Only populated when resolveHashes is true.
    #[serde(skip_serializing_if = "BTreeMap::is_empty")]
    module_hashes: BTreeMap<String, String>,
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
}

#[derive(Serialize)]
struct JsonReplacement {
    path: String,
    version: String,
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
    }
}

/// Convert a `PackageGraph` to a JSON string.
pub(crate) fn package_graph_to_json(
    graph: &PackageGraph,
    src_dir: &str,
    module_hashes: BTreeMap<String, String>,
) -> Result<String> {
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

    // Build local_packages map.
    let mut local_packages = BTreeMap::new();
    for lp in &graph.local_packages {
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
        local_packages.insert(
            lp.import_path.clone(),
            JsonLocalPkg {
                dir,
                local_imports: lp.local_imports.clone(),
                third_party_imports: lp.third_party_imports.clone(),
                is_cgo: lp.is_cgo,
                cgo_pkg_config: lp.cgo_pkg_config.clone(),
                cgo_cflags: lp.cgo_cflags.clone(),
                cgo_ldflags: lp.cgo_ldflags.clone(),
            },
        );
    }

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

    let output = JsonOutput {
        packages,
        local_packages,
        module_path: graph.module_path.clone(),
        replacements,
        test_packages,
        module_hashes,
    };

    serde_json::to_string(&output).context("failed to serialize output JSON")
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;

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
    }

    // --- parse_test_packages ---

    #[test]
    fn test_parse_skips_build_graph_packages() {
        let mut third_party = BTreeSet::new();
        third_party.insert("github.com/already/known".to_owned());

        let input = third_party_json("github.com/already/known", "github.com/already/known", "v1.0.0");
        let mut replacements = BTreeMap::new();
        let result = parse_test_packages(input.as_bytes(), &third_party, &mut replacements).unwrap();
        assert!(result.is_empty());
    }

    #[test]
    fn test_parse_collects_test_only() {
        let third_party = BTreeSet::new(); // empty build graph

        let input = third_party_json("github.com/testify", "github.com/testify", "v1.9.0");
        let mut replacements = BTreeMap::new();
        let result = parse_test_packages(input.as_bytes(), &third_party, &mut replacements).unwrap();
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].import_path, "github.com/testify");
    }

    #[test]
    fn test_parse_skips_synthetic_packages() {
        let third_party = BTreeSet::new();

        let input = [
            // Synthetic test main
            third_party_json("example.com/pkg.test", "example.com/pkg", ""),
            // Recompiled variant
            format!(r#"{{"ImportPath":"example.com/pkg [example.com/pkg.test]","Module":{{"Path":"example.com/pkg","Main":true}},"Imports":[]}}"#),
            // Real test-only dep
            third_party_json("github.com/testify", "github.com/testify", "v1.9.0"),
        ]
        .join("\n");

        let mut replacements = BTreeMap::new();
        let result = parse_test_packages(input.as_bytes(), &third_party, &mut replacements).unwrap();
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].import_path, "github.com/testify");
    }

    #[test]
    fn test_parse_skips_local_packages() {
        let third_party = BTreeSet::new();
        let input = local_pkg_json("example.com/m/internal/x", "example.com/m", "/src/x", &[]);
        let mut replacements = BTreeMap::new();
        let result = parse_test_packages(input.as_bytes(), &third_party, &mut replacements).unwrap();
        assert!(result.is_empty());
    }

    #[test]
    fn test_parse_deduplicates() {
        let third_party = BTreeSet::new();
        let input = [
            third_party_json("github.com/dup", "github.com/dup", "v1.0.0"),
            third_party_json("github.com/dup", "github.com/dup", "v1.0.0"),
        ]
        .join("\n");
        let mut replacements = BTreeMap::new();
        let result = parse_test_packages(input.as_bytes(), &third_party, &mut replacements).unwrap();
        assert_eq!(result.len(), 1);
    }

    #[test]
    fn test_parse_collects_test_replacements() {
        let third_party = BTreeSet::new();
        let input = replaced_json(
            "github.com/test/dep",
            "github.com/test/dep",
            "v1.0.0",
            "github.com/fork/dep",
            "v1.1.0",
        );
        let mut replacements = BTreeMap::new();
        let result = parse_test_packages(input.as_bytes(), &third_party, &mut replacements).unwrap();
        assert_eq!(result.len(), 1);
        let (path, _) = &replacements["github.com/test/dep@v1.1.0"];
        assert_eq!(path, "github.com/fork/dep");
    }

    // --- extract_replace ---

    #[test]
    fn extract_replace_none() {
        let m = GoModule {
            path: "foo".into(),
            version: "v1".into(),
            main: false,
            replace: None,
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
            main: false,
            replace: Some(Box::new(GoModule {
                path: "bar".into(),
                version: "v2".into(),
                main: false,
                replace: None,
            })),
        };
        let (p, v) = extract_replace(&m);
        assert_eq!(p, "bar");
        assert_eq!(v, "v2");
    }

    // --- sanitize_name ---

    #[test]
    fn sanitize_name_whitelist() {
        assert_eq!(sanitize_name("github.com/foo/bar+baz"), "github.com-foo-bar+baz");
        assert_eq!(sanitize_name("git.sr.ht/~geb/dotool"), "git.sr.ht-_geb-dotool");
        assert_eq!(sanitize_name("example.com/@scope/pkg"), "example.com-_at_scope-pkg");
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
        };
        let jp = pkg_data_to_json_pkg(&p, &|_| true);
        assert_eq!(jp.mod_key, "github.com/foo/bar@v2.0.0");
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
        };
        let jp = pkg_data_to_json_pkg(&p, &|imp| imp == "github.com/keep");
        assert_eq!(jp.imports, vec!["github.com/keep"]);
    }
}
