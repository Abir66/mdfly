# Documents are mutable in place, no version history

A slug is a stable, forever pointer to "the latest Bundle the owner published under it." `mdfly update <slug>` replaces the Bundle; orphaned Assets are garbage-collected. We do not retain prior versions and do not expose a `@version` URL form.

We chose this over immutable-per-publish (every update = new slug) and over mutable-with-history because the dominant user story is "I shared this URL, let me fix the typo" — breaking the shared URL is a worse failure than losing the pre-typo bytes. Readers who need byte-exact archival should snapshot externally. A versioned mode can be layered on later under an opt-in flag without changing the default URL contract.
