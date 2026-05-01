# tardai-plugins

Tier 0 plugins for the [tardai-tool-bus](https://github.com/mrjkilcoyne-lgtm/tardai-tool-bus). Three tiny FastAPI services, each registered with the Bus on startup.

## Plugins

| id                     | purpose                                              | blast_radius      | sensitivity |
|------------------------|------------------------------------------------------|-------------------|-------------|
| `self-artefact-read`   | GET a file from the artefact write surface.         | read-only-cluster | none        |
| `self-artefact-list`   | LIST files under a prefix on the artefact surface.  | read-only-cluster | none        |
| `tool-bus-introspect`  | Enriched view of the Bus's own manifest. Recursion. | read-only-cluster | none        |

## Layout

```
tardai-plugins/
├── self-artefact-read/      app.py manifest.yaml requirements.txt Dockerfile k8s/
├── self-artefact-list/      app.py manifest.yaml requirements.txt Dockerfile k8s/
├── tool-bus-introspect/     app.py manifest.yaml requirements.txt Dockerfile k8s/
└── .github/workflows/build-push.yml   # matrix build, one image per plugin
```

One repo, three subdirs, one matrix workflow. Each plugin builds to `ghcr.io/mrjkilcoyne-lgtm/tardai-plugins-<id>:latest`.

## Self-registration

Each plugin POSTs its manifest to `http://tardai-tool-bus.tardai.svc.cluster.local:8000/api/tools/register` on startup, retrying with backoff. The Bus's manifest reflects what is actually running — pods that aren't up don't show up.

## Deploy order

1. `kubectl apply -f tardai-tool-bus/k8s/` (Bus first)
2. Verify `/api/tools/manifest` returns `[]` (sovereign milestone)
3. Then each plugin: `kubectl apply -f tardai-plugins/self-artefact-read/k8s/` etc.
4. Watch Bus logs for `[bus] registered tool: <id>`.
