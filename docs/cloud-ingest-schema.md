# chainkit-cloud ingest schema

The wire format used by `chainkit/cloudagent` to push telemetry to chainkit-cloud.

This document is the single source of truth for the format. It is referenced by:

- `chainkit/cloudagent` — produces these payloads.
- `chainkit-cloud-srv` — consumes them at `POST /v1/ingest`.

Schema versions are pinned by the `schema` field in every payload. Cloud-srv rejects unknown major versions with HTTP 400. Minor additions (new optional fields) do not bump the major version; consumers must ignore unknown fields.

Current version: **`chainkit.ingest.v1`**.

---

## Transport

- **Method**: `POST /v1/ingest`
- **Endpoint**: `<Endpoint from cloudagent.Options>` + `/v1/ingest`
- **Auth**: `Authorization: Bearer ck_<env>_<random>`
- **Content-Type**: `application/json`
- **Request body**: a single `Batch` object (see below)
- **Success response**: `202 Accepted` with empty body
- **Failure responses**:
  - `400 Bad Request` — schema validation, allowlist violation (see Privacy)
  - `401 Unauthorized` — missing/revoked/wrong API key
  - `429 Too Many Requests` — plan-based rate limit exceeded; agent should backoff
  - `5xx` — transient; agent retries with exponential backoff capped at `MaxBackoff`

## Batch

```json
{
  "schema": "chainkit.ingest.v1",
  "agent":  "chainkit-go-sdk/0.x cloudagent/1",
  "batch_id": "01JKZG3...",
  "sent_at":  "2026-05-10T15:30:00.123Z",
  "events":   [ /* see Event */ ]
}
```

| field      | type       | notes |
| ---------- | ---------- | ----- |
| `schema`   | string     | Required. Always `chainkit.ingest.v1` for this version. |
| `agent`    | string     | Required. Agent identification, used by support to triage version-specific bugs. Format: free-form, but should include the chainkit SDK version and cloudagent version. |
| `batch_id` | string     | Required. ULID or UUID. Used for idempotency dedup at the cloud — duplicate `batch_id` from the same project is rejected with `200 OK` and treated as already-ingested. |
| `sent_at`  | RFC3339    | Required. The agent's wall-clock when the batch was flushed. |
| `events`   | array      | Required, may be empty. Capped at the agent's configured `BatchSize`. Cloud-srv enforces a hard ceiling of `1000` events per batch — exceeding this returns `400`. |

## Event

Two kinds of events share the same wrapper. The `kind` field discriminates.

```json
{
  "id":  "01JKZG3...",
  "t":   "2026-05-10T15:30:00.123Z",
  "kind": "req" | "score",
  /* exactly one of: */
  "req":   { /* RequestPayload */ },
  "score": { /* ScorePayload */ }
}
```

| field | type    | notes |
| ----- | ------- | ----- |
| `id`  | string  | Required. ULID, agent-generated, monotonic per batch. Used for `(project_id, event.id)` dedup at the cloud over a 5-minute Redis window. |
| `t`   | RFC3339 | Required. Wall-clock when the event was captured by the SDK. |
| `kind`| enum    | Required. `"req"` for an SDK operation event; `"score"` for an adaptive-scoring event. |
| `req` | object  | Present iff `kind == "req"`. |
| `score`| object | Present iff `kind == "score"`. |

### `req` payload (RequestEvent)

Mirrors `chainkit.RequestEvent` (see `metrics.go`). Emitted once per top-level SDK call (`GetUTXOs`, `PushTx`, …).

```json
{
  "provider": "mempool",
  "operation": "GetUTXOs",
  "ok": true,
  "dur_ms": 142,
  "attempts": 1,
  "err": null
}
```

| field      | type    | notes |
| ---------- | ------- | ----- |
| `provider` | string  | Name of the provider that ultimately served the request, or empty when every provider in the chain failed. |
| `operation`| string  | SDK method name (`GetUTXOs`, `PushTx`, `DeriveAddress`, …). |
| `ok`       | bool    | `true` iff the operation returned without error. |
| `dur_ms`   | integer | Total wall-clock latency, including time spent on failed providers and retry backoff. The latency the caller actually observed. |
| `attempts` | integer | Total number of provider invocations across the entire fallback chain. A successful first call is `1`. |
| `err`      | enum or null | Error class; one of `auth`, `config`, `timeout`, `rate_limit`, `other`. Null when `ok` is true. |

### `score` payload (ScoreEvent)

Mirrors `cloudagent.ScoreEvent`. The `type` field discriminates between the seven `scoring/metrics.Recorder` methods.

```json
{
  "type": "latency" | "score_change" | "effective" | "event" | "store_op" | "cache_hit" | "provider_rank",
  "provider": "mempool",
  /* fields below depend on type */
  "score_after": 0.92,
  "rt_ms":       142,
  "event_type":  "operation_success",
  "score_type":  "latency_penalty",
  "old_value":   100.0,
  "new_value":   85.5,
  "store":       "redis",
  "operation":   "GetUTXOs",
  "rank":        1,
  "total_providers": 3,
  "cache_hit":   true,
  "success":     true,
  "store_err":   "error" | null
}
```

Only the fields relevant to a given `type` are populated; all others are omitted or zero. Consumers must tolerate omitted/extra fields.

---

## Privacy allowlist (enforced)

The SDK and cloudagent **never** transmit:

- Bitcoin/EVM/Solana addresses, xpubs, private keys, WIFs, transaction IDs
- Transaction amounts (sat, BTC, wei, lamports), fees, balances
- Raw error strings (only the classified `err` enum is forwarded)
- Customer-side request IDs, IP addresses, user agents
- Anything inside `OperationMetadata.Extra`

Cloud-srv validates this at ingest. Any payload containing a top-level field whose name matches `address|xpub|tx|amount|sat|wif|key` is rejected with HTTP 400 and the offending field name in the response body. This is a defence-in-depth line — the SDK already prevents these fields from being constructed in the first place.

---

## Retention & rollups

These are cloud-srv concerns; documented here so SDK-side consumers understand what becomes queryable.

- Raw events: 7 days (free plan), longer per paid plan.
- 1-minute continuous aggregates: 30 days.
- 1-hour rollups: 13 months.

---

## Schema evolution rules

1. **Additive changes** (new optional fields, new enum members) ship under the same major version. Consumers must ignore unknown fields and treat unknown enum members as `other`.
2. **Removals or renames** require a new major version (`chainkit.ingest.v2`). The agent and cloud-srv negotiate via the `schema` field; the cloud rejects unknown majors with HTTP 400.
3. The privacy allowlist is **not** subject to additive evolution — adding a field that could carry user data requires a privacy-policy update and an explicit allow.

---

## Reference

- Producer: [`cloudagent`](../cloudagent) (`MetricsRecorder`, `ScoringRecorder`).
- Consumer: `chainkit-cloud-srv` `POST /v1/ingest` handler (M4 in the cloud build sequence).
