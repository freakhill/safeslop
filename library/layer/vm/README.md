# library/layer/vm/

Disposable VM scaffolding. The Tart-based brew sandbox is the only VM workflow today; its assumptions and defaults are documented as a recipe at [`../../task/evaluate-formulae/`](../../task/evaluate-formulae/) and driven by [`slop-brew-vm`](../../../scripts/slop-brew-vm.fish).

If/when more VM workflows are added (a separate Linux clone for build experiments, a Windows guest for a Windows-only tool, etc.) their per-VM settings would land here. Today the directory is intentionally a pointer.
