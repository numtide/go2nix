//! Go package resolver for Nix.
//!
//! Runs `go list -json -deps -e` against a Go source tree and returns the
//! third-party package graph, local packages, module replacements, and
//! (optionally) test-only dependencies as JSON.
//!
//! Also resolves NAR hashes for Go modules from go.sum + GOMODCACHE,
//! eliminating the need for a checked-in lockfile.
//!
//! Pure Rust with no nix dependencies — the nix integration layer
//! (`plugin/resolveGoPackages.cc`) handles primop registration and
//! JSON ↔ nix value conversion via the nix C API.

mod module_hashes;
mod nar;
mod nar_cache;
mod resolve;

use std::ffi::{CStr, CString};

/// Resolve Go packages from JSON input, returning JSON output.
///
/// Input JSON: `{ "go": "...", "src": "...", "tags": [], "doCheck": false, ... }`
/// Output JSON: `{ "packages": {...}, "localPackages": {...}, "modulePath": "...",
///   "replacements": {...}, "testPackages": {...} }`
///
/// Returns 0 on success, non-zero on error. Caller must free `*out` / `*err_out`
/// with `go2nix_free_string`.
///
/// # Safety
/// `input_json` must be a valid null-terminated C string. `out` and `err_out`
/// must be valid pointers to writable `*mut c_char` locations.
#[no_mangle]
pub unsafe extern "C" fn resolve_go_packages_json(
    input_json: *const std::ffi::c_char,
    out: *mut *mut std::ffi::c_char,
    err_out: *mut *mut std::ffi::c_char,
) -> i32 {
    unsafe fn inner(input_json: *const std::ffi::c_char) -> Result<String, String> {
        let input = CStr::from_ptr(input_json)
            .to_str()
            .map_err(|e| format!("invalid UTF-8 in input: {e}"))?;

        let opts: resolve::JsonInput =
            serde_json::from_str(input).map_err(|e| format!("failed to parse input JSON: {e}"))?;

        let graph = resolve::resolve_packages(&opts).map_err(|e| format!("{e:#}"))?;

        // Resolve NAR hashes from go.sum + GOMODCACHE when requested.
        let hashes = if opts.resolve_hashes {
            let src_dir = std::path::Path::new(&opts.src);
            let mod_root = &opts.mod_root;
            let go_sum_path = if mod_root == "." {
                src_dir.join("go.sum")
            } else {
                src_dir.join(mod_root).join("go.sum")
            };

            let gomodcache = resolve::find_gomodcache(&opts.go)
                .map_err(|e| format!("finding GOMODCACHE: {e:#}"))?;

            module_hashes::resolve_module_hashes(&go_sum_path, &gomodcache)
                .map_err(|e| format!("resolving module hashes: {e:#}"))?
                .into_iter()
                .map(|(k, v)| (k, v.nar_hash))
                .collect()
        } else {
            std::collections::BTreeMap::new()
        };

        resolve::package_graph_to_json(&graph, &opts.src, hashes)
            .map_err(|e| format!("{e:#}"))
    }

    match inner(input_json) {
        Ok(json) => {
            let cstr = CString::new(json).unwrap_or_else(|_| CString::new("{}").unwrap());
            *out = cstr.into_raw();
            0
        }
        Err(msg) => {
            let cstr = CString::new(msg)
                .unwrap_or_else(|_| CString::new("error (message contained null byte)").unwrap());
            *err_out = cstr.into_raw();
            1
        }
    }
}

/// Free a string allocated by `resolve_go_packages_json`.
///
/// # Safety
/// `s` must be a pointer returned by `resolve_go_packages_json`, or null.
#[no_mangle]
pub unsafe extern "C" fn go2nix_free_string(s: *mut std::ffi::c_char) {
    if !s.is_null() {
        drop(CString::from_raw(s));
    }
}
