# GitHub is the sole identity provider

Owned Documents require the User to authenticate via GitHub OAuth (device flow, initiated by `mdfly login`). The User's GitHub login is reused verbatim as the `<user>` segment in `mdfly.dev/<user>/<slug>` URLs — mdfly does not maintain its own username namespace.

We chose this over email/magic-link and over multi-provider OAuth because the target audience is CLI users sharing markdown (overwhelmingly devs with GitHub accounts), the device flow avoids running a localhost callback inside the CLI, and reusing GitHub handles sidesteps username collisions and squatting. The cost is provider lock-in: adding a second identity source later cannot retroactively merge accounts, and a user who loses their GitHub account loses access to their Documents.
