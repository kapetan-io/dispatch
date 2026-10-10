# Supplier API

- `GET /stock` returns every SKU's level. It counts against the rate limit.
- Webhooks. The supplier posts a signed `stock.changed` event to a URL we register, within seconds of any level change, carrying the SKU and the new level. Delivery is retried for 24 hours, and events can arrive out of order or twice.
- `GET /stock?since=<timestamp>` returns only SKUs that changed since the timestamp.
