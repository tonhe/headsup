# REST fixtures

Request/response pairs for `internal/api/httpapi` tests, one JSON file per
exchange: `{name, request: {method, path, query?, body?}, response: {status,
headers, body}}`.

These are **hand-built from the OpenAPI documents** (headscale v0.29.6
`gen/openapiv2/headscale/v1/headscale.swagger.json` for `v029/`, main's
`hscontrol/api/v1` Huma definitions for `v030/`), pending real recordings
from running servers. Replace them with captured traffic when available.

- `v029/`: grpc-gateway (protojson camelCase, uint64 as strings, errors as
  `{code, message, details}`; expire options as query parameters).
- `v030/`: Huma (camelCase, uint64 as strings, errors as RFC 7807
  `application/problem+json`; expire options in the JSON body).
