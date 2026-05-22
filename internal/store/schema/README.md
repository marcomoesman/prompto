# Store schema migrations

Each `NNN_description.sql` file is one migration applied in numeric order.
Once a migration is shipped, **never edit it** — add a new migration file
that layers the change on top. The migration runner records applied versions
in `schema_version`.
