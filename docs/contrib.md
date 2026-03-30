# Contrib Packages

Contrib packages are optional extensions to Axon that live in separate Go
modules under the `contrib/` directory. Each package has its own `go.mod`,
so importing one does not pull its dependencies into your main module. Take
only what you need.

| Package | Guide | Purpose |
|---|---|---|
| `contrib/plan` | [Plan Guide](contrib/plan.md) | Multi-step procedures driven by the LLM |
| `contrib/mongo` | [Mongo Guide](contrib/mongo.md) | MongoDB-backed `HistoryStore` and `MemoryStore` |
