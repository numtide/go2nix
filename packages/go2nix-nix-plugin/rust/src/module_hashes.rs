//! Resolve NAR hashes for Go modules from go.sum + GOMODCACHE.
//!
//! Flow:
//! 1. Parse go.sum to get h1: hashes for each module@version
//! 2. For each module, check the filesystem cache (h1: → NAR hash)
//! 3. On cache miss, find the extracted source tree in GOMODCACHE,
//!    compute its NAR hash, and cache the result
//!
//! The NAR hash covers only the extracted source tree (not .info, .zip, etc.),
//! making it a pure function of the module content — which is what h1: captures.

use crate::nar;
use crate::nar_cache::NarCache;
use anyhow::{Context, Result};
use std::collections::BTreeMap;
use std::path::{Path, PathBuf};

/// Parsed go.sum entry (only the directory hash, not the /go.mod hash).
#[derive(Debug)]
struct GoSumEntry {
    path: String,
    version: String,
    h1: String,
}

/// Module hash info returned to the caller.
#[derive(Debug, Clone)]
pub struct ModuleHash {
    pub nar_hash: String,
}

/// Parse go.sum and resolve NAR hashes for all modules.
///
/// `go_sum_path`: path to go.sum
/// `gomodcache`: GOMODCACHE directory (contains extracted source trees)
pub fn resolve_module_hashes(
    go_sum_path: &Path,
    gomodcache: &Path,
) -> Result<BTreeMap<String, ModuleHash>> {
    let entries = parse_go_sum(go_sum_path)?;
    let cache = NarCache::open().context("opening NAR hash cache")?;

    let mut result = BTreeMap::new();

    for entry in &entries {
        let key = format!("{}@{}", entry.path, entry.version);

        // Check cache first.
        if let Some(cached) = cache.get(&entry.h1) {
            result.insert(key, ModuleHash { nar_hash: cached });
            continue;
        }

        // Compute NAR hash from extracted source tree in GOMODCACHE.
        let source_dir = module_source_dir(gomodcache, &entry.path, &entry.version);
        if !source_dir.exists() {
            // Module not in local cache — skip. The lockfile won't have a
            // hash, and the Nix builder will fail clearly when it can't find
            // the FOD hash.
            continue;
        }

        let nar_hash = nar::hash_path(&source_dir)
            .with_context(|| format!("computing NAR hash of {}", source_dir.display()))?;

        cache.put(&entry.h1, &nar_hash).ok(); // best-effort cache write
        result.insert(key, ModuleHash { nar_hash });
    }

    Ok(result)
}

/// Parse go.sum into directory-hash entries (skip /go.mod lines).
fn parse_go_sum(path: &Path) -> Result<Vec<GoSumEntry>> {
    let content = std::fs::read_to_string(path)
        .with_context(|| format!("reading {}", path.display()))?;

    let mut entries = Vec::new();

    for line in content.lines() {
        let line = line.trim();
        if line.is_empty() {
            continue;
        }

        // Format: <module> <version>[/go.mod] <hash>
        let parts: Vec<&str> = line.splitn(3, ' ').collect();
        if parts.len() != 3 {
            continue;
        }

        let (mod_path, version, hash) = (parts[0], parts[1], parts[2]);

        // Skip /go.mod hash entries — we only need the directory hash.
        if version.ends_with("/go.mod") {
            continue;
        }

        if !hash.starts_with("h1:") {
            continue;
        }

        entries.push(GoSumEntry {
            path: mod_path.to_owned(),
            version: version.to_owned(),
            h1: hash.to_owned(),
        });
    }

    Ok(entries)
}

/// Construct the path to a module's extracted source tree in GOMODCACHE.
///
/// GOMODCACHE layout: `<escaped-path>@<version>/`
/// e.g. `$GOMODCACHE/github.com/foo/bar@v1.2.3/`
///
/// Go escapes uppercase letters in module paths: `A` → `!a`.
fn module_source_dir(gomodcache: &Path, mod_path: &str, version: &str) -> PathBuf {
    let escaped = escape_mod_path(mod_path);
    gomodcache.join(format!("{escaped}@{version}"))
}

/// Go module path case-escaping: uppercase → `!` + lowercase.
/// Matches golang.org/x/mod/module.EscapePath().
fn escape_mod_path(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    for c in s.chars() {
        if c.is_ascii_uppercase() {
            out.push('!');
            out.push(c.to_ascii_lowercase());
        } else {
            out.push(c);
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_go_sum_basic() {
        let dir = tempfile::tempdir().unwrap();
        let sum_path = dir.path().join("go.sum");
        std::fs::write(
            &sum_path,
            "github.com/foo/bar v1.2.3 h1:abc123=\n\
             github.com/foo/bar v1.2.3/go.mod h1:modmod=\n\
             github.com/baz/qux v0.1.0 h1:xyz789=\n",
        )
        .unwrap();

        let entries = parse_go_sum(&sum_path).unwrap();
        assert_eq!(entries.len(), 2);
        assert_eq!(entries[0].path, "github.com/foo/bar");
        assert_eq!(entries[0].version, "v1.2.3");
        assert_eq!(entries[0].h1, "h1:abc123=");
        assert_eq!(entries[1].path, "github.com/baz/qux");
    }

    #[test]
    fn parse_go_sum_skips_non_h1() {
        let dir = tempfile::tempdir().unwrap();
        let sum_path = dir.path().join("go.sum");
        std::fs::write(&sum_path, "github.com/foo v1.0.0 h2:something=\n").unwrap();

        let entries = parse_go_sum(&sum_path).unwrap();
        assert_eq!(entries.len(), 0);
    }

    #[test]
    fn escape_mod_path_basic() {
        assert_eq!(escape_mod_path("github.com/BurntSushi/toml"), "github.com/!burnt!sushi/toml");
        assert_eq!(escape_mod_path("github.com/foo/bar"), "github.com/foo/bar");
    }
}

#[cfg(test)]
mod integration_tests {
    use super::*;

    /// End-to-end test: resolve hashes for the go2nix project's own modules.
    #[test]
    fn resolve_hashes_for_go2nix() {
        let go_sum = Path::new("/root/src/go2nix/go/go2nix/go.sum");
        if !go_sum.exists() {
            eprintln!("skipping: go.sum not found");
            return;
        }

        let gomodcache = Path::new("/root/go/pkg/mod");
        if !gomodcache.exists() {
            eprintln!("skipping: GOMODCACHE not found");
            return;
        }

        let result = resolve_module_hashes(go_sum, gomodcache).unwrap();
        eprintln!("resolved {} module hashes:", result.len());
        for (key, hash) in &result {
            eprintln!("  {key} → {}", hash.nar_hash);
        }

        // Should have at least some modules
        assert!(!result.is_empty(), "expected at least one module hash");

        // All hashes should be valid SRI format
        for (key, hash) in &result {
            assert!(
                hash.nar_hash.starts_with("sha256-"),
                "bad hash for {key}: {}",
                hash.nar_hash
            );
        }
    }
}
