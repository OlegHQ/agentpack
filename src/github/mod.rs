mod download;
mod git_protocol;
mod list_tags;
mod metadata_cache;
mod parse_url;
mod resolve_ref;

pub use download::{
    archive_no_files_for_repo_path, choose_package_prefix_for_blob_path,
    collect_repo_relative_paths, download_and_extract, download_tarball_bytes,
    extract_tarball_with_prefix, parent_dir_in_repo, path_in_repo_looks_like_file,
};
pub use list_tags::list_tags;
pub use parse_url::{
    canonical_github_tree_url, github_source_from_segments, normalized_identity, parse_github_url,
    GitHubSource,
};
pub use resolve_ref::resolve_ref_to_sha;

/// Return a GitHub API bearer token from `GITHUB_TOKEN` or `GH_TOKEN`, if set.
pub(crate) fn github_token() -> Option<String> {
    std::env::var("GITHUB_TOKEN")
        .or_else(|_| std::env::var("GH_TOKEN"))
        .ok()
}
