# Contrib Packages

Contrib packages live under `contrib/` and are reserved for optional
extensions that **pull in external dependencies**. Each package has its own
`go.mod`, so importing one does not leak its dependencies into your main
module. Take only what you need.

If a package has no external dependencies and would be useful to most
users, it lives at the top level alongside `kernel`, `workflow`, and
`middleware` — not in `contrib/`.

| Package | Guide | Purpose |
|---|---|---|
| `contrib/mongo` | [Mongo Guide](contrib/mongo.md) | MongoDB-backed `HistoryStore` and `MemoryStore` |
