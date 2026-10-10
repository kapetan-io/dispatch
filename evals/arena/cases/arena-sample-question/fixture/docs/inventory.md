# Inventory service

The inventory service keeps a local copy of the supplier's stock levels so the storefront can show "in stock" without a round trip. A job calls the supplier API's `GET /stock` every 10 minutes and overwrites the local copy. The storefront has oversold twice this quarter, both times within minutes of a supplier stock change.

Constraints that are settled: the local copy must stay (the storefront can't call the supplier on a page view), and the supplier API allows 600 calls an hour per key.
