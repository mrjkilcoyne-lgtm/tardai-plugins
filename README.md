# tardai-plugins

10 plugins for the [tardai-tool-bus](https://github.com/mrjkilcoyne-lgtm/tardai-tool-bus).
Single Go module, one binary per plugin, distroless images.

This repo was migrated from Python/FastAPI (~38 MiB resident each) to Go
single-binary (~5–10 MiB each) on 2026-05-01 to recover ~300 MiB of node
memory on the single-node Civo cluster. Same Tool Bus contracts, same
manifests, same governance — thinner runtime.

## Plugins

| id | tier | blast_radius | two-phase | sensitivity |
|---|---|---|---|---|
| `self-artefact-read` | 0 | read-only-cluster | no | none |
| `self-artefact-list` | 0 | read-only-cluster | no | none |
| `tool-bus-introspect` | 0 | read-only-cluster | no | none |
| `http-egress` | 1 | write-external | yes | none |
| `mempalace-read` | 1 | read-only-external | no | none |
| `mempalace-search` | 1 | read-only-external | no | none |
| `pod-introspect` | 1 | read-only-cluster | no | none |
| `time-sense` | 1 | read-only-cluster | no | none |
| `cost-sense` | 1 | read-only-external | no | financial |
| `mandate-tracking` | 1 | read-only-cluster | no | none |

## Layout

```
tardai-plugins/
├── go.mod
├── go.sum
├── Dockerfile                  # parameterised by --build-arg PLUGIN
├── pkg/plugin/                 # shared scaffold (auth, register, audit, plan, invoke)
├── cmd/<id>/main.go            # one main per plugin (~30-100 lines)
├── manifests/                  # k8s Deployment + Service per plugin (Go resource profile)
│   ├── shared-policy.yaml      # http-egress allowlist ConfigMap
│   └── <id>.yaml
├── .github/workflows/build-push.yml
└── <legacy plugin dirs>/       # original Python sources retained for reference
```

The legacy Python source directories (`self-artefact-read/`, etc.) are
retained on disk for reference but are no longer the build context.
The GHA workflow builds the root Dockerfile with `--build-arg PLUGIN=<id>`.

## Build pattern

```dockerfile
FROM golang:1.23-alpine AS builder
ARG PLUGIN
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY pkg ./pkg
COPY cmd ./cmd
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /out/plugin ./cmd/${PLUGIN}

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/plugin /plugin
USER nonroot
ENTRYPOINT ["/plugin"]
```

## Resource profile

Per-plugin requests: `cpu=5m, memory=5Mi`. Limits: `cpu=100m, memory=32Mi`.
Boots in <100 ms. Compare with the Python pods at `48 MiB` request each.

## Rolling deploy

Use `./rollout-go.sh` after the GHA matrix has pushed the 10 images:

1. Sets the new image on each Deployment (one at a time).
2. Updates resource requests to the Go profile.
3. Restarts the rollout, waits up to 90s for ready.
4. Verifies the plugin reappears in the Bus's `/api/tools/manifest`.
5. On failure: rolls back to the previous image and continues.

## Honesty / known gaps

See `STATUS.md`. Behaviour preserved from the Python plugins exactly:
`mempalace-read`/`mempalace-search` still 502 (MemPalace API undocumented),
`cost-sense` Anthropic admin still stubbed, Civo/Vercel creds absent.
Those are pre-existing gaps; this rewrite did not fix or change them.
