# 7 — Container registry

Everything here happens in the **GitHub UI**, not on the box. The `publish` job in
`.github/workflows/ci.yml` builds the ARM image on every push to `main`, pushes it
to GitHub Container Registry as `ghcr.io/abir66/mdfly:<commit-sha>`, and prunes to
the newest 10 versions (ADR-0015). This page is what you check once, after the
first run, so that job keeps working.

Package names on GHCR are lowercase: the repo is `Abir66/mdfly`, the image is
`ghcr.io/abir66/mdfly`.

## 1. Workflow permissions

The `publish` job asks for `packages: write` explicitly, so the repo default does
not have to be permissive. Confirm it anyway:

1. Repo → **Settings** → **Actions** → **General**
2. Scroll to **Workflow permissions**
3. Either option works. If a publish run fails with `403 Forbidden` on the push or
   on a `DELETE .../versions/<id>` call, select **Read and write permissions** and
   **Save**.

**If the setting is greyed out**, an organisation policy is overriding it:
Organisation → **Settings** → **Actions** → **General** → **Workflow permissions**.
Change it there, or — if you cannot — create a classic personal access token with
the `write:packages` and `delete:packages` scopes
(github.com/settings/tokens), store it as repo secret `GHCR_TOKEN`
(Repo → **Settings** → **Secrets and variables** → **Actions** → **New repository
secret**), and replace both `${{ secrets.GITHUB_TOKEN }}` references in the
`publish` job with `${{ secrets.GHCR_TOKEN }}`.

## 2. Package visibility

The package appears only **after the first successful publish run**.

1. Repo home page → right sidebar → **Packages** → **mdfly**
   (direct: `https://github.com/users/Abir66/packages/container/package/mdfly`)
2. The header next to the package name must read **Private**.

A first publish from a private repo creates a private package. If it ever reads
**Public**: package page → **Package settings** (gear, right side) → **Danger
Zone** → **Change visibility** → **Private**. ADR-0015 declined public images on
purpose — the binary holds no secrets, but its SQL and route names are readable
strings inside it.

## 3. Package-to-repo linkage

The prune step deletes versions with the repo-scoped workflow token, and that only
works while the package is linked to this repo.

1. Package page → the **Source repository** line in the right sidebar should read
   `Abir66/mdfly`. The workflow sets it from the
   `org.opencontainers.image.source` label, so it should be there from the first
   push.
2. Package page → **Package settings** → **Manage Actions access**. `Abir66/mdfly`
   must be listed with the **Write** role. If it is not: **Add repository** →
   `mdfly` → set role to **Write**.

An unlinked package publishes fine and then silently fails to prune, which is how
you would discover it at 500 MB.

## 4. Storage and transfer budget

- **Storage**: Account → **Settings** → **Billing and licensing** → **Plans and
  usage** → **Storage for Actions and Packages**
  (direct: `https://github.com/settings/billing`). The free private-package
  allowance is **500 MB**; a version is roughly 25 MB, so 10 retained versions sit
  around 250 MB.
- **Transfer out**: same page, **Data transfer out** — free for pulls made from
  inside GitHub Actions, metered for pulls from the box. One deploy is one pull.

If storage creeps up despite the prune, check §3 first: an unlinked package is the
usual cause.

## 5. What "done" looks like

1. **A version tagged with the commit SHA.** Package page → **Versions** tab
   (`https://github.com/users/Abir66/packages/container/mdfly/versions`). The
   newest row's tag is the full 40-character SHA of the `main` commit that just
   built.
2. **It pulls and runs anywhere.** With a token that can read packages
   (`read:packages` — the box gets its own, created and used in
   [step 4.4](4-deploy.md#44-registry-login)):

   ```sh
   echo "$CR_PAT" | docker login ghcr.io -u Abir66 --password-stdin
   docker pull ghcr.io/abir66/mdfly:<sha>
   docker run --rm ghcr.io/abir66/mdfly:<sha> --help
   ```

   The last command prints `usage: mdfly-server <serve|jobs>` — both process roles
   are in the one image (ADR-0003).
3. **A prune line in the log.** Actions → the run → **Publish image** → **Prune old
   image versions**. Until there are more than 10 versions it reads `Nothing to
   prune`; after that, one `Deleting version <id> (tags: …)` line per dropped
   version.
4. **Branches publish nothing.** Push a branch and open its run: `lint`, `test`,
   `migrate` and `build` execute, `publish` is skipped.
