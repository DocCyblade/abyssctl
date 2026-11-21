# Restore brings back the Backup's Actual version and Regroups offline

A Restore puts back the Instance's data together with the Actual version and image digest recorded in the Backup,
so restored data always runs on the code that made it. Before the Instance starts again, the tool Regroups the
restored data offline: with the container stopped, every `files` row in `server-files/account.sqlite` with a non-null `group_id` gets a
new uuidv4, and each `user-files/group-<old>.sqlite` is **renamed** to `group-<new>.sqlite`. The tool does this
itself with a pure-Go SQLite library (`modernc.org/sqlite`, no cgo). A missing group file fails the Restore.

We chose to restore the Backup's version over keeping the Instance's current one, because Actual can't run data on
an older version than the one that migrated it: the server exits with `Missing migration file`. Keeping the current
version would break whenever the Backup was made by a newer version than the Instance now runs, for example after
an earlier Restore to an older Backup. Restoring older data onto a newer image would instead run that version's
migrations without an Upgrade's `pre-upgrade` Backup and checks. We chose to Regroup over a plain copy, because
without it a device that synced after the Backup silently pushes its newer changes back and undoes the Restore.
After a Regroup, devices get `file-has-reset` and offer to revert, which downloads the restored budget under the
new `group_id`.

We chose renaming the group file over deleting it, because the group file holds sync messages that are not yet in
the budget snapshot: in a test against Actual 26.11.0, deleting it gave an empty budget on the next download and
lost data that was in the Backup. We chose an offline edit over Actual's own reset-sync endpoint
(`POST /reset-user-file`) on the running server, which deletes the group file and leaves every device on
`file-needs-upload` until one re-uploads its own copy, and over a one-shot container from the Instance's image,
which ties the Regroup to tooling inside an image the tool doesn't control.

## Consequences

- `modernc.org/sqlite` is one of v1's three dependencies, and only the Backup module imports it. It is pure Go, so
  the binary stays static with no cgo ([ADR 0003](0003-single-static-go-binary.md)).
- Restore pulls the Backup's image by digest before it stops the Instance, and refuses if the pull fails.
  `HOSTNAME` and `STATE` in the Instance record stay as they were. A Restore can move an Instance back to an
  older Actual version; `status` then shows that an Upgrade is available.
- Changes a device made after the Backup are lost when it reverts. This is the intended outcome, and the
  Operator docs must say so. Changes that had already synced to the server stay in the `pre-restore` Backup.
- Upgrade's automatic Rollback follows from this decision: it puts back the `pre-upgrade` Backup's data, Actual
  version and digest, through the same data swap as Restore. It Regroups only if the new version ever passed
  `/health`. Before that, the tool never saw the new version serving, and the listener starts only
  after migrations succeed.
- The Regroup depends on Actual's on-disk layout (`files.group_id`, `group-<id>.sqlite`). A change there breaks
  Restore, so the Backup and Restore journey test checks that old clients get `file-has-reset` and that a fresh
  download holds only what was in the Backup.
