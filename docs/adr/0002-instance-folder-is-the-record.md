# Each Instance's folder is its only record

An Instance exists exactly when its folder under `<data root>/instances/` holds a valid Instance
record: a plain `KEY=value` file. The tool generates the Instance's `compose.yaml` and its Proxy site
file from the record and overwrites any hand edits. When the disk and Docker disagree, the disk wins.

We chose this over a central registry (the earlier tools' YAML files), because a registry drifts from the
folders it describes and needs its own locking against concurrent writes. We chose it over Docker
labels as the record, because labels disappear with the container and can't be changed without
recreating it. We chose it over one shared Compose file, because every change would edit shared YAML
and restart more than one Instance. `KEY=value` keeps the record readable from any language, so it
doesn't commit the tool's form or language.

## Consequences

- A tool-labelled container with no record is an Orphan: reported, never removed automatically.
- There is no escape hatch for hand-tuning `compose.yaml` in v1. Per-Instance Actual settings must
  later go into the record as an allow-list, not into the derived files.
- Listing Instances means scanning folders. Half-built and half-deleted folders (`.<name>.creating/`, `.<name>.removing/`) must be ignored.
