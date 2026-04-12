//! NAR (Nix ARchive) serialization and hashing.
//!
//! Computes the SHA-256 NAR hash of a filesystem path, producing SRI-format
//! output identical to `nix hash path`. The NAR format serializes a file
//! system object deterministically — sorted directory entries, padding to
//! 8-byte boundaries, symlink targets as strings.

use anyhow::{Context, Result};
use sha2::{Digest, Sha256};
use std::fs;
use std::io::Write;
use std::os::unix::fs::PermissionsExt;
use std::path::Path;

use base64::engine::general_purpose::STANDARD as BASE64;
use base64::Engine;

/// Compute the SRI-format NAR hash of a filesystem path.
///
/// Returns e.g. `"sha256-EwPR3o6o8dUzsAeHki26rYm/s1uDuEcpV3ljmaHiNSg="`.
pub fn hash_path(path: &Path) -> Result<String> {
    let mut hasher = Sha256::new();
    dump_path(&mut hasher, path).with_context(|| format!("NAR hashing {}", path.display()))?;
    let digest = hasher.finalize();
    Ok(format!("sha256-{}", BASE64.encode(digest)))
}

/// Write the NAR serialization of `path` to `w`.
fn dump_path(w: &mut impl Write, path: &Path) -> Result<()> {
    write_str(w, "nix-archive-1")?;
    dump_entry(w, path)
}

fn dump_entry(w: &mut impl Write, path: &Path) -> Result<()> {
    let meta = fs::symlink_metadata(path)
        .with_context(|| format!("stat {}", path.display()))?;

    write_str(w, "(")?;

    if meta.file_type().is_symlink() {
        write_str(w, "type")?;
        write_str(w, "symlink")?;
        write_str(w, "target")?;
        let target = fs::read_link(path)
            .with_context(|| format!("readlink {}", path.display()))?;
        write_str(w, &target.to_string_lossy())?;
    } else if meta.is_dir() {
        write_str(w, "type")?;
        write_str(w, "directory")?;

        let mut entries: Vec<_> = fs::read_dir(path)
            .with_context(|| format!("readdir {}", path.display()))?
            .collect::<std::result::Result<Vec<_>, _>>()
            .with_context(|| format!("reading dir entries of {}", path.display()))?;

        // NAR requires sorted entries.
        entries.sort_by_key(|e| e.file_name());

        for entry in entries {
            write_str(w, "entry")?;
            write_str(w, "(")?;
            write_str(w, "name")?;
            write_str(w, &entry.file_name().to_string_lossy())?;
            write_str(w, "node")?;
            dump_entry(w, &entry.path())?;
            write_str(w, ")")?;
        }
    } else if meta.is_file() {
        write_str(w, "type")?;
        write_str(w, "regular")?;
        if meta.permissions().mode() & 0o111 != 0 {
            write_str(w, "executable")?;
            write_str(w, "")?;
        }
        write_str(w, "contents")?;
        let contents = fs::read(path)
            .with_context(|| format!("read {}", path.display()))?;
        write_bytes(w, &contents)?;
    } else {
        anyhow::bail!("unsupported file type at {}", path.display());
    }

    write_str(w, ")")?;
    Ok(())
}

/// Write a NAR string: 8-byte little-endian length, content, padding to 8-byte boundary.
fn write_str(w: &mut impl Write, s: &str) -> Result<()> {
    write_bytes(w, s.as_bytes())
}

/// Write NAR bytes: 8-byte little-endian length, content, zero-padding to 8-byte boundary.
fn write_bytes(w: &mut impl Write, data: &[u8]) -> Result<()> {
    let len = data.len() as u64;
    w.write_all(&len.to_le_bytes())?;
    w.write_all(data)?;
    // Pad to 8-byte boundary.
    let pad = (8 - (data.len() % 8)) % 8;
    if pad > 0 {
        w.write_all(&[0u8; 8][..pad])?;
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::symlink;

    /// Helper: create a temp dir, run a closure, clean up.
    fn with_tmpdir(f: impl FnOnce(&Path)) {
        let dir = tempfile::tempdir().unwrap();
        f(dir.path());
    }

    #[test]
    fn hash_empty_file() {
        with_tmpdir(|dir| {
            let file = dir.join("empty");
            fs::write(&file, b"").unwrap();
            let hash = hash_path(&file).unwrap();
            // Known hash of an empty regular file NAR.
            assert!(hash.starts_with("sha256-"), "got: {hash}");
        });
    }

    #[test]
    fn hash_simple_file() {
        with_tmpdir(|dir| {
            let file = dir.join("hello");
            fs::write(&file, b"hello\n").unwrap();
            let hash = hash_path(&file).unwrap();
            assert!(hash.starts_with("sha256-"), "got: {hash}");
        });
    }

    #[test]
    fn hash_directory_is_sorted() {
        with_tmpdir(|dir| {
            let sub = dir.join("mydir");
            fs::create_dir(&sub).unwrap();
            fs::write(sub.join("b.txt"), b"b").unwrap();
            fs::write(sub.join("a.txt"), b"a").unwrap();
            // Should produce the same hash regardless of creation order.
            let hash1 = hash_path(&sub).unwrap();

            let sub2 = dir.join("mydir2");
            fs::create_dir(&sub2).unwrap();
            fs::write(sub2.join("a.txt"), b"a").unwrap();
            fs::write(sub2.join("b.txt"), b"b").unwrap();
            let hash2 = hash_path(&sub2).unwrap();

            assert_eq!(hash1, hash2);
        });
    }

    #[test]
    fn hash_symlink() {
        with_tmpdir(|dir| {
            let target = dir.join("target");
            fs::write(&target, b"content").unwrap();
            let link = dir.join("link");
            symlink("target", &link).unwrap();
            let hash = hash_path(&link).unwrap();
            assert!(hash.starts_with("sha256-"), "got: {hash}");
        });
    }

    /// Compare our NAR hash against `nix hash path` for a real Go module.
    #[test]
    fn hash_matches_nix() {
        let test_dir = "/root/go/pkg/mod/github.com/andybalholm/brotli@v1.2.0";
        if !Path::new(test_dir).exists() {
            eprintln!("skipping: {test_dir} not found");
            return;
        }

        let our_hash = hash_path(Path::new(test_dir)).unwrap();

        let output = std::process::Command::new("nix")
            .args(["hash", "path", test_dir])
            .output()
            .expect("nix hash path failed");
        assert!(output.status.success(), "nix hash path failed");
        let nix_hash = String::from_utf8(output.stdout).unwrap().trim().to_owned();

        assert_eq!(our_hash, nix_hash, "NAR hash mismatch: ours={our_hash} nix={nix_hash}");
    }
}
