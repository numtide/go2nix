//! Filesystem-backed cache for the full `resolveGoPackages` JSON output.
//!
//! Cache layout:
//!   $XDG_CACHE_HOME/go2nix/resolve/<sha256-hex-key>.json
//!
//! The key is computed by `resolve::compute_cache_key` from go.sum/go.mod,
//! a cheap local-only `go list` probe, and platform inputs. On a hit the
//! caller returns the cached JSON without running `go list -deps`, which
//! means GOMODCACHE never needs to be realised.
//!
//! Best-effort: any I/O failure here degrades to the uncached path. Same
//! atomic-rename write pattern as `nar_cache.rs`.

use std::fs;
use std::io::Write;
use std::path::{Path, PathBuf};

/// Bump whenever the shape of the serialised `JsonOutput` changes so stale
/// cache entries self-invalidate (e.g. when #43 added the `files` field).
pub const SCHEMA_VERSION: u32 = 1;

fn cache_dir() -> Option<PathBuf> {
    let cache_home = std::env::var("XDG_CACHE_HOME")
        .or_else(|_| std::env::var("HOME").map(|h| format!("{h}/.cache")))
        .ok()?;
    Some(PathBuf::from(cache_home).join("go2nix/resolve"))
}

/// Look up a cached JSON result by key. Returns `None` on miss or any error.
pub fn read(key: &str) -> Option<String> {
    read_from(&cache_dir()?, key)
}

/// Best-effort store of a JSON result. Errors are reported to stderr and
/// otherwise ignored — caching is an optimisation, never a hard requirement.
pub fn write(key: &str, json: &str) {
    let Some(dir) = cache_dir() else {
        return;
    };
    if let Err(e) = write_to(&dir, key, json) {
        eprintln!("go2nix: resolve-cache write skipped: {e:#}");
    }
}

fn read_from(dir: &Path, key: &str) -> Option<String> {
    fs::read_to_string(dir.join(format!("{key}.json"))).ok()
}

fn write_to(dir: &Path, key: &str, json: &str) -> anyhow::Result<()> {
    fs::create_dir_all(dir)?;
    let mut tmp = tempfile::NamedTempFile::new_in(dir)?;
    tmp.write_all(json.as_bytes())?;
    tmp.persist(dir.join(format!("{key}.json")))?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn round_trip() {
        let tmp = tempfile::tempdir().unwrap();

        assert!(read_from(tmp.path(), "abc123").is_none());
        write_to(tmp.path(), "abc123", r#"{"ok":true}"#).unwrap();
        assert_eq!(read_from(tmp.path(), "abc123").unwrap(), r#"{"ok":true}"#);
    }

    #[test]
    fn write_is_idempotent() {
        let tmp = tempfile::tempdir().unwrap();

        write_to(tmp.path(), "k", "v1").unwrap();
        write_to(tmp.path(), "k", "v1").unwrap();
        assert_eq!(read_from(tmp.path(), "k").unwrap(), "v1");
    }
}
