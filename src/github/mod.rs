mod download;
mod list_tags;
mod parse_url;
mod resolve_ref;

pub use download::{
    choose_package_prefix_for_blob_path, collect_repo_relative_paths, download_and_extract,
    download_tarball_bytes, extract_tarball_with_prefix, parent_dir_in_repo,
    path_in_repo_looks_like_file,
};
pub use list_tags::list_tags;
pub use parse_url::{
    canonical_github_tree_url, github_source_from_segments, normalized_identity, parse_github_url,
    GitHubSource,
};
pub use resolve_ref::{check_rate_limit_hint, resolve_ref_to_sha};
