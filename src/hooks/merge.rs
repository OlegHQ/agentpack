use super::ir::HookBundle;

pub fn sort_bundle(bundle: &mut HookBundle) {
    bundle.hooks.sort_by(|a, b| {
        a.origin
            .layer
            .sort_rank()
            .cmp(&b.origin.layer.sort_rank())
            .then_with(|| a.origin.module.cmp(&b.origin.module))
            .then_with(|| a.origin.source_rel.cmp(&b.origin.source_rel))
            .then_with(|| a.origin.event_index.cmp(&b.origin.event_index))
            .then_with(|| {
                a.origin
                    .matcher_group_index
                    .cmp(&b.origin.matcher_group_index)
            })
            .then_with(|| a.origin.hook_index.cmp(&b.origin.hook_index))
    });
}
