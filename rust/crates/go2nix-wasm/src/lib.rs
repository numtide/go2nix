use nix_wasm_rust::Value;
use serde::Deserialize;
use std::collections::BTreeMap;

#[derive(Deserialize)]
struct Lockfile {
    #[serde(rename = "mod")]
    modules: BTreeMap<String, String>,
    replace: Option<BTreeMap<String, String>>,
    pkg: BTreeMap<String, BTreeMap<String, Vec<String>>>,
}

/// Processed module info (pure data, no Nix values).
#[derive(Debug, Clone, PartialEq)]
pub struct ModuleInfo {
    hash: String,
    fetch_path: String,
    version: String,
    path: String,
    dir_suffix: String,
}

/// Processed package info (pure data, no Nix values).
#[derive(Debug, Clone, PartialEq)]
pub struct PackageInfo {
    mod_key: String,
    subdir: String,
    imports: Vec<String>,
    drv_name: String,
}

/// Result of processing a lockfile (pure data).
#[derive(Debug, Clone, PartialEq)]
pub struct ProcessedLockfile {
    modules: BTreeMap<String, ModuleInfo>,
    packages: BTreeMap<String, PackageInfo>,
}

pub fn process(content: &str) -> Result<ProcessedLockfile, toml::de::Error> {
    let lockfile: Lockfile = toml::from_str(content)?;
    let replaces = lockfile.replace.unwrap_or_default();

    let mut modules = BTreeMap::new();
    for (mod_key, hash) in &lockfile.modules {
        let (path, version) = mod_key
            .split_once('@')
            .expect("invalid module key: missing @");
        let fetch_path = replaces
            .get(mod_key)
            .map(|s| s.as_str())
            .unwrap_or(path);
        let dir_suffix = format!("{}@{}", escape_mod_path(fetch_path), version);

        modules.insert(
            mod_key.clone(),
            ModuleInfo {
                hash: hash.clone(),
                fetch_path: fetch_path.to_string(),
                version: version.to_string(),
                path: path.to_string(),
                dir_suffix,
            },
        );
    }

    let mut packages = BTreeMap::new();
    for (mod_key, pkg_map) in &lockfile.pkg {
        let (mod_path, _) = mod_key.split_once('@').expect("invalid module key: missing @");
        let prefix = format!("{}/", mod_path);

        for (import_path, imports) in pkg_map {
            let subdir = if import_path == mod_path {
                "".to_string()
            } else {
                import_path
                    .strip_prefix(&prefix)
                    .unwrap_or(import_path)
                    .to_string()
            };
            let drv_name = format!("gopkg-{}", sanitize_name(import_path));

            packages.insert(
                import_path.clone(),
                PackageInfo {
                    mod_key: mod_key.clone(),
                    subdir,
                    imports: imports.clone(),
                    drv_name,
                },
            );
        }
    }

    Ok(ProcessedLockfile { modules, packages })
}

#[no_mangle]
pub extern "C" fn process_lockfile(arg: Value) -> Value {
    let content = arg.read_file();
    let content = String::from_utf8(content)
        .unwrap_or_else(|e| nix_wasm_rust::panic(&format!("invalid UTF-8: {e}")));
    let result = process(&content)
        .unwrap_or_else(|e| nix_wasm_rust::panic(&format!("TOML parse error: {e}")));

    let modules: Vec<(&str, Value)> = result
        .modules
        .iter()
        .map(|(mod_key, m)| {
            (
                mod_key.as_str(),
                Value::make_attrset(&[
                    ("hash", Value::make_string(&m.hash)),
                    ("fetchPath", Value::make_string(&m.fetch_path)),
                    ("version", Value::make_string(&m.version)),
                    ("path", Value::make_string(&m.path)),
                    ("dirSuffix", Value::make_string(&m.dir_suffix)),
                ]),
            )
        })
        .collect();

    let mut packages: Vec<(&str, Value)> = Vec::new();
    for (import_path, pkg) in &result.packages {
        let import_values: Vec<Value> = pkg.imports.iter().map(|i| Value::make_string(i)).collect();
        packages.push((
            import_path.as_str(),
            Value::make_attrset(&[
                ("modKey", Value::make_string(&pkg.mod_key)),
                ("subdir", Value::make_string(&pkg.subdir)),
                ("imports", Value::make_list(&import_values)),
                ("drvName", Value::make_string(&pkg.drv_name)),
            ]),
        ));
    }

    Value::make_attrset(&[
        ("modules", Value::make_attrset(&modules)),
        ("packages", Value::make_attrset(&packages)),
    ])
}

fn escape_mod_path(s: &str) -> String {
    let mut r = String::with_capacity(s.len());
    for c in s.chars() {
        if c.is_ascii_uppercase() {
            r.push('!');
            r.push(c.to_ascii_lowercase());
        } else {
            r.push(c);
        }
    }
    r
}

fn sanitize_name(s: &str) -> String {
    s.replace('/', "-").replace('+', "_")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_escape_mod_path_lowercase() {
        assert_eq!(escape_mod_path("golang.org/x/crypto"), "golang.org/x/crypto");
    }

    #[test]
    fn test_escape_mod_path_uppercase() {
        assert_eq!(
            escape_mod_path("github.com/BurntSushi/toml"),
            "github.com/!burnt!sushi/toml"
        );
    }

    #[test]
    fn test_escape_mod_path_empty() {
        assert_eq!(escape_mod_path(""), "");
    }

    #[test]
    fn test_sanitize_name_slashes() {
        assert_eq!(
            sanitize_name("golang.org/x/crypto/ssh"),
            "golang.org-x-crypto-ssh"
        );
    }

    #[test]
    fn test_sanitize_name_plus() {
        assert_eq!(sanitize_name("google.golang.org/grpc++"), "google.golang.org-grpc__");
    }

    #[test]
    fn test_sanitize_name_mixed() {
        assert_eq!(sanitize_name("a/b+c/d"), "a-b_c-d");
    }

    #[test]
    fn test_process_basic_lockfile() {
        let toml = r#"
[mod]
"git.sr.ht/~geb/opt@v0.0.0-20230911153257-e72225a1933c" = "sha256-abc123"
"github.com/bendahl/uinput@v1.7.0" = "sha256-def456"

[pkg."git.sr.ht/~geb/opt@v0.0.0-20230911153257-e72225a1933c"]
"git.sr.ht/~geb/opt" = []

[pkg."github.com/bendahl/uinput@v1.7.0"]
"github.com/bendahl/uinput" = []
"#;
        let result = process(toml).unwrap();

        assert_eq!(result.modules.len(), 2);
        assert_eq!(result.packages.len(), 2);

        let opt_mod = &result.modules["git.sr.ht/~geb/opt@v0.0.0-20230911153257-e72225a1933c"];
        assert_eq!(opt_mod.hash, "sha256-abc123");
        assert_eq!(opt_mod.path, "git.sr.ht/~geb/opt");
        assert_eq!(opt_mod.fetch_path, "git.sr.ht/~geb/opt");
        assert_eq!(opt_mod.version, "v0.0.0-20230911153257-e72225a1933c");
        assert_eq!(
            opt_mod.dir_suffix,
            "git.sr.ht/~geb/opt@v0.0.0-20230911153257-e72225a1933c"
        );

        let opt_pkg = &result.packages["git.sr.ht/~geb/opt"];
        assert_eq!(
            opt_pkg.mod_key,
            "git.sr.ht/~geb/opt@v0.0.0-20230911153257-e72225a1933c"
        );
        assert_eq!(opt_pkg.subdir, "");
        assert!(opt_pkg.imports.is_empty());
        assert_eq!(opt_pkg.drv_name, "gopkg-git.sr.ht-~geb-opt");
    }

    #[test]
    fn test_process_with_subpackages() {
        let toml = r#"
[mod]
"golang.org/x/crypto@v0.4.0" = "sha256-xyz"

[pkg."golang.org/x/crypto@v0.4.0"]
"golang.org/x/crypto" = []
"golang.org/x/crypto/ssh" = ["golang.org/x/crypto"]
"golang.org/x/crypto/chacha20" = []
"#;
        let result = process(toml).unwrap();

        assert_eq!(result.packages.len(), 3);

        let root = &result.packages["golang.org/x/crypto"];
        assert_eq!(root.subdir, "");

        let ssh = &result.packages["golang.org/x/crypto/ssh"];
        assert_eq!(ssh.subdir, "ssh");
        assert_eq!(ssh.imports, vec!["golang.org/x/crypto"]);
        assert_eq!(ssh.drv_name, "gopkg-golang.org-x-crypto-ssh");

        let chacha = &result.packages["golang.org/x/crypto/chacha20"];
        assert_eq!(chacha.subdir, "chacha20");
    }

    #[test]
    fn test_process_with_replace() {
        let toml = r#"
[mod]
"example.com/foo@v1.0.0" = "sha256-aaa"

[replace]
"example.com/foo@v1.0.0" = "example.com/fork/foo"

[pkg."example.com/foo@v1.0.0"]
"example.com/foo" = []
"#;
        let result = process(toml).unwrap();

        let m = &result.modules["example.com/foo@v1.0.0"];
        assert_eq!(m.path, "example.com/foo");
        assert_eq!(m.fetch_path, "example.com/fork/foo");
        assert_eq!(m.dir_suffix, "example.com/fork/foo@v1.0.0");
    }

    #[test]
    fn test_process_replace_with_uppercase() {
        let toml = r#"
[mod]
"github.com/BurntSushi/toml@v1.2.0" = "sha256-bbb"

[pkg."github.com/BurntSushi/toml@v1.2.0"]
"github.com/BurntSushi/toml" = []
"#;
        let result = process(toml).unwrap();

        let m = &result.modules["github.com/BurntSushi/toml@v1.2.0"];
        assert_eq!(
            m.dir_suffix,
            "github.com/!burnt!sushi/toml@v1.2.0"
        );
    }

    #[test]
    fn test_process_no_replace_section() {
        let toml = r#"
[mod]
"example.com/bar@v2.0.0" = "sha256-ccc"

[pkg."example.com/bar@v2.0.0"]
"example.com/bar" = []
"#;
        let result = process(toml).unwrap();

        let m = &result.modules["example.com/bar@v2.0.0"];
        assert_eq!(m.fetch_path, "example.com/bar");
    }

    #[test]
    fn test_process_invalid_toml() {
        assert!(process("not valid toml [[[").is_err());
    }
}
