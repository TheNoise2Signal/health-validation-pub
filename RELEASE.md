# Release Process

This repository has two independently versioned Go modules:

- `catalog/vX.Y.Z`
- `toolkit/vX.Y.Z`

See [Versioning and releases](docs/DEVELOPER.md#versioning-and-releases).

## Maintainer steps

1. Confirm that `main` CI passes.
2. Update `CHANGELOG.md`.
3. If toolkit requires a new catalog version, release catalog first and update the toolkit dependency.
4. Create the module tag from the tested `main` commit.
5. Push only the intended tag.
6. Confirm that the tag workflow completes.

Toolkit tags publish the multi-architecture image to GHCR. Catalog tags validate the catalog module and do not publish an image.
