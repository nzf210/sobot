use std::path::{Path, PathBuf};

/// Resolves `raw` relative to `base` and ensures the resolved path stays
/// within `base`. Returns the resolved canonical path or an error if a
/// path traversal attempt is detected.
pub fn sanitize_path(raw: &str, base: &Path) -> Result<PathBuf, String> {
    let resolved = base.join(raw);

    let canonical = match resolved.canonicalize() {
        Ok(p) => p,
        Err(_) => {
            // File may not exist yet (e.g. DATA_DIR created on first use).
            // Resolve .. components manually and check the result stays in base.
            let normalized = normalize_path(&resolved);
            let base_canonical = base
                .canonicalize()
                .map_err(|e| format!("cannot resolve base directory: {}", e))?;
            if !normalized.starts_with(&base_canonical) {
                return Err(format!(
                    "path traversal blocked: '{}' escapes '{}'",
                    raw,
                    base.display()
                ));
            }
            normalized
        }
    };

    let base_canonical = base
        .canonicalize()
        .map_err(|e| format!("cannot resolve base directory: {}", e))?;

    if !canonical.starts_with(&base_canonical) {
        return Err(format!(
            "path traversal blocked: '{}' escapes '{}'",
            raw,
            base.display()
        ));
    }

    Ok(canonical)
}

/// Normalize a path by resolving . and .. components without touching the
/// filesystem (unlike canonicalize). Does not resolve symlinks.
fn normalize_path(path: &Path) -> PathBuf {
    let mut stack: Vec<PathBuf> = Vec::new();
    let is_abs = path.is_absolute();

    for component in path.components() {
        match component {
            std::path::Component::ParentDir => {
                // If we can pop, do so. Otherwise, if absolute, ignore
                // (can't go above root). If relative, push the ..
                if stack.is_empty() && !is_abs {
                    stack.push(PathBuf::from(".."));
                } else if !stack.is_empty() {
                    let top = stack.last().unwrap();
                    if top == std::path::Component::ParentDir.as_os_str() {
                        stack.push(PathBuf::from(".."));
                    } else {
                        stack.pop();
                    }
                }
                // If absolute and stack is empty, ignore (can't go above /)
            }
            std::path::Component::CurDir => {
                // skip .
            }
            c => {
                stack.push(c.as_os_str().into());
            }
        }
    }

    if is_abs {
        let mut result = PathBuf::from("/");
        for p in stack {
            result.push(p);
        }
        result
    } else {
        let mut result = PathBuf::new();
        for p in stack {
            result.push(p);
        }
        result
    }
}

