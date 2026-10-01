# 0010 - Progress and summary outcome fixes

Follow-up to proposal 0070 and changelog/0009.

- Audit summaries and progress share one outcome classifier.
- Deployment failures, blocked package deletes, missing items, and guideline
  violations count as failures. Warnings alone do not.
- Package-only progress uses the package ID instead of a nil placeholder.
- Standalone deployment writes an item record, including the wait result.
- Regression tests check outcome counts, package-only records, and payload omission.
- An exact-path test checks nested directory creation and preservation of old records.
- POSIX permission assertions apply only on POSIX platforms. Windows access is
  controlled by directory ACLs; these tests do not certify an ACL configuration.

Verification on Windows, 2026-09-30: all Go package tests, build, and vet pass.
Command-level CLI checks and a POSIX run remain open. No release tag was created.
