//! Filesystem-backed cache for h1: → NAR hash mappings.
//!
//! Cache layout:
//!   $XDG_CACHE_HOME/go2nix/nar/<url-encoded-h1-hash>
//!
//! Each file contains the SRI NAR hash (e.g. "sha256-abc..."). Writes use
//! atomic rename so concurrent processes never read partial data. Duplicate
//! computation is harmless — same input always produces the same output.

use anyhow::{Context, Result};
use std::fs;
use std::io::Write;
use std::path::PathBuf;

/// Persistent cache from h1: hashes to NAR hashes.
pub struct NarCache {
    dir: PathBuf,
}

impl NarCache {
    /// Open (or create) the cache directory.
    pub fn open() -> Result<Self> {
        let cache_home = std::env::var("XDG_CACHE_HOME")
            .or_else(|_| std::env::var("HOME").map(|h| format!("{h}/.cache")))
            .unwrap_or_else(|_| "/tmp".to_owned());
        let dir = PathBuf::from(cache_home).join("go2nix/nar");
        fs::create_dir_all(&dir)
            .with_context(|| format!("creating cache dir {}", dir.display()))?;
        Ok(Self { dir })
    }

    /// Look up a cached NAR hash by h1: key.
    pub fn get(&self, h1: &str) -> Option<String> {
        let path = self.key_path(h1);
        fs::read_to_string(&path).ok().map(|s| s.trim().to_owned())
    }

    /// Store a NAR hash for an h1: key. Uses atomic write-then-rename.
    pub fn put(&self, h1: &str, nar_hash: &str) -> Result<()> {
        let path = self.key_path(h1);

        // Write to a temp file in the same directory, then rename.
        // This is atomic on POSIX and safe under concurrent writers.
        let mut tmp = tempfile::NamedTempFile::new_in(&self.dir)
            .context("creating temp file for nar cache")?;
        tmp.write_all(nar_hash.as_bytes())
            .context("writing nar hash to temp file")?;
        tmp.persist(&path)
            .with_context(|| format!("persisting cache entry {}", path.display()))?;
        Ok(())
    }

    /// Map an h1: hash to a filename. URL-encode the characters that aren't
    /// safe in filenames (`:`, `/`, `+`, `=`).
    fn key_path(&self, h1: &str) -> PathBuf {
        let encoded = h1
            .replace('%', "%25")
            .replace(':', "%3A")
            .replace('/', "%2F")
            .replace('+', "%2B")
            .replace('=', "%3D");
        self.dir.join(encoded)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn round_trip() {
        let dir = tempfile::tempdir().unwrap();
        std::env::set_var("XDG_CACHE_HOME", dir.path());
        let cache = NarCache::open().unwrap();

        let h1 = "h1:abc123+/=";
        assert!(cache.get(h1).is_none());

        cache.put(h1, "sha256-xyz789").unwrap();
        assert_eq!(cache.get(h1).unwrap(), "sha256-xyz789");
    }

    #[test]
    fn idempotent_put() {
        let dir = tempfile::tempdir().unwrap();
        std::env::set_var("XDG_CACHE_HOME", dir.path());
        let cache = NarCache::open().unwrap();

        let h1 = "h1:test";
        cache.put(h1, "sha256-aaa").unwrap();
        cache.put(h1, "sha256-aaa").unwrap();
        assert_eq!(cache.get(h1).unwrap(), "sha256-aaa");
    }
}
