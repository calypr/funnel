---
title: Compute
menu:
  main:
    weight: -5
---

# Compute

## Listing scheduler nodes

The Funnel scheduler extension exposes `GET /v1/nodes`. The first request can
omit pagination parameters; it returns at most 500 nodes. Set `page_size` to a
value from 1 through 1000 to choose a page size. Each response includes
`next_page_token` when more nodes are available. Pass that opaque value as
`page_token` to fetch the next page. An empty `next_page_token` marks the end.

```sh
curl --get 'http://localhost:8000/v1/nodes' \
  --data-urlencode 'page_size=100'

# Use the next_page_token from the previous response.
curl --get 'http://localhost:8000/v1/nodes' \
  --data-urlencode 'page_size=100' \
  --data-urlencode 'page_token=<next_page_token>'
```

The token is a live continuation cursor, not a snapshot. Concurrent node
changes can affect later pages. Clients that previously expected one response
to contain every node must follow `next_page_token` until it is empty.
