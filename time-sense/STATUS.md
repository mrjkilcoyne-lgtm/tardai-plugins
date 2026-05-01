# time-sense — STATUS

**State**: shipped, fully functional.
**Confirm tier**: OFF (read-only-cluster + none → OFF).

## Stateless design choice
Per sovereign's spec, simpler to keep stateless and let sovereign pass
the reference timestamp via `relative_to`. The plugin tracks its own
boot time as `boot_wall` / `uptime_s` so callers can observe pod
restarts and monotonic continuity.

## Behaviour
- `now_iso`, `now_epoch`, `now_human` — three time formats simultaneously
- `monotonic` — process monotonic counter (jump-free)
- `uptime_s` — seconds since this pod started
- `boot_wall` — ISO wall-clock at pod start
- `delta_s` — set if `relative_to` ISO timestamp provided, else null

## Smallest plugin in the tree
~80 LoC including manifest + registration boilerplate. No external deps
beyond the shared FastAPI/httpx stack. Idle memory expected ~30Mi.
